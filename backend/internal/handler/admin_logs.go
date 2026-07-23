package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/response"
)

// ============================================================
// 服务日志监控（容器列表 + 历史日志 + 实时日志流 SSE）
// ============================================================
//
// 设计目标:
//   - 在仪表盘内嵌"服务日志监控"卡片, 半小时轮询一次拉取最近日志
//   - 报错日志(ERROR/FATAL/panic/exception/failed)在前端高亮, 便于快速定位
//   - 支持实时 SSE 流(可选开关, 默认轮询模式即可)
//
// 实现要点:
//   - 通过 docker CLI 读取容器 stdout/stderr 日志, 不依赖 docker.sock 直连
//     (panel 容器已挂载 docker.sock, GitPull 能跑 docker compose build 即证明)
//   - 容器名白名单: 只允许 nexus-* 前缀, 防止通过 :name 注入任意容器名
//     执行命令(docker logs <name> 中 name 会作为 argv 传入, 但仍做白名单防御)
//   - 限制 --tail 最大 2000 行, --since 最大 24h, 防止拉全量日志导致 OOM
//   - 同容器同窗口的请求做 3 秒内存缓存, 避免多用户并发拉日志打爆 docker
//   - SSE 流复用 auto_deploy.go 的心跳保活模式

// containerNameRe 容器名白名单: 仅允许字母/数字/下划线/连字符, 且必须 nexus- 前缀
// 防止注入 "name; rm -rf /" 之类的命令拼接(docker logs 走 exec argv 已无 shell 注入,
// 但白名单是纵深防御)
var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// isAllowedContainer 容器名白名单校验
func isAllowedContainer(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	if !containerNameRe.MatchString(name) {
		return false
	}
	// 允许的容器名前缀(覆盖 nexus-panel 项目的所有容器 + docker compose 标准名)
	allowedPrefixes := []string{"nexus-", "panel", "frontend", "postgres", "redis"}
	for _, p := range allowedPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ContainerInfo 容器信息(对应 docker ps --format json 的字段子集)
type ContainerInfo struct {
	ID      string `json:"id"`       // 容器 ID 短格式
	Name    string `json:"name"`     // 容器名
	Image   string `json:"image"`    // 镜像名
	Status  string `json:"status"`   // 状态文本(Up 2 hours / Exited...)
	State   string `json:"state"`    // 状态码(running / exited / restarting...)
	Ports   string `json:"ports"`    // 端口映射
	Created string `json:"created"`  // 创建时间(ISO)
}

// logEntryCache 日志请求缓存(同容器同窗口 3 秒内合并)
type logEntryCache struct {
	mu      sync.Mutex
	entries map[string]logCacheItem
}

type logCacheItem struct {
	ts       time.Time
	logs     string
}

var logCache = &logEntryCache{entries: make(map[string]logCacheItem)}

// getCache 读取缓存(3 秒内有效)
func (c *logEntryCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if time.Since(item.ts) > 3*time.Second {
		delete(c.entries, key)
		return "", false
	}
	return item.logs, true
}

// setCache 写入缓存
func (c *logEntryCache) set(key, logs string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 防止缓存无限增长: 超过 100 条清理一半
	if len(c.entries) > 100 {
		for k := range c.entries {
			delete(c.entries, k)
			if len(c.entries) <= 50 {
				break
			}
		}
	}
	c.entries[key] = logCacheItem{ts: time.Now(), logs: logs}
}

// ContainerList GET /api/v1/admin/system/containers
// 列出所有容器(nexus-* / postgres / redis 等项目相关容器)
// 用 docker ps -a --format json 输出, 过滤掉与项目无关的容器
func (h *AdminSystemHandler) ContainerList(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// docker ps -a --format json 每行输出一个 JSON 对象
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{json .}}")
	output, err := cmd.Output()
	if err != nil {
		// docker 不可用(容器内未挂载 docker.sock 时), 返回空列表 + 提示
		response.OK(c, gin.H{
			"containers": []ContainerInfo{},
			"available":  false,
			"msg":        "docker 命令不可用, 请确认 panel 容器已挂载 /var/run/docker.sock",
		})
		return
	}

	var containers []ContainerInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// docker ps --format json 输出字段: ID, Names, Image, Status, State, Ports, CreatedAt, RunningFor
		var raw struct {
			ID         string `json:"ID"`
			Names      string `json:"Names"`
			Image      string `json:"Image"`
			Status     string `json:"Status"`
			State      string `json:"State"`
			Ports      string `json:"Ports"`
			CreatedAt  string `json:"CreatedAt"`
			RunningFor string `json:"RunningFor"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		// Names 可能是逗号分隔的多个名(docker ps 通常只返回一个)
		name := strings.TrimSpace(strings.Split(raw.Names, ",")[0])
		if !isAllowedContainer(name) {
			continue
		}
		containers = append(containers, ContainerInfo{
			ID:      raw.ID[:12],
			Name:    name,
			Image:   raw.Image,
			Status:  raw.Status,
			State:   raw.State,
			Ports:   raw.Ports,
			Created: raw.CreatedAt,
		})
	}

	response.OK(c, gin.H{
		"containers": containers,
		"available":  true,
	})
}

// ContainerLogs GET /api/v1/admin/system/containers/:name/logs?tail=500&since=30m
// 拉取容器历史日志
//   - tail: 返回最后 N 行, 默认 500, 上限 2000
//   - since: 只返回指定时间内的日志, 默认 30m, 上限 24h
//
// 返回字段:
//   - logs: 日志文本(原始)
//   - error_count: 报错行数(ERROR/FATAL/panic/exception/failed 等)
//   - warn_count: 警告行数
//   - total_lines: 总行数
func (h *AdminSystemHandler) ContainerLogs(c *gin.Context) {
	name := c.Param("name")
	if !isAllowedContainer(name) {
		response.FailMsg(c, response.CodeParamError, "无效的容器名")
		return
	}

	// 解析 tail 参数(默认 500, 上限 2000)
	tail := 500
	if v := c.Query("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
			if tail > 2000 {
				tail = 2000
			}
		}
	}

	// 解析 since 参数(默认 30m, 上限 24h)
	since := "30m"
	if v := c.Query("since"); v != "" {
		// 校验格式: 数字+单位(m/h/s), 单位最大 24h
		if isValidSince(v) {
			since = v
		}
	}

	// 缓存 key(同容器+同 tail+同 since 3 秒内复用)
	cacheKey := fmt.Sprintf("%s|%d|%s", name, tail, since)
	if cached, ok := logCache.get(cacheKey); ok {
		// 直接返回缓存的解析结果
		stats := analyzeContainerLogs(cached)
		response.OK(c, gin.H{
			"logs":        cached,
			"error_count": stats.errorCount,
			"warn_count":  stats.warnCount,
			"total_lines": stats.totalLines,
			"cached":      true,
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// docker logs --tail N --since SINCE --timestamps <name>
	// --timestamps 在每行前加 RFC3339 时间戳, 便于前端按时间排序
	cmd := exec.CommandContext(ctx, "docker", "logs",
		"--tail", strconv.Itoa(tail),
		"--since", since,
		"--timestamps",
		name,
	)
	output, err := cmd.CombinedOutput()
	logs := string(output)
	if err != nil {
		// docker logs 失败时仍返回部分输出(可能 stderr 有提示), 但标记 failed
		stats := analyzeContainerLogs(logs)
		response.OK(c, gin.H{
			"logs":        logs + fmt.Sprintf("\n[拉取日志失败: %v]\n", err),
			"error_count": stats.errorCount + 1,
			"warn_count":  stats.warnCount,
			"total_lines": stats.totalLines,
			"failed":      true,
		})
		return
	}

	logCache.set(cacheKey, logs)
	stats := analyzeContainerLogs(logs)
	response.OK(c, gin.H{
		"logs":        logs,
		"error_count": stats.errorCount,
		"warn_count":  stats.warnCount,
		"total_lines": stats.totalLines,
		"cached":      false,
	})
}

// containerLogStats 容器日志统计结果
// (与 auto_deploy.go 的 logStats 区分, 那个用于节点部署诊断)
type containerLogStats struct {
	errorCount int
	warnCount  int
	totalLines int
}

// errorPattern 报错关键词正则(不区分大小写)
// 匹配 ERROR / ERR / FATAL / panic / exception / failed / failure / undefined / nil pointer 等
var errorPattern = regexp.MustCompile(`(?i)\b(error|err\b|fatal|panic|exception|failed|failure|nil pointer|undefined|out of memory|oom-killer|segmentation fault|segfault)\b`)

// warnPattern 警告关键词正则
var warnPattern = regexp.MustCompile(`(?i)\b(warn(ing)?|deprecat(ed|ion)|slow query|retry|backoff)\b`)

// analyzeContainerLogs 统计容器日志中的报错/警告行数
// (与 auto_deploy.go 的 analyzeLogs 区分, 那个用于节点部署诊断)
func analyzeContainerLogs(logs string) containerLogStats {
	if logs == "" {
		return containerLogStats{}
	}
	lines := strings.Split(logs, "\n")
	stats := containerLogStats{totalLines: len(lines)}
	for _, line := range lines {
		if errorPattern.MatchString(line) {
			stats.errorCount++
		} else if warnPattern.MatchString(line) {
			stats.warnCount++
		}
	}
	return stats
}

// ============================================================
// 智能运维: 全局错误聚合(自动扫描所有容器, 提取 ERROR/WARN 行)
// ============================================================
//
// 设计目标:
//   - 管理员仪表盘"日志滚动"区域自动获取所有容器的错误日志, 无需手动逐个切换容器
//   - 一次请求扫描 nexus-panel/postgres/redis/frontend 等所有项目容器
//   - 提取匹配 errorPattern/warnPattern 的行, 按时间倒序聚合返回
//   - 前端 Dashboard 可直接渲染"全栈错误聚合"视图
//
// 与 ContainerLogs 的区别:
//   - ContainerLogs: 单容器全量日志(含正常行), 用于深入排查某个容器
//   - ErrorsAggregate: 全容器仅错误/警告行, 用于全局巡检快速发现故障

// ErrorEntry 聚合后的单条错误/警告日志
type ErrorEntry struct {
	Container string `json:"container"`          // 容器名
	Level     string `json:"level"`              // error / warn
	Timestamp string `json:"timestamp,omitempty"` // RFC3339 时间戳(从 docker logs --timestamps 解析)
	Line      string `json:"line"`               // 日志原文(已截断到 1KB 防超长行)
}

// ErrorsAggregateResult 错误聚合结果
type ErrorsAggregateResult struct {
	Available         bool         `json:"available"`          // docker 是否可用
	Since             string       `json:"since"`              // 时间窗口
	ContainersScanned int          `json:"containers_scanned"` // 实际扫描的容器数
	TotalErrors       int          `json:"total_errors"`       // 错误行总数
	TotalWarns        int          `json:"total_warns"`        // 警告行总数
	Errors            []ErrorEntry `json:"errors"`             // 聚合的错误/警告列表(按时间倒序)
	Msg               string       `json:"msg,omitempty"`      // 提示信息(docker 不可用时)
}

// errorsAggregateCache 错误聚合结果缓存(避免频繁扫描所有容器打爆 docker)
// 同 since+limit 30 秒内复用
var errorsAggregateCache = struct {
	mu  sync.Mutex
	ts  time.Time
	res *ErrorsAggregateResult
}{}

// ErrorsAggregate GET /api/v1/admin/system/errors?since=1h&limit=100
// 自动扫描所有项目容器最近日志, 聚合返回所有 ERROR/WARN 行
//   - since: 时间窗口, 默认 1h, 上限 24h
//   - limit: 返回条数上限, 默认 100, 上限 500
//
// 实现要点:
//   - 串行扫描各容器(避免并发 docker logs 子进程打爆 CPU/内存)
//   - 每个容器最多拉 500 行(--tail 500), 提取匹配 errorPattern/warnPattern 的行
//   - 结果按时间倒序(最新在前), 便于运营者先看最近的错误
//   - 30 秒内存缓存, 避免多用户并发触发全容器扫描
func (h *AdminSystemHandler) ErrorsAggregate(c *gin.Context) {
	// 解析 since(默认 1h, 上限 24h)
	since := "1h"
	if v := c.Query("since"); v != "" && isValidSince(v) {
		since = v
	}
	// 解析 limit(默认 100, 上限 500)
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > 500 {
				limit = 500
			}
		}
	}

	// 30 秒缓存: 同 since 复用, 避免频繁全容器扫描
	errorsAggregateCache.mu.Lock()
	if errorsAggregateCache.res != nil &&
		time.Since(errorsAggregateCache.ts) < 30*time.Second &&
		errorsAggregateCache.res.Since == since {
		cached := *errorsAggregateCache.res
		// 按 limit 截断
		if len(cached.Errors) > limit {
			cached.Errors = cached.Errors[:limit]
		}
		errorsAggregateCache.mu.Unlock()
		cached.Available = true
		response.OK(c, cached)
		return
	}
	errorsAggregateCache.mu.Unlock()

	// 1. 列出所有项目容器
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	listCmd := exec.CommandContext(ctx, "docker", "ps", "--format", "{{json .}}")
	listOut, err := listCmd.Output()
	if err != nil {
		// docker 不可用
		response.OK(c, &ErrorsAggregateResult{
			Available: false,
			Since:     since,
			Errors:    []ErrorEntry{},
			Msg:       "docker 命令不可用, 请确认 panel 容器已挂载 /var/run/docker.sock",
		})
		return
	}

	// 解析容器列表, 仅保留 running 状态的项目容器
	var containerNames []string
	scanner := bufio.NewScanner(strings.NewReader(string(listOut)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw struct {
			Names string `json:"Names"`
			State string `json:"State"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		name := strings.TrimSpace(strings.Split(raw.Names, ",")[0])
		if !isAllowedContainer(name) || raw.State != "running" {
			continue
		}
		containerNames = append(containerNames, name)
	}

	// 2. 串行扫描每个容器, 提取错误/警告行
	result := &ErrorsAggregateResult{
		Available:         true,
		Since:             since,
		ContainersScanned: len(containerNames),
		Errors:            []ErrorEntry{},
	}

	for _, name := range containerNames {
		// 每个容器最多拉 500 行, 防止超大日志打爆内存
		logCmd := exec.CommandContext(ctx, "docker", "logs",
			"--tail", "500",
			"--since", since,
			"--timestamps",
			name,
		)
		out, _ := logCmd.CombinedOutput()
		// docker logs 失败(容器已停止/无权限)时静默跳过, 不阻断其它容器扫描
		if len(out) == 0 {
			continue
		}
		// 逐行解析, 提取错误/警告行
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			level := ""
			if errorPattern.MatchString(line) {
				level = "error"
			} else if warnPattern.MatchString(line) {
				level = "warn"
			} else {
				continue
			}
			// 截断超长行(防单行日志几 MB 撑爆响应)
			if len(line) > 1024 {
				line = line[:1024] + "...[truncated]"
			}
			// 解析 docker logs --timestamps 前缀的 RFC3339 时间戳
			ts := parseDockerLogTimestamp(line)
			entry := ErrorEntry{
				Container: name,
				Level:     level,
				Timestamp: ts,
				Line:      line,
			}
			result.Errors = append(result.Errors, entry)
			if level == "error" {
				result.TotalErrors++
			} else {
				result.TotalWarns++
			}
		}
	}

	// 3. 按时间倒序(最新在前), docker logs --timestamps 输出已是时间正序,
	//    反转后即倒序。若有时间戳则按时间戳排序兜底。
	sortErrorEntriesByTimeDesc(result.Errors)

	// 4. 截断到 limit
	if len(result.Errors) > limit {
		result.Errors = result.Errors[:limit]
	}

	// 5. 写缓存
	errorsAggregateCache.mu.Lock()
	errorsAggregateCache.res = result
	errorsAggregateCache.ts = time.Now()
	errorsAggregateCache.mu.Unlock()

	response.OK(c, result)
}

