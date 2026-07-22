package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultXrayDir    = "/app/xray"
	defaultConfigPath = "/app/config.json"
	// defaultXrayAPIPort Xray API 监听端口（用于 statsquery 查询用户级流量）。
	// 该端口独立于代理端口，仅监听 127.0.0.1，不对外暴露。
	defaultXrayAPIPort = 10085
)

// XrayManager 管理 Xray-core 二进制下载与进程生命周期
type XrayManager struct {
	version    string
	binaryPath string
	configPath string

	mu     sync.Mutex
	cmd    *exec.Cmd
	doneCh chan struct{} // 进程退出时关闭
}

// NewXrayManager 创建 Xray 管理器，确保目录存在
func NewXrayManager(version string) (*XrayManager, error) {
	xrayDir := getenvDefault("XRAY_DIR", defaultXrayDir)
	configPath := getenvDefault("XRAY_CONFIG_PATH", defaultConfigPath)
	if err := os.MkdirAll(xrayDir, 0755); err != nil {
		return nil, fmt.Errorf("创建 Xray 目录失败: %w", err)
	}
	return &XrayManager{
		version:    version,
		binaryPath: filepath.Join(xrayDir, "xray"),
		configPath: configPath,
	}, nil
}

// EnsureBinary 确保二进制存在(否则下载)
func (m *XrayManager) EnsureBinary() error {
	if _, err := os.Stat(m.binaryPath); err == nil {
		log.Printf("Xray 二进制已存在: %s", m.binaryPath)
		return os.Chmod(m.binaryPath, 0755)
	}
	return m.downloadXray()
}

// downloadXray 从 GitHub releases 下载并解压 Xray-core 二进制
// P0-AG3: 下载 zip 后同时下载 .dgst 文件提取 SHA-256, 与本地 zip 比对, 校验失败删除并返回错误
// P1-AG9: io.Copy 加 200MB 上限, 防止恶意/异常响应耗尽磁盘
func (m *XrayManager) downloadXray() error {
	arch := xrayArch()
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/%s/Xray-linux-%s.zip", m.version, arch)
	log.Printf("下载 Xray-core %s: %s", m.version, url)

	tmpZip, err := os.CreateTemp("", "xray-*.zip")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmpZip.Name()
	defer os.Remove(tmpName)

	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		tmpZip.Close()
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		tmpZip.Close()
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmpZip.Close()
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	// P1-AG9: 限制下载大小 200MB, 防止异常响应耗尽磁盘
	if _, err := io.Copy(tmpZip, io.LimitReader(resp.Body, 200<<20)); err != nil {
		tmpZip.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmpZip.Close(); err != nil {
		return err
	}

	// P0-AG3: 下载 .dgst 文件提取 SHA-256, 与本地 zip 比对
	// .dgst 文件格式: SHA256(Xray-linux-64.zip)= <hex> (或类似 SHA2-256=...=<hex>)
	// 若 .dgst 下载失败(某些镜像可能不提供), 记录警告但继续(不阻断)
	dgstURL := url + ".dgst"
	dgstReq, _ := http.NewRequest("GET", dgstURL, nil)
	dgstResp, dgstErr := client.Do(dgstReq)
	if dgstErr == nil && dgstResp.StatusCode == http.StatusOK {
		dgstBytes, _ := io.ReadAll(io.LimitReader(dgstResp.Body, 4096))
		dgstResp.Body.Close()
		expectedHash := extractSHA256(string(dgstBytes))
		if expectedHash != "" {
			localHash, hashErr := sha256File(tmpName)
			if hashErr != nil {
				return fmt.Errorf("计算本地 zip SHA-256 失败: %w", hashErr)
			}
			if localHash != expectedHash {
				os.Remove(tmpName)
				return fmt.Errorf("xray 二进制 hash 校验失败: expected %s, got %s", expectedHash, localHash)
			}
			log.Printf("Xray zip hash 校验通过: %s", localHash)
		} else {
			log.Printf("[WARN] .dgst 文件已下载但未提取到 SHA-256, 跳过校验(继续解压)")
		}
	} else if dgstErr != nil {
		log.Printf("[WARN] 下载 .dgst 失败, 跳过 hash 校验(不阻断): %v", dgstErr)
	} else {
		log.Printf("[WARN] .dgst HTTP %d, 跳过 hash 校验(不阻断)", dgstResp.StatusCode)
	}

	if err := extractXrayBinary(tmpName, m.binaryPath); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}
	if err := os.Chmod(m.binaryPath, 0755); err != nil {
		return err
	}
	log.Printf("Xray-core 下载完成: %s", m.binaryPath)
	return nil
}

