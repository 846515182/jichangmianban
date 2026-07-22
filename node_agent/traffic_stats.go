package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// UserTraffic 用户级流量统计，通过 Xray StatsService API 获取每个用户的流量增量。
//
// 工作原理:
//  1. 调用 xray api statsquery 获取所有用户计数器快照
//  2. 与上一次快照对比计算增量
//  3. 返回 [(userID, upload, download), ...] 供 ReportRealtime 上报
//
// Xray Stats 命名格式:
//
//	user>>>{uuid}>>>traffic>>>uplink
//	user>>>{uuid}>>>traffic>>>downlink
//
// 其中 {uuid} 就是面板下发的 user.ID（Xray clients[].id）。
// 面板后端 ReportRealtime 直接按 UUID 匹配用户，无需 agent 端做映射。
type UserTraffic struct {
	mu          sync.Mutex
	prevUp      map[string]int64 // user_id → 上次上行字节(counter 绝对值)
	prevDown    map[string]int64 // user_id → 上次下行字节(counter 绝对值)
	xrayBinPath string
	apiPort     int

	// statsQueryFn 用于查询 Xray 统计数据的函数，在测试中可注入 mock。
	statsQueryFn func() (up map[string]int64, down map[string]int64, err error)
}

// TrafficDelta 用户流量增量（相对于上次查询）
type TrafficDelta struct {
	UserID   string
	Upload   int64
	Download int64
}

// NewUserTraffic 创建用户流量统计器。
// xrayBinPath: Xray 二进制路径（如 /app/xray/xray）
// apiPort: Xray API 监听端口（对应配置中的 api inbound 端口）
func NewUserTraffic(xrayBinPath string, apiPort int) *UserTraffic {
	ut := &UserTraffic{
		prevUp:      make(map[string]int64),
		prevDown:    make(map[string]int64),
		xrayBinPath: xrayBinPath,
		apiPort:     apiPort,
	}
	ut.statsQueryFn = ut.queryXrayStats
	return ut
}

// QueryDelta 查询 Xray Stats 并返回自上次查询以来的每个用户的流量增量。
// 首次调用时 prev 为空，所有用户当前 counter 值作为"基线"记录但不上报（增量为 0）。
// 后续调用只上报增量 > 0 的记录。
func (ut *UserTraffic) QueryDelta() ([]TrafficDelta, error) {
	currentUp, currentDown, err := ut.statsQueryFn()
	if err != nil {
		return nil, err
	}

	ut.mu.Lock()
	deltas := calculateDeltas(ut.prevUp, ut.prevDown, currentUp, currentDown)
	ut.mu.Unlock()

	return deltas, nil
}

// calculateDeltas 根据当前快照和上次基线计算流量增量，并更新基线。
// 纯函数，便于单元测试。
//
// 参数:
//   - prevUp/prevDown: 上次的计数器基线（调用后会原地修改）
//   - currentUp/currentDown: 当前查询到的计数器值
//
// 特性:
//   - 首次调用(prev 均为 0)时只建立基线，不返回增量
//   - Xray 重启 counter 归零(delta 为负)时用当前值作为增量
//   - 已移除的用户从基线中清理
func calculateDeltas(
	prevUp, prevDown map[string]int64,
	currentUp, currentDown map[string]int64,
) []TrafficDelta {
	// 收集所有出现过的用户（当前 + 历史）
	allUsers := make(map[string]bool)
	for uid := range currentUp {
		allUsers[uid] = true
	}
	for uid := range currentDown {
		allUsers[uid] = true
	}
	for uid := range prevUp {
		allUsers[uid] = true
	}
	for uid := range prevDown {
		allUsers[uid] = true
	}

	var deltas []TrafficDelta
	for uid := range allUsers {
		curUp := currentUp[uid]
		curDown := currentDown[uid]
		prevUpVal := prevUp[uid]
		prevDownVal := prevDown[uid]

		upload := curUp - prevUpVal
		download := curDown - prevDownVal

		// 计数器重置检测：Xray 重启后 counter 归零，delta 为负数。
		// 此时用当前值当作本次增量（即丢弃重启前的流量，避免上报负值）。
		if upload < 0 {
			upload = curUp
		}
		if download < 0 {
			download = curDown
		}

		if upload > 0 || download > 0 {
			deltas = append(deltas, TrafficDelta{
				UserID:   uid,
				Upload:   upload,
				Download: download,
			})
		}

		// 更新基线
		prevUp[uid] = curUp
		prevDown[uid] = curDown
	}

	// 清理已不存在的用户（Xray 中已移除的旧用户基线）
	for uid := range prevUp {
		if _, ok := currentUp[uid]; !ok {
			delete(prevUp, uid)
		}
	}
	for uid := range prevDown {
		if _, ok := currentDown[uid]; !ok {
			delete(prevDown, uid)
		}
	}

	return deltas
}

