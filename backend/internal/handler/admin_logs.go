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

// containerNameRe 容器名格式校验: 仅允许字母/数字/下划线/连字符
// 防止注入 "name; rm -rf /" 之类的命令拼接(docker logs 走 exec argv 已无 shell 注入,
// 但格式校验是纵深防御)
var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// isAllowedContainer 容器名校验
// 智能运维增强: 不再依赖固定前缀白名单, 而是基于 docker compose project label 自动识别
// 同项目容器。同时保留基础格式校验防止命令注入。
func isAllowedContainer(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	return containerNameRe.MatchString(name)
}

// containerProjectCache 缓存当前面板容器所属的 compose project(60 秒)
var containerProjectCache = struct {
	mu      sync.RWMutex
	project string
	expires time.Time
}{}

// getOwnComposeProject 获取当前面板容器所属的 docker compose project
// 实现: 通过 /proc/self/cgroup 读取当前容器 ID, 然后 docker inspect 读取 Labels
// 失败则返回空字符串, 调用方 fallback 到前缀白名单
func getOwnComposeProject() string {
	containerProjectCache.mu.RLock()
	if time.Now().Before(containerProjectCache.expires) && containerProjectCache.project != "" {
		p := containerProjectCache.project
		containerProjectCache.mu.RUnlock()
		return p
	}
	containerProjectCache.mu.RUnlock()

	id := readSelfContainerID()
	if id == "" {
		return ""
	}
	project := inspectContainerProject(id)

	containerProjectCache.mu.Lock()
	containerProjectCache.project = project
	containerProjectCache.expires = time.Now().Add(60 * time.Second)
	containerProjectCache.mu.Unlock()
	return project
}

// readSelfContainerID 读取当前容器自己的容器 ID(从 /proc/self/cgroup)
func readSelfContainerID() string {
	data, err := exec.Command("cat", "/proc/self/cgroup").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue
		}
		path := parts[2]
		if idx := strings.LastIndex(path, "/docker/"); idx >= 0 {
			id := path[idx+8:]
			if len(id) >= 12 {
				return id
			}
		}
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			id := path[idx+1:]
			if len(id) >= 12 && !strings.Contains(id, "-") {
				return id
			}
		}
	}
	return ""
}

// inspectContainerProject 通过 docker inspect 读取容器的 compose project label
func inspectContainerProject(id string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .Config.Labels}}", id)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var labels map[string]string
	if err := json.Unmarshal(out, &labels); err != nil {
		return ""
	}
	return labels["com.docker.compose.project"]
}

// projectContainerCache 缓存 project -> containers 映射(30 秒)
var projectContainerCache = struct {
	mu         sync.RWMutex
	expires    time.Time
	containers []string
}{}

// findProjectContainers 自动发现同 docker compose project 的所有运行中容器
// 如果当前面板容器没有 compose project label, 则 fallback 到历史前缀白名单
func findProjectContainers() []string {
	projectContainerCache.mu.RLock()
	if time.Now().Before(projectContainerCache.expires) && len(projectContainerCache.containers) > 0 {
		c := make([]string, len(projectContainerCache.containers))
		copy(c, projectContainerCache.containers)
		projectContainerCache.mu.RUnlock()
		return c
	}
	projectContainerCache.mu.RUnlock()

	ownProject := getOwnComposeProject()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "--format", "{{json .}}")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var names []string
	var inspectIDs []string
	type containerRow struct {
		Names string `json:"Names"`
		ID    string `json:"ID"`
		State string `json:"State"`
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row containerRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue
		}
		name := strings.TrimSpace(strings.Split(row.Names, ",")[0])
		if !isAllowedContainer(name) || row.State != "running" {
			continue
		}
		names = append(names, name)
		inspectIDs = append(inspectIDs, row.ID)
	}

	if ownProject == "" {
		projectContainerCache.mu.Lock()
		projectContainerCache.containers = names
		projectContainerCache.expires = time.Now().Add(30 * time.Second)
		projectContainerCache.mu.Unlock()
		return names
	}

	var filtered []string
	if len(inspectIDs) > 0 {
		inspectCmd := exec.CommandContext(ctx, "docker", append([]string{"inspect", "--format", "{{json .Config.Labels}}"}, inspectIDs...)...)
		inspectOut, err := inspectCmd.Output()
		if err == nil {
			lines := strings.Split(string(inspectOut), "\n")
			for i, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" || i >= len(names) {
					continue
				}
				var labels map[string]string
				if err := json.Unmarshal([]byte(line), &labels); err != nil {
					continue
				}
				if labels["com.docker.compose.project"] == ownProject {
					filtered = append(filtered, names[i])
				}
			}
		}
	}

	if len(filtered) == 0 {
		filtered = filterFallbackPrefixes(names)
	}

	projectContainerCache.mu.Lock()
	projectContainerCache.containers = filtered
	projectContainerCache.expires = time.Now().Add(30 * time.Second)
	projectContainerCache.mu.Unlock()
	return filtered
}

