package main

import (
	"archive/zip"
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
func (m *XrayManager) downloadXray() error {
	arch, err := xrayArch()
	if err != nil {
		return err
	}
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
	if _, err := io.Copy(tmpZip, resp.Body); err != nil {
		tmpZip.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmpZip.Close(); err != nil {
		return err
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

// xrayArch 根据 runtime.GOARCH 返回 Xray 发布包的架构标识
// [P1-2 2026-07-17] 不支持的架构返回错误而非默认 amd64, 防止下载到不兼容二进制后段错误
func xrayArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "64", nil
	case "arm64":
		return "arm64-v8a", nil
	case "arm":
		return "arm32-v7a", nil
	case "386":
		return "32", nil
	default:
		return "", fmt.Errorf("不支持的 CPU 架构: %s (当前 Xray-core 仅支持 amd64/arm64/arm/386)", runtime.GOARCH)
	}
}

// extractXrayBinary 从 zip 中提取 xray 二进制到 dest
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
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
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

// WriteConfig 写入 Xray 配置文件
func (m *XrayManager) WriteConfig(cfgJSON string) error {
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(m.configPath, []byte(cfgJSON), 0644)
}

// IsRunning 返回 Xray 进程是否在运行
func (m *XrayManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

// Start 启动 Xray 进程(若已在运行则跳过)
func (m *XrayManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

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
func (m *XrayManager) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}
	return m.Start()
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
