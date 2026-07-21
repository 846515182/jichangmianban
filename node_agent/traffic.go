package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
)

// TrafficCounter 节点级流量计数器，基于 /proc/net/dev 读取增量
// 第一版只做节点级流量汇总；用户级精确统计留 TODO
//
// P1-AG10: 引入 pendingRx/pendingTx/pendingValid 字段, Peek 读到的值存到 pending,
// Commit 用 pending 值(而不是重新读 /proc)更新 prev。避免 Peek 与 Commit 之间
// 网卡计数器继续增长导致中间增量丢失(原实现 Commit 重新读 /proc 会把 prev 跳到最新值,
// Peek→Commit 期间的新增流量既不算本次上报也不算下次上报, 永久丢失)。
type TrafficCounter struct {
	mu   sync.Mutex
	prevRx int64
	prevTx int64
	hasPrev bool
	// pendingRx/pendingTx: Peek 时读到的 rx/tx, Commit 时用于更新 prev
	// pendingValid: Peek 是否成功读取(只在 valid 时 Commit 才更新 prev)
	pendingRx    int64
	pendingTx    int64
	pendingValid bool
	totalRx      int64 // 累计下载字节（自进程启动）
	totalTx      int64 // 累计上传字节（自进程启动）
}

// NewTrafficCounter 创建流量计数器
func NewTrafficCounter() *TrafficCounter {
	return &TrafficCounter{}
}

// readNetDevTotal 读取 /proc/net/dev，汇总所有非 lo 接口的 rx/tx 字节数
// P1-AG11: 失败时返回 error, 调用方(Peek)据此决定不更新 prev 也不上报
func readNetDevTotal() (rx, tx int64, err error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo <= 2 {
			continue // 跳过两行表头
		}
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}
		// fields[0] = rx 字节, fields[8] = tx 字节
		if v, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
			rx += v
		}
		if v, err := strconv.ParseInt(fields[8], 10, 64); err == nil {
			tx += v
		}
	}
	return rx, tx, nil
}

// Peek 返回自上次提交以来的上传/下载增量, 但不消费基线(不更新 prev)
// 用于"上报成功才消费增量"的语义, 避免上报失败导致增量数据永久丢失(P1-B4)
//
// P1-AG10: 读到的 rx/tx 存到 pending, 供 Commit 使用(避免 Commit 重新读 /proc 导致中间增量丢失)
// P1-AG11: /proc 读取失败时不更新 prev 也不上报, 返回 0,0(不报错, 只是不上报),
// 避免短暂的 /proc 异常导致 prev 被错误清零或上报异常值
func (t *TrafficCounter) Peek() (upload, download int64) {
	rx, tx, err := readNetDevTotal()
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		// P1-AG11: /proc 读取失败, 不更新 prev 也不上报, pending 标记为无效
		// 下次 Peek 会重新读 /proc, 成功后才更新 pending; 失败期间流量不上报但不丢失
		// (因为 prev 没变, 下次成功的 Peek 会把这段未上报的增量一起算上)
		t.pendingValid = false
		return 0, 0
	}
	// P1-AG10: 把读到的 rx/tx 存到 pending, Commit 时用(避免 Commit 重新读 /proc 导致中间增量丢失)
	t.pendingRx = rx
	t.pendingTx = tx
	t.pendingValid = true
	if !t.hasPrev {
		t.prevRx = rx
		t.prevTx = tx
		t.hasPrev = true
		return 0, 0
	}
	upload = rx - t.prevRx   // rx = 服务器接收 = 用户上传
	download = tx - t.prevTx // tx = 服务器发送 = 用户下载
	if upload < 0 {
		upload = 0
	}
	if download < 0 {
		download = 0
	}
	return upload, download
}

// Commit 确认增量已成功上报, 更新基线并累加总量(仅在上报成功后调用)
// P1-AG10: 用 Peek 时存的 pending 值更新 prev, 避免重新读 /proc 导致中间增量丢失
// 若 pending 无效(Peek 期间 /proc 读取失败), 不更新 prev(下次 Peek 会重算增量)
func (t *TrafficCounter) Commit(upload, download int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pendingValid {
		t.prevRx = t.pendingRx
		t.prevTx = t.pendingTx
		t.pendingValid = false
	}
	t.hasPrev = true
	t.totalRx += upload
	t.totalTx += download
}

// Delta 返回并消费增量(等价于 Peek + Commit, 兼容旧用法)
// 从服务器视角: rx=网卡接收=用户上传, tx=网卡发送=用户下载
func (t *TrafficCounter) Delta() (upload, download int64) {
	upload, download = t.Peek()
	t.Commit(upload, download)
	return upload, download
}

// Total 返回自进程启动以来的累计下载和上传字节数
func (t *TrafficCounter) Total() (download, upload int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.totalRx, t.totalTx
}

// readMemInfo 读取 /proc/meminfo，返回(总内存, 已用内存)字节
func readMemInfo() (total, used int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	var memFree, memAvailable, buffers, cached int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		val, _ := strconv.ParseInt(parts[1], 10, 64)
		val *= 1024 // /proc/meminfo 单位是 kB
		switch parts[0] {
		case "MemTotal:":
			total = val
		case "MemFree:":
			memFree = val
		case "MemAvailable:":
			memAvailable = val
		case "Buffers:":
			buffers = val
		case "Cached:":
			cached = val
		}
	}
	if memAvailable > 0 && total > 0 {
		used = total - memAvailable
	} else {
		used = total - memFree - buffers - cached
	}
	if used < 0 {
		used = 0
	}
	return total, used
}