// parseDockerLogTimestamp 从 docker logs --timestamps 输出行中解析 RFC3339 时间戳前缀
// docker logs --timestamps 格式: "2026-07-23T10:30:00.123456789Z <log content>"
// 解析失败返回空字符串(不阻断聚合)
func parseDockerLogTimestamp(line string) string {
	// 找第一个空格, 前面是时间戳
	idx := strings.IndexByte(line, ' ')
	if idx <= 0 || idx > 40 {
		return ""
	}
	tsStr := line[:idx]
	// 尝试解析为 RFC3339(含纳秒)
	if _, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
		return tsStr
	}
	if _, err := time.Parse(time.RFC3339, tsStr); err == nil {
		return tsStr
	}
	return ""
}

// sortErrorEntriesByTimeDesc 按时间戳倒序排列(最新在前)
// 无时间戳的条目排到最后, 保持原顺序
func sortErrorEntriesByTimeDesc(entries []ErrorEntry) {
	// 稳定排序: 有时间戳的按时间倒序, 无时间戳的保持原顺序在后
	// 用冒泡式稳定排序即可(数据量通常 < 500)
	n := len(entries)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			a, b := entries[j], entries[j+1]
			// 无时间戳的排到最后
			if a.Timestamp == "" && b.Timestamp != "" {
				entries[j], entries[j+1] = b, a
				continue
			}
			if a.Timestamp != "" && b.Timestamp != "" {
				ta, errA := time.Parse(time.RFC3339Nano, a.Timestamp)
				tb, errB := time.Parse(time.RFC3339Nano, b.Timestamp)
				if errA == nil && errB == nil && ta.Before(tb) {
					entries[j], entries[j+1] = b, a
				}
			}
		}
	}
}