// filterFallbackPrefixes 历史兜底: 当 compose project 不可用时, 用前缀匹配
func filterFallbackPrefixes(names []string) []string {
	allowedPrefixes := []string{"nexus-", "panel", "frontend", "postgres", "redis"}
	var out []string
	for _, name := range names {
		for _, p := range allowedPrefixes {
			if strings.HasPrefix(name, p) {
				out = append(out, name)
				break
			}
		}
	}
	return out
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
// 智能运维: 全局错误聚合(自动发现同项目容器 + 系统日志 + 按指纹去重)
// ============================================================
//
// 设计目标:
//   - 管理员仪表盘"日志滚动"区域自动获取所有错误, 无需手动逐个切换容器
//   - 自动识别 docker compose project 内所有容器(新增容器自动纳入, 无需改代码)
//   - 不只容器: 同时尝试收集宿主机系统日志(journalctl/dmesg/syslog)
//   - 只展示错误/警告, 正常日志不展示
//   - 相同错误按"指纹"聚合, 只展示一次, 避免同一错误疯狂滚动
//   - 前端 Dashboard 可直接渲染"全局错误聚合"视图
//
// 与 ContainerLogs 的区别:
//   - ContainerLogs: 单容器全量日志(含正常行), 用于深入排查某个容器
//   - ErrorsAggregate: 全栈错误去重聚合, 用于全局巡检快速发现故障

// AggregatedError 去重聚合后的单条错误
type AggregatedError struct {
	Fingerprint string   `json:"fingerprint"` // 归一化指纹(去掉时间戳/数字/UUID/IP 后的模板)
	Level       string   `json:"level"`       // error / warn
	Sample      string   `json:"sample"`      // 最近一次原始日志(截断)
	Count       int      `json:"count"`       // 出现次数
	LastAt      string   `json:"last_at"`     // 最近发生时间(RFC3339)
	Containers  []string `json:"containers"`  // 来源容器/主机名列表(去重)
	Sources     []string `json:"sources"`     // 日志源类型(docker/journal/dmesg/syslog)
}

// ErrorsAggregateResult 错误聚合结果
type ErrorsAggregateResult struct {
	Available         bool              `json:"available"`          // docker 是否可用
	Since             string            `json:"since"`              // 时间窗口
	ContainersScanned int               `json:"containers_scanned"` // 实际扫描的容器数
	TotalErrors       int               `json:"total_errors"`       // 原始错误行总数
	TotalWarns        int               `json:"total_warns"`        // 原始警告行总数
	UniqueErrors      int               `json:"unique_errors"`      // 去重后错误数
	UniqueWarns       int               `json:"unique_warns"`       // 去重后警告数
	Errors            []AggregatedError `json:"errors"`             // 聚合后的错误列表(按最近时间倒序)
	Msg               string            `json:"msg,omitempty"`      // 提示信息(docker 不可用时)
}

// errorsAggregateCache 错误聚合结果缓存(避免频繁扫描所有容器打爆 docker)
// 同 since 30 秒内复用
var errorsAggregateCache = struct {
	mu  sync.Mutex
	ts  time.Time
	res *ErrorsAggregateResult
}{}

// logFingerprintRe 用于指纹归一化的正则(数字/UUID/IP/hex)
var (
	uuidRe      = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	ipv4Re      = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`)
	hex64Re     = regexp.MustCompile(`\b[0-9a-fA-F]{32,64}\b`)
	numberRe    = regexp.MustCompile(`\b\d+\b`)
	wsRe        = regexp.MustCompile(`\s+`)
)

// logLineFingerprint 计算日志行的归一化指纹
// 去掉时间戳前缀 + 替换 UUID/IP/数字 + 小写 + 截断, 用于去重
func logLineFingerprint(line string) string {
	// 1. 去掉 docker logs --timestamps 前缀 (RFC3339 时间戳)
	if idx := strings.IndexByte(line, ' '); idx > 0 && idx <= 40 {
		ts := line[:idx]
		if _, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			line = line[idx+1:]
		} else if _, err := time.Parse(time.RFC3339, ts); err == nil {
			line = line[idx+1:]
		}
	}
	// 2. 替换动态 token
	line = uuidRe.ReplaceAllString(line, "<UUID>")
	line = ipv4Re.ReplaceAllString(line, "<IP>")
	line = hex64Re.ReplaceAllString(line, "<HEX>")
	line = numberRe.ReplaceAllString(line, "<N>")
	// 3. 压缩空白, 小写, 截断
	line = wsRe.ReplaceAllString(strings.TrimSpace(line), " ")
	line = strings.ToLower(line)
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

// extractLevel 判断一行日志的级别(error/warn/空)
func extractLevel(line string) string {
	if errorPattern.MatchString(line) {
		return "error"
	}
	if warnPattern.MatchString(line) {
		return "warn"
	}
	return ""
}

// truncateLine 截断超长行到 1KB
func truncateLine(line string) string {
	if len(line) > 1024 {
		return line[:1024] + "...[truncated]"
	}
	return line
}

// parseLogTimestamp 通用时间戳解析: 支持 docker RFC3339 前缀 + syslog 风格
func parseLogTimestamp(line string) string {
	// docker logs --timestamps 格式: "2026-07-23T10:30:00.123456789Z <log>"
	if idx := strings.IndexByte(line, ' '); idx > 0 && idx <= 40 {
		ts := line[:idx]
		if _, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return ts
		}
		if _, err := time.Parse(time.RFC3339, ts); err == nil {
			return ts
		}
	}
	return ""
}

// ErrorsAggregate GET /api/v1/admin/system/errors?since=1h&limit=100
//
// 自动发现同 docker compose project 的所有容器 + 宿主机系统日志,
// 提取 ERROR/WARN 行, 按指纹去重聚合返回。
//
// 特性:
//   - since: 时间窗口, 默认 1h, 上限 24h
//   - limit: 返回条数上限, 默认 100, 上限 500
//   - 只返回错误/警告, 正常日志不展示
//   - 相同错误按指纹聚合, 只展示一次, 含 count/last_at/多容器来源
//   - 新增同 project 容器自动纳入(无需改代码)
//   - 30 秒内存缓存
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

	// 30 秒缓存: 同 since 复用
	errorsAggregateCache.mu.Lock()
	if errorsAggregateCache.res != nil &&
		time.Since(errorsAggregateCache.ts) < 30*time.Second &&
		errorsAggregateCache.res.Since == since {
		cached := *errorsAggregateCache.res
		if len(cached.Errors) > limit {
			cached.Errors = cached.Errors[:limit]
		}
		errorsAggregateCache.mu.Unlock()
		cached.Available = true
		response.OK(c, cached)
		return
	}
	errorsAggregateCache.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// 1. 自动发现同 project 的运行中容器
	containerNames := findProjectContainers()
	if containerNames == nil {
		// docker 不可用
		response.OK(c, &ErrorsAggregateResult{
			Available: false,
			Since:     since,
			Errors:    []AggregatedError{},
			Msg:       "docker 命令不可用, 请确认 panel 容器已挂载 /var/run/docker.sock",
		})
		return
	}

	// 2. 聚合器: fingerprint -> AggregatedError
	agg := make(map[string]*AggregatedError)
	addLine := func(source, container, line, level, ts string) {
		if level == "" {
			return
		}
		fp := logLineFingerprint(line)
		if fp == "" {
			return
		}
		item, ok := agg[fp]
		if !ok {
			item = &AggregatedError{
				Fingerprint: fp,
				Level:       level,
				Sample:      truncateLine(line),
				Count:       0,
				Containers:  []string{},
				Sources:     []string{},
			}
			agg[fp] = item
		}
		item.Count++
		// 保留最近时间
		if ts != "" {
			if item.LastAt == "" || ts > item.LastAt {
				item.LastAt = ts
				item.Sample = truncateLine(line)
			}
		}
		// 容器去重
		if container != "" && !sliceContains(item.Containers, container) {
			item.Containers = append(item.Containers, container)
		}
		// 日志源去重
		if source != "" && !sliceContains(item.Sources, source) {
			item.Sources = append(item.Sources, source)
		}
	}

	result := &ErrorsAggregateResult{
		Available:         true,
		Since:             since,
		ContainersScanned: len(containerNames),
		Errors:            []AggregatedError{},
	}

	// 3. 串行扫描每个容器, 只提取错误/警告行
	for _, name := range containerNames {
		logCmd := exec.CommandContext(ctx, "docker", "logs",
			"--tail", "500",
			"--since", since,
			"--timestamps",
			name,
		)
		out, _ := logCmd.CombinedOutput()
		if len(out) == 0 {
			continue
		}
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			level := extractLevel(line)
			if level == "" {
				continue
			}
			ts := parseLogTimestamp(line)
			addLine("docker", name, line, level, ts)
			if level == "error" {
				result.TotalErrors++
			} else {
				result.TotalWarns++
			}
		}
	}

	// 4. 收集系统级日志(宿主机 journalctl / dmesg / syslog, 面板容器内可能不可用, 优雅降级)
	collectSystemErrors(ctx, since, addLine, result)

	// 5. map -> slice, 按最近时间倒序
	for _, item := range agg {
		if item.Level == "error" {
			result.UniqueErrors++
		} else {
			result.UniqueWarns++
		}
		result.Errors = append(result.Errors, *item)
	}
	sortAggregatedErrorsByTimeDesc(result.Errors)

	// 6. 截断到 limit
	if len(result.Errors) > limit {
		result.Errors = result.Errors[:limit]
	}

	// 7. 写缓存
	errorsAggregateCache.mu.Lock()
	errorsAggregateCache.res = result
	errorsAggregateCache.ts = time.Now()
	errorsAggregateCache.mu.Unlock()

	response.OK(c, result)
}

// collectSystemErrors 收集宿主机系统级错误日志
// 面板容器通常无 systemd, 但可能挂载了 /var/log 或 /run/log, 这里做最佳努力尝试
func collectSystemErrors(ctx context.Context, since string, addLine func(source, container, line, level, ts string), result *ErrorsAggregateResult) {
	// 4.1 journalctl --priority=err (需要容器内有 systemd 或挂载 journal)
	if out, err := exec.CommandContext(ctx, "journalctl", "--priority=3", "--since", since, "--no-pager", "-n", "200").CombinedOutput(); err == nil && len(out) > 0 {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			level := extractLevel(line)
			if level == "" {
				// journalctl priority=3 默认就是错误, 没关键字也视为 error
				level = "error"
			}
			addLine("journal", "host", line, level, "")
			if level == "error" {
				result.TotalErrors++
			} else {
				result.TotalWarns++
			}
		}
	}

	// 4.2 dmesg --level=err,warn (需要特权 CAP_SYSLOG)
	if out, err := exec.CommandContext(ctx, "dmesg", "--level=err,warn", "-n", "100").CombinedOutput(); err == nil && len(out) > 0 {
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			level := extractLevel(line)
			if level == "" {
				level = "error"
			}
			addLine("dmesg", "host", line, level, "")
			if level == "error" {
				result.TotalErrors++
			} else {
				result.TotalWarns++
			}
		}
	}

	// 4.3 直接读取常见 syslog 文件(如果宿主机 /var/log 被挂载进容器)
	syslogFiles := []string{"/var/log/syslog", "/var/log/messages"}
	for _, path := range syslogFiles {
		if out, err := exec.CommandContext(ctx, "tail", "-n", "200", path).CombinedOutput(); err == nil && len(out) > 0 {
			lines := strings.Split(string(out), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				level := extractLevel(line)
				if level == "" {
					continue
				}
				addLine("syslog", "host", line, level, "")
				if level == "error" {
					result.TotalErrors++
				} else {
					result.TotalWarns++
				}
			}
		}
	}
}

// sliceContains 判断字符串切片是否包含某元素
func sliceContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// sortAggregatedErrorsByTimeDesc 按最近时间倒序排列聚合错误(最新在前)
// 无时间戳的排到最后
func sortAggregatedErrorsByTimeDesc(entries []AggregatedError) {
	n := len(entries)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			a, b := entries[j], entries[j+1]
			if a.LastAt == "" && b.LastAt != "" {
				entries[j], entries[j+1] = b, a
				continue
			}
			if a.LastAt != "" && b.LastAt != "" && a.LastAt < b.LastAt {
				entries[j], entries[j+1] = b, a
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
