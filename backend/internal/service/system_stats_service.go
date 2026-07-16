package service

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"nexus-panel/internal/repo"
)

// SystemStatsService 采集运行面板所在主机的实时系统状态
// 数据来源: /proc 文件系统（无第三方依赖）
// 注: 面板服务器自身的负载/速度指标。每个节点的负载/速度由 node_agent 上报，
//
//	仍由 gRPC traffic_service 路径维护。
type SystemStatsService struct {
	nodeRepo *repo.NodeRepo
	userRepo *repo.UserRepo

	mu           sync.Mutex
	lastNetRx    uint64
	lastNetTx    uint64
	lastSampleAt time.Time
}

// SystemStats 单次采集结果
type SystemStats struct {
	// CPU 1/5/15 分钟平均负载（load average）
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`

	// 内存（字节）
	MemTotal uint64  `json:"mem_total"`
	MemUsed  uint64  `json:"mem_used"`
	MemPct   float64 `json:"mem_pct"`

	// 磁盘（字节）
	DiskTotal uint64  `json:"disk_total"`
	DiskUsed  uint64  `json:"disk_used"`
	DiskPct   float64 `json:"disk_pct"`

	// 实时网络速度（bps，bits per second）
	// 第一字节为 nan，确保前端拿到的不是 0 而是上一周期到当前的瞬时值
	NetInBps  int64 `json:"net_in_bps"`
	NetOutBps int64 `json:"net_out_bps"`

	// 在线/总数
	OnlineNodes  int64 `json:"online_nodes"`
	TotalNodes   int64 `json:"total_nodes"`
	EnabledNodes int64 `json:"enabled_nodes"`
	OnlineUsers  int64 `json:"online_users"`
	TotalUsers   int64 `json:"total_users"`

	// 运行信息
	UptimeSec int64  `json:"uptime_sec"`
	Hostname  string `json:"hostname"`
	SampledAt int64  `json:"sampled_at"`
}

// NewSystemStatsService 构造函数
func NewSystemStatsService(nr *repo.NodeRepo, ur *repo.UserRepo) *SystemStatsService {
	return &SystemStatsService{
		nodeRepo: nr,
		userRepo: ur,
	}
}

// Collect 采集一次完整快照
func (s *SystemStatsService) Collect() (*SystemStats, error) {
	stats := &SystemStats{
		SampledAt: time.Now().Unix(),
	}

	// hostname
	if h, err := os.Hostname(); err == nil {
		stats.Hostname = h
	}

	// loadavg
	if l1, l5, l15, err := readLoadAvg(); err == nil {
		stats.Load1, stats.Load5, stats.Load15 = l1, l5, l15
	}

	// meminfo
	if total, available, err := readMemInfo(); err == nil {
		stats.MemTotal = total
		stats.MemUsed = total - available
		if total > 0 {
			stats.MemPct = roundTo(float64(stats.MemUsed)*100/float64(total), 2)
		}
	}

	// disk usage of "/"
	if total, free, err := readDiskUsage("/"); err == nil {
		stats.DiskTotal = total
		stats.DiskUsed = total - free
		if total > 0 {
			stats.DiskPct = roundTo(float64(stats.DiskUsed)*100/float64(total), 2)
		}
	}

	// net speed（基于 /proc/net/dev 两次采样差分）
	rx, tx, err := readNetTotalBytes()
	if err == nil {
		s.mu.Lock()
		now := time.Now()
		if !s.lastSampleAt.IsZero() && rx >= s.lastNetRx && tx >= s.lastNetTx {
			elapsed := now.Sub(s.lastSampleAt).Seconds()
			if elapsed > 0 {
				stats.NetInBps = int64(float64(rx-s.lastNetRx) * 8 / elapsed)
				stats.NetOutBps = int64(float64(tx-s.lastNetTx) * 8 / elapsed)
				// 防止异常突刺：上限 100Gbps
				const cap = int64(100 * 1024 * 1024 * 1024)
				if stats.NetInBps > cap {
					stats.NetInBps = cap
				}
				if stats.NetOutBps > cap {
					stats.NetOutBps = cap
				}
			}
		}
		s.lastNetRx = rx
		s.lastNetTx = tx
		s.lastSampleAt = now
		s.mu.Unlock()
	}

	// uptime
	if up, err := readUptime(); err == nil {
		stats.UptimeSec = int64(up)
	}

	// 节点统计
	if s.nodeRepo != nil {
		if v, err := s.nodeRepo.CountAll(); err == nil {
			stats.TotalNodes = v
		}
		if v, err := s.nodeRepo.CountOnline(); err == nil {
			stats.OnlineNodes = v
		}
		if v, err := s.nodeRepo.CountEnabled(); err == nil {
			stats.EnabledNodes = v
		}
	}

	// 用户统计
	if s.userRepo != nil {
		if v, err := s.userRepo.CountAll(); err == nil {
			stats.TotalUsers = v
		}
		if v, err := s.userRepo.CountActive(); err == nil {
			stats.OnlineUsers = v
		}
	}

	return stats, nil
}

// CollectSimple 给用户端的精简版：只显示面板自身负载与网络速度（不含详细节点数）
func (s *SystemStatsService) CollectSimple() (*SystemStats, error) {
	full, err := s.Collect()
	if err != nil {
		return nil, err
	}
	// 用户端隐藏内部节点/用户统计
	simple := &SystemStats{
		Load1:     full.Load1,
		Load5:     full.Load5,
		Load15:    full.Load15,
		MemTotal:  full.MemTotal,
		MemUsed:   full.MemUsed,
		MemPct:    full.MemPct,
		NetInBps:  full.NetInBps,
		NetOutBps: full.NetOutBps,
		UptimeSec: full.UptimeSec,
		Hostname:  full.Hostname,
		SampledAt: full.SampledAt,
	}
	return simple, nil
}

// ============================================================
// /proc 解析辅助
// ============================================================

func readLoadAvg() (float64, float64, float64, error) {
	f, err := os.Open("/proc/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, 0, 0, fmt.Errorf("loadavg empty")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("loadavg format unexpected")
	}
	l1, e1 := strconv.ParseFloat(fields[0], 64)
	l5, e2 := strconv.ParseFloat(fields[1], 64)
	l15, e3 := strconv.ParseFloat(fields[2], 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, fmt.Errorf("loadavg parse error")
	}
	return l1, l5, l15, nil
}

func readMemInfo() (total, available uint64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		var k string
		var v uint64
		if _, err := fmt.Sscanf(line, "%s %d", &k, &v); err != nil {
			continue
		}
		switch k {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			available = v * 1024
		}
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("meminfo: MemTotal not found")
	}
	return total, available, nil
}

func readNetTotalBytes() (rx, tx uint64, err error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		// 跳过两行表头
		if lineNo <= 2 {
			continue
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// 形如: eth0: 1234 5678 ...
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colon])
		// 忽略回环
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		// /proc/net/dev 列: bytes packets errs drop fifo frame compressed multicast | ...
		// receive: bytes at 0, transmit: bytes at 8
		r, errR := strconv.ParseUint(fields[0], 10, 64)
		t, errT := strconv.ParseUint(fields[8], 10, 64)
		if errR == nil {
			rx += r
		}
		if errT == nil {
			tx += t
		}
	}
	if rx == 0 && tx == 0 {
		return 0, 0, fmt.Errorf("no net interface data")
	}
	return rx, tx, nil
}

func readUptime() (float64, error) {
	f, err := os.Open("/proc/uptime")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var up, idle float64
	if _, err := fmt.Fscan(f, &up, &idle); err != nil {
		return 0, err
	}
	return up, nil
}

func roundTo(v float64, digits int) float64 {
	mul := 1.0
	for i := 0; i < digits; i++ {
		mul *= 10
	}
	return float64(int64(v*mul+0.5)) / mul
}

// readDiskUsage 通过 statfs 读取指定路径所在文件系统的容量与剩余字节数
func readDiskUsage(path string) (total, free uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	// Bavail: 非超级用户可用的块数；Blocks: 总块数；Bsize: 块大小
	total = stat.Blocks * uint64(stat.Bsize)
	free = stat.Bavail * uint64(stat.Bsize)
	return total, free, nil
}