// queryXrayStats 调用 xray api statsquery 并解析结果。
// 返回两个 map: userID → upload_bytes 和 userID → download_bytes。
func (ut *UserTraffic) queryXrayStats() (map[string]int64, map[string]int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	addr := fmt.Sprintf("127.0.0.1:%d", ut.apiPort)
	cmd := exec.CommandContext(ctx, ut.xrayBinPath, "api", "statsquery",
		"-server", addr)
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("xray api statsquery (server=%s) 失败: %w", addr, err)
	}

	var result struct {
		Stat []struct {
			Name  string      `json:"name"`
			Value interface{} `json:"value"` // Xray v24+ 返回数字，v23 返回字符串
		} `json:"stat"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, nil, fmt.Errorf("解析 stats 输出失败: %w", err)
	}

	up := make(map[string]int64)
	down := make(map[string]int64)

	for _, s := range result.Stat {
		userID, isUp := parseUserTrafficStat(s.Name)
		if userID == "" {
			continue
		}
		var val int64
		switch v := s.Value.(type) {
		case float64:
			// Xray v24+ 返回 JSON number (float64)
			val = int64(v)
		case string:
			// Xray v23 返回字符串
			if _, err := fmt.Sscanf(v, "%d", &val); err != nil {
				log.Printf("[traffic] 解析计数器失败 name=%s value=%s: %v", s.Name, v, err)
				continue
			}
		default:
			log.Printf("[traffic] 未知 value 类型 name=%s type=%T: %v", s.Name, s.Value, s.Value)
			continue
		}
		if isUp {
			up[userID] = val
		} else {
			down[userID] = val
		}
	}
	log.Printf("[traffic] Xray Stats 查询完成: %d 用户 (upload=%d, download=%d 记录)",
		len(up), len(up), len(down))

	return up, down, nil
}

// parseUserTrafficStat 解析 Xray Stat 名称，提取 userID 和方向。
//
// 格式: user>>>{uuid}>>>traffic>>>uplink / downlink
// 返回 (userID, isUpload)。
func parseUserTrafficStat(name string) (string, bool) {
	if !strings.HasPrefix(name, "user>>>") {
		return "", false
	}
	rest := name[7:] // 去掉 "user>>>"（7个字符: u,s,e,r,>,>,>）

	idx := strings.Index(rest, ">>>")
	if idx < 0 {
		return "", false
	}
	uuid := rest[:idx]
	if !isValidUUID(uuid) {
		return "", false
	}

	remaining := rest[idx+3:]
	isUp := strings.HasSuffix(remaining, "traffic>>>uplink")
	isDown := strings.HasSuffix(remaining, "traffic>>>downlink")
	if !isUp && !isDown {
		return "", false
	}

	return uuid, isUp
}

// isValidUUID 简单校验 UUID 格式（8-4-4-4-12 共 36 字符，含 4 个连字符）
func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for _, pos := range []int{8, 13, 18, 23} {
		if s[pos] != '-' {
			return false
		}
	}
	return true
}