// isValidSince 校验 docker logs --since 参数格式
// 允许: 30m / 2h / 1h30m / 3600s 等 Go duration-like 格式, 或 RFC3339 时间
// 上限: 24h(防止拉全量日志)
func isValidSince(s string) bool {
	if len(s) > 32 {
		return false
	}
	// RFC3339 时间格式(2026-07-19T10:00:00)
	if strings.Contains(s, "T") && strings.Contains(s, ":") {
		_, err := time.Parse(time.RFC3339, s)
		return err == nil
	}
	// duration 格式: 30m / 2h / 1h30m / 3600s
	// 简单校验: 仅含数字和 mhsm 单位字符
	for _, ch := range s {
		if !(ch >= '0' && ch <= '9') && ch != 'm' && ch != 'h' && ch != 's' {
			return false
		}
	}
	// 换算为秒, 上限 24h
	d, err := time.ParseDuration(s)
	if err != nil {
		return false
	}
	return d > 0 && d <= 24*time.Hour
}

// ContainerLogStream GET /api/v1/admin/system/containers/:name/logs/stream
// 实时日志流(SSE)
//   - 客户端通过 EventSource 连接, 后端 docker logs -f 持续推送
//   - 心跳保活: 每 10 秒发一个 ": heartbeat" 注释行
//   - 客户端断开时自动 kill docker logs 子进程, 避免僵尸进程
//
// 用法: const es = new EventSource('/api/v1/admin/system/containers/nexus-panel/logs/stream')
//
//	es.onmessage = (e) => { appendLog(e.data) }
//	es.onerror = () => es.close()
func (h *AdminSystemHandler) ContainerLogStream(c *gin.Context) {
	name := c.Param("name")
	if !isAllowedContainer(name) {
		response.FailMsg(c, response.CodeParamError, "无效的容器名")
		return
	}

	// SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, _ := c.Writer.(http.Flusher)
	if flusher == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "不支持流式响应"})
		return
	}
	flusher.Flush()

	// 启动 docker logs -f 子进程
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "logs",
		"--tail", "100",   // 先拉最近 100 行历史
		"--follow",        // 然后持续跟随
		"--timestamps",
		name,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	cmd.Stderr = cmd.Stdout // stderr 也合并到 stdout
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}
	// 客户端断开或出错时 kill 子进程
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// 心跳保活 goroutine
	heartbeatDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fmt.Fprintf(c.Writer, ": heartbeat\n\n")
				flusher.Flush()
			case <-heartbeatDone:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	defer close(heartbeatDone)

	// 逐行读取 docker logs 输出, 转为 SSE event
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := scanner.Text()
		// SSE 格式: data: <line>\n\n
		// 多行用 "data: " 前缀逐行发送
		fmt.Fprintf(c.Writer, "data: %s\n\n", line)
		flusher.Flush()
	}
}