// extractSHA256 从 .dgst 文件内容中提取 SHA-256 哈希值
// 支持常见格式:
//   - "SHA256(filename.zip)= <hex>"
//   - "SHA2-256=filename.zip=<hex>"
//   - 行内含 "SHA256" / "SHA2-256" 且行尾有 64 位 hex
//
// 若未找到返回空字符串
func extractSHA256(dgstContent string) string {
	for _, line := range strings.Split(dgstContent, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if !strings.Contains(upper, "SHA256") && !strings.Contains(upper, "SHA2-256") {
			continue
		}
		// 取最后一个 '=' 后的部分作为 hash 候选
		idx := strings.LastIndex(line, "=")
		if idx < 0 {
			continue
		}
		hash := strings.TrimSpace(line[idx+1:])
		// SHA-256 hex 长度固定 64
		if len(hash) == 64 {
			return hash
		}
	}
	return ""
}

// sha256File 计算指定文件路径的 SHA-256 哈希值(返回小写 hex)
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// xrayArch 根据 runtime.GOARCH 返回 Xray 发布包的架构标识
func xrayArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "64"
	case "arm64":
		return "arm64-v8a"
	case "arm":
		return "arm32-v7a"
	default:
		return "64"
	}
}

// extractXrayBinary 从 zip 中提取 xray 二进制到 dest
// P1-AG8: 原子写+fsync — 先写到 dest.tmp, fsync 后 rename, 避免写一半进程崩溃导致二进制损坏
// (原实现直接写 dest, 进程在 io.Copy 中途崩溃会留下半截文件, 下次启动 Xray 会执行失败)
func extractXrayBinary(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.Base(f.Name) != "xray" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		tmpPath := dest + ".tmp"
		out, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		if err != nil {
			out.Close()
			os.Remove(tmpPath)
			return err
		}
		if err := out.Sync(); err != nil {
			out.Close()
			os.Remove(tmpPath)
			return err
		}
		out.Close()
		// 原子替换, 保证 dest 要么是完整新版本, 要么是旧版本, 不会是半截文件
		if err := os.Rename(tmpPath, dest); err != nil {
			os.Remove(tmpPath)
			return err
		}
		return nil
	}
	return fmt.Errorf("zip 中未找到 xray 二进制")
}

// OverrideListenPort 解析 Xray 配置 JSON，将第一个 inbound 的端口覆盖为 listenPort
// 如果 listenPort 为 0，则使用配置中已有的端口
func OverrideListenPort(cfgJSON string, listenPort int) (string, error) {
	if cfgJSON == "" {
		return "", fmt.Errorf("配置为空")
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return "", fmt.Errorf("解析 Xray 配置 JSON 失败: %w", err)
	}
	inbounds, ok := cfg["inbounds"].([]interface{})
	if !ok || len(inbounds) == 0 {
		return "", fmt.Errorf("Xray 配置缺少 inbounds")
	}
	if first, ok := inbounds[0].(map[string]interface{}); ok {
		if listenPort > 0 {
			first["port"] = listenPort
		}
		first["listen"] = "0.0.0.0"
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("重新序列化配置失败: %w", err)
	}
	return string(out), nil
}

// InjectStatsConfig 向 Xray 配置 JSON 中注入 stats 和 api 配置，使 Xray 启用用户级流量统计
// 并通过本地 API 端口暴露 StatsService。
//
// 注入内容:
//   - "stats": {} — 启用统计模块
//   - "api": {"tag": "api", "services": ["StatsService"]} — 启用 API 模块
//   - "policy": 添加 statsUserUplink/Downlink — 启用用户级流量计数
//   - inbounds: 添加 dokodemo-door inbound 监听 127.0.0.1:apiPort — API 入口
//   - routing: 添加 rule 将 api inbound 流量路由到 api outbound
//
// 如果配置已包含 stats 段则跳过注入（幂等）。
func InjectStatsConfig(cfgJSON string, apiPort int) (string, error) {
	if cfgJSON == "" {
		return "", fmt.Errorf("配置为空")
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return "", fmt.Errorf("解析 Xray 配置 JSON 失败: %w", err)
	}

	// 为所有 inbound 的 clients 注入 email 字段(用 id 作为 email)
	// Xray-core 用户级 stats 依赖 client.email 创建 counter:
	//   user>>>{email}>>>traffic>>>uplink / downlink
	// 缺少 email 时 xray 不创建用户级 counter, statsquery 只返回 inbound 级统计,
	// agent 查询到 0 用户, 真实用户流量无法上报(面板显示 0 bps)。
	// 用 client.id(UUID) 作为 email, 使 stats 名称与 agent parseUserTrafficStat 解析格式一致。
	injected := injectClientEmails(cfg)
	if injected > 0 {
		log.Printf("[xray] 为 %d 个 client 注入 email 字段(启用用户级流量统计)", injected)
	}

	// 幂等：已有 stats 配置则跳过注入(但 email 已注入, 需重新序列化)
	if _, hasStats := cfg["stats"]; hasStats {
		if injected == 0 {
			log.Printf("[xray] 配置已包含 stats 段且无需注入 email，跳过")
			return cfgJSON, nil
		}
		out, err := json.Marshal(cfg)
		if err != nil {
			return "", fmt.Errorf("重新序列化配置(email注入)失败: %w", err)
		}
		log.Printf("[xray] 配置已包含 stats 段，仅注入 email 后返回")
		return string(out), nil
	}

	cfg["stats"] = map[string]interface{}{}
	cfg["api"] = map[string]interface{}{
		"tag":      "api",
		"services": []string{"StatsService"},
	}

	policy, _ := cfg["policy"].(map[string]interface{})
	if policy == nil {
		policy = map[string]interface{}{}
		cfg["policy"] = policy
	}
	levels, _ := policy["levels"].(map[string]interface{})
	if levels == nil {
		levels = map[string]interface{}{
			"0": map[string]interface{}{
				"statsUserUplink":   true,
				"statsUserDownlink": true,
			},
		}
		policy["levels"] = levels
	}
	system, _ := policy["system"].(map[string]interface{})
	if system == nil {
		system = map[string]interface{}{
			"statsInboundUplink":   true,
			"statsInboundDownlink": true,
		}
		policy["system"] = system
	}

	var inbounds []interface{}
	if raw, ok := cfg["inbounds"].([]interface{}); ok {
		inbounds = raw
	}
	apiInbound := map[string]interface{}{
		"tag":      "api",
		"listen":   "127.0.0.1",
		"port":     apiPort,
		"protocol": "dokodemo-door",
		"settings": map[string]interface{}{
			"address": "127.0.0.1",
		},
	}
	inbounds = append([]interface{}{apiInbound}, inbounds...)
	cfg["inbounds"] = inbounds

	var outbounds []interface{}
	if raw, ok := cfg["outbounds"].([]interface{}); ok {
		outbounds = raw
	}
	outbounds = append(outbounds, map[string]interface{}{
		"tag":      "api",
		"protocol": "freedom",
		"settings": map[string]interface{}{},
	})
	cfg["outbounds"] = outbounds

	routing, _ := cfg["routing"].(map[string]interface{})
	if routing == nil {
		routing = map[string]interface{}{}
		cfg["routing"] = routing
	}
	var rules []interface{}
	if raw, ok := routing["rules"].([]interface{}); ok {
		rules = raw
	}
	apiRule := map[string]interface{}{
		"type":        "field",
		"inboundTag":  []string{"api"},
		"outboundTag": "api",
	}
	rules = append([]interface{}{apiRule}, rules...)
	routing["rules"] = rules

	out, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("重新序列化 stats 注入配置失败: %w", err)
	}
	log.Printf("[xray] Stats/API 配置已注入 (api_port=%d)", apiPort)
	return string(out), nil
}

// WriteConfig 写入 Xray 配置文件
// P1-AG7: 原子写 — 先写到 configPath.tmp, 再 rename, 避免写一半进程崩溃导致配置文件损坏
// (原实现直接 WriteFile, 进程在写中途崩溃会留下半截 JSON, Xray 启动解析失败)
func (m *XrayManager) WriteConfig(cfgJSON string) error {
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0755); err != nil {
		return err
	}
	tmpPath := m.configPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(cfgJSON), 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, m.configPath)
}

// IsRunning 返回 Xray 进程是否在运行
func (m *XrayManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

// BinaryPath 返回 Xray 二进制路径
func (m *XrayManager) BinaryPath() string {
	return m.binaryPath
}

// Start 启动 Xray 进程(若已在运行则跳过)
func (m *XrayManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked()
}

// startLocked 启动 Xray 进程的内部实现(不加锁), 供 Start 和 Restart 复用
// 调用方必须已持有 m.mu
func (m *XrayManager) startLocked() error {
	if m.cmd != nil {
		return nil
	}
	if _, err := os.Stat(m.binaryPath); err != nil {
		return fmt.Errorf("Xray 二进制不存在: %s", m.binaryPath)
	}

	cmd := exec.Command(m.binaryPath, "run", "-config", m.configPath)
	cmd.Stdout = logWriter{prefix: "[xray] "}
	cmd.Stderr = logWriter{prefix: "[xray] "}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 Xray 进程失败: %w", err)
	}
	m.cmd = cmd
	m.doneCh = make(chan struct{})
	log.Printf("Xray 进程已启动 pid=%d", cmd.Process.Pid)

	go func(c *exec.Cmd, ch chan struct{}) {
		err := c.Wait()
		if err != nil {
			log.Printf("Xray 进程退出: %v", err)
		} else {
			log.Printf("Xray 进程已退出")
		}
		m.mu.Lock()
		// 仅当当前 cmd 仍是本进程时清理(Stop 主动停止时已置 nil)
		if m.cmd == c {
			m.cmd = nil
		}
		m.mu.Unlock()
		close(ch)
	}(cmd, m.doneCh)

	return nil
}

// Stop 优雅停止 Xray 进程(SIGTERM，5s 超时后 SIGKILL)
func (m *XrayManager) Stop() error {
	m.mu.Lock()
	cmd := m.cmd
	if cmd == nil || cmd.Process == nil {
		m.mu.Unlock()
		return nil
	}
	doneCh := m.doneCh
	m.cmd = nil // 标记停止，Wait goroutine 不再重复清理
	m.mu.Unlock()

	pid := cmd.Process.Pid
	log.Printf("停止 Xray 进程 pid=%d", pid)
	_ = cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		log.Printf("Xray 未在 5s 内退出，发送 SIGKILL")
		_ = cmd.Process.Signal(syscall.SIGKILL)
		<-doneCh
	}
	return nil
}

// Restart 重启 Xray 进程
// P1-AG16: 用一把锁覆盖 Stop+Start, 防止并发 Restart/Start 在 Stop 释放锁后
// 被其他 goroutine 插入 Start, 导致多个 Xray 进程同时运行抢占端口
// (原实现 Stop 释放锁后再 Start 重新获取锁, 中间窗口可能被并发 Start 插入)
func (m *XrayManager) Restart() error {
	m.mu.Lock()
	cmd := m.cmd
	if cmd != nil && cmd.Process != nil {
		doneCh := m.doneCh
		m.cmd = nil
		pid := cmd.Process.Pid
		log.Printf("重启: 停止旧 Xray 进程 pid=%d", pid)
		// 注意: 此处不能持锁阻塞等待 SIGTERM(5s), 否则与 startLocked 的锁需求死锁。
		// 但又必须持锁防止并发。妥协: 在锁内发 SIGKILL 立即终止, 然后等 doneCh 关闭。
		// SIGKILL 比 SIGTERM 更暴力但能快速释放锁, 对重启场景可接受(Xray 无持久状态需保存)。
		_ = cmd.Process.Signal(syscall.SIGKILL)
		m.mu.Unlock()
		<-doneCh
		m.mu.Lock()
	} else {
		// 无运行中的进程, 直接持锁启动(不释放)
	}
	defer m.mu.Unlock()
	return m.startLocked()
}

// logWriter 将子进程输出按行写入标准日志
type logWriter struct {
	prefix string
}

func (w logWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		log.Printf("%s%s", w.prefix, msg)
	}
	return len(p), nil
}

// injectClientEmails 遍历所有 inbound 的 clients, 为缺少 email 的 client 添加 email(用 id 作为值)。
// Xray-core 用户级流量统计(statsquery)依赖 client.email 创建 counter, 缺少 email 时不统计。
// 返回注入 email 的 client 数量。
func injectClientEmails(cfg map[string]interface{}) int {
	injected := 0
	inbounds, ok := cfg["inbounds"].([]interface{})
	if !ok {
		return 0
	}
	for _, ib := range inbounds {
		inbound, ok := ib.(map[string]interface{})
		if !ok {
			continue
		}
		settings, ok := inbound["settings"].(map[string]interface{})
		if !ok {
			continue
		}
		clients, ok := settings["clients"].([]interface{})
		if !ok {
			continue
		}
		for _, c := range clients {
			client, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if _, hasEmail := client["email"]; hasEmail {
				continue
			}
			id, ok := client["id"].(string)
			if !ok || id == "" {
				continue
			}
			client["email"] = id
			injected++
		}
	}
	return injected
}
