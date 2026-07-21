package handler

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/security"

	"go.uber.org/zap"
)

// ====== 部署错误码（前端可针对性提示） ======
const (
	DeployErrDockerNotInstalled = "DOCKER_NOT_INSTALLED" // Docker 未安装（已自动安装则不报）
	DeployErrPortConflict       = "PORT_CONFLICT"        // 目标端口已被占用
	DeployErrDiskFull           = "DISK_FULL"            // 磁盘空间不足
	DeployErrMemoryLow          = "MEMORY_LOW"           // 内存不足 1G
	DeployErrSSHConnect         = "SSH_CONNECT_FAIL"     // SSH 连接失败
	DeployErrSSHAuth            = "SSH_AUTH_FAIL"        // SSH 认证失败
	DeployErrSSHTimeout         = "SSH_TIMEOUT"          // SSH 连接超时
	DeployErrBuild              = "BUILD_FAIL"           // 编译失败
	DeployErrTransfer           = "TRANSFER_FAIL"        // 传输失败
	DeployErrStart              = "START_FAIL"           // 容器启动失败
	DeployErrVerify             = "VERIFY_FAIL"          // 节点注册验证失败
	DeployErrUnknown            = "UNKNOWN"
)

// ====== 部署阶段常量（6 步） ======
const (
	PhaseConnectServer = "connect_server" // 1. 连接服务器
	PhaseEnvCheck      = "env_check"      // 2. 环境检测
	PhasePrepare       = "prepare"        // 3. 准备部署(目录+文件+配置+Docker)
	PhaseBuild         = "build"          // 4. 编译程序
	PhaseStart         = "start"          // 5. 启动服务
	PhaseVerify        = "verify"         // 6. 验证完成
)

// ====== 重试配置 ======
const (
	MaxDeployRetries = 3
	RetryInterval    = 30 // 秒
)

// AutoDeployHandler 一键自动部署：面板 SSH 到节点服务器，自动推送文件、装 Docker、启动、验证
type AutoDeployHandler struct {
	nodeRepo *repo.NodeRepo
	jwtMgr   *security.JWTManager
}

func NewAutoDeployHandler(nodeRepo *repo.NodeRepo, jwt *security.JWTManager) *AutoDeployHandler {
	return &AutoDeployHandler{nodeRepo: nodeRepo, jwtMgr: jwt}
}

type autoDeployReq struct {
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"` // SSH 私钥文本(支持密钥认证, 与密码二选一, 密钥优先)
	Username   string `json:"username"`
	Port       int    `json:"port"`
}

// node_agent 源码路径，优先使用环境变量 NODE_AGENT_PATH，否则回退值
var nodeAgentPath = getNodeAgentPath()

func getNodeAgentPath() string {
	if p := os.Getenv("NODE_AGENT_PATH"); p != "" {
		return p
	}
	// 默认为 Docker 容器内挂载路径
	return "/app/node_agent"
}

// getHostProjectRoot 获取宿主机上的项目根目录
// 用于通过 docker.sock 调用宿主机 docker 时的 -v 挂载源路径
// 因为容器内路径 ≠ 宿主机路径，必须单独配置
// 优先读 HOST_PROJECT_ROOT 环境变量，没配置则回退到容器内 getGitRoot() 的值（兼容旧部署）
func getHostProjectRoot() string {
	if p := os.Getenv("HOST_PROJECT_ROOT"); p != "" {
		return p
	}
	return getGitRoot()
}

// getHostNodeAgentPath 获取宿主机上的 node_agent 目录路径
// 用于在面板服务器上预编译 node-agent 二进制时的 docker run -v 挂载
func getHostNodeAgentPath() string {
	if p := os.Getenv("HOST_NODE_AGENT_PATH"); p != "" {
		return p
	}
	return filepath.Join(getHostProjectRoot(), "node_agent")
}

// 从配置 PanelDomain 提取面板服务器公网 IP
func getPanelIP() string {
	// 优先用 PANEL_GRPC_HOST 环境变量(节点连面板用，避免 Cloudflare 域名无法转发 50051 端口)
	if host := os.Getenv("PANEL_GRPC_HOST"); host != "" {
		return host
	}
	domain := app.Get().Cfg.PanelDomain
	if domain == "" {
		return ""
	}
	if u, err := url.Parse(domain); err == nil && u.Host != "" {
		return u.Hostname()
	}
	return strings.TrimPrefix(strings.TrimPrefix(domain, "http://"), "https://")
}

// panelGrpcAddrInfo 描述节点连接面板 gRPC 的地址决策
//
// 修复 START_FAIL-CDN (P0): 面板用公信 CA 证书(如 Let's Encrypt 签的 bbcdtv.top)时,
// 证书 SAN 只有域名没有 IP。如果 .env.node 的 PANEL_GRPC_ADDR 写成 IP:50051,
// agent TLS 握手时 ServerName 校验会失败("x509: cannot validate certificate for IP
// because it doesn't contain any IP SANs"), 节点永久掉线。
// 同时 bbcdtv.top 走 Cloudflare CDN, DNS 解析到 CF IP 无法直达 50051 端口。
//
// 决策:
//   - Addr:       写入 .env.node 的 PANEL_GRPC_ADDR 的 host 部分(域名或 IP)
//   - RealIP:     面板服务器真实 IP(PANEL_GRPC_HOST 或域名解析)
//   - UseDomain:  true 表示 Addr 是域名, 需要在节点 /etc/hosts 写 RealIP→Addr 映射
//     (绕过 CDN 直达面板服务器, 同时让 TLS ServerName 匹配证书 SAN)
//   - IsCDN:      true 表示域名走 CDN(RealIP 与域名 DNS 解析不同)
type panelGrpcAddrInfo struct {
	Addr      string // 写入 .env.node 的 PANEL_GRPC_ADDR host(可能是域名或 IP)
	RealIP    string // 面板服务器真实 IP(用于 /etc/hosts 映射)
	UseDomain bool   // true=Addr 是域名, 需写 /etc/hosts 绕过 CDN
}

// resolvePanelGrpcAddr 决策节点连接面板 gRPC 的地址
// 优先级:
//  1. 面板启用 TLS + PanelDomain 是域名 → 用域名(让 TLS ServerName 匹配证书 SAN)
//     同时若 PANEL_GRPC_HOST 已配置(说明 CDN 场景), 通过 /etc/hosts 绕过 CDN
//  2. 其他场景 → 用 PANEL_GRPC_HOST 或 PanelDomain 解析结果(IP 或域名)
func resolvePanelGrpcAddr() panelGrpcAddrInfo {
	domain := app.Get().Cfg.PanelDomain
	grpcHost := os.Getenv("PANEL_GRPC_HOST")

	// 提取 PanelDomain 中的域名部分(去掉 https:// 前缀)
	domainHost := ""
	if domain != "" {
		if u, err := url.Parse(domain); err == nil && u.Host != "" {
			domainHost = u.Hostname()
		} else {
			domainHost = strings.TrimPrefix(strings.TrimPrefix(domain, "http://"), "https://")
		}
	}

	// 检查 domainHost 是不是 IP(若是 IP 则不进入 CDN 处理)
	isIP := net.ParseIP(domainHost) != nil

	// 面板启用 TLS + PanelDomain 是域名(非 IP) → 必须用域名让 TLS ServerName 匹配证书 SAN
	// 修复 START_FAIL-CDN: 旧版用 PANEL_GRPC_HOST(IP) 让 agent 连, 但公信 CA 证书 SAN 无 IP,
	// TLS 校验失败 → START_FAIL
	if app.Get().Cfg.GRPCTLSEnabled() && domainHost != "" && !isIP {
		info := panelGrpcAddrInfo{
			Addr:      domainHost,
			RealIP:    grpcHost,
			UseDomain: true,
		}
		// 如果没配 PANEL_GRPC_HOST, RealIP 留空, 由节点 DNS 直接解析(可能命中 CDN)
		if grpcHost == "" {
			info.UseDomain = false // 没有 RealIP 就不需要写 /etc/hosts
		}
		return info
	}

	// 非 TLS 场景或 PanelDomain 是 IP: 直接用 PANEL_GRPC_HOST(若配置), 否则用域名
	if grpcHost != "" {
		return panelGrpcAddrInfo{Addr: grpcHost, RealIP: grpcHost, UseDomain: false}
	}
	return panelGrpcAddrInfo{Addr: domainHost, RealIP: domainHost, UseDomain: false}
}

// ensureHostsMapping 在节点 /etc/hosts 添加 panelRealIP → domain 映射, 绕过 CDN
// 幂等: 已存在映射则跳过
// 修复 START_FAIL-CDN: bbcdtv.top 走 CF CDN, 节点 DNS 解析到 CF IP 无法直达 50051 端口
// 通过 /etc/hosts 把域名映射到面板真实 IP, 既绕过 CDN 又让 TLS ServerName 匹配证书 SAN
func ensureHostsMapping(client *ssh.Client, panelRealIP, domain string) (string, error) {
	if panelRealIP == "" || domain == "" {
		return "(skip: 空参数)", nil
	}
	// 幂等: 先检查是否已存在映射
	checkCmd := fmt.Sprintf("grep -c '^%s[[:space:]]\\+%s\\b' /etc/hosts 2>/dev/null || echo 0",
		panelRealIP, domain)
	out, err := sshRun(client, checkCmd)
	if err == nil && strings.TrimSpace(out) != "0" {
		return fmt.Sprintf("(已存在映射: %s → %s)", panelRealIP, domain), nil
	}
	// 追加映射(用 >> 避免覆盖)
	addCmd := fmt.Sprintf("echo '%s %s  # nexus-panel grpc bypass CDN' >> /etc/hosts && echo OK",
		panelRealIP, domain)
	out, err = sshRun(client, addCmd)
	if err != nil {
		return out, fmt.Errorf("写 /etc/hosts 失败: %w", err)
	}
	return fmt.Sprintf("(已添加映射: %s → %s)", panelRealIP, domain), nil
}

// precheckGRPCTLS 部署前在节点服务器上预检面板 gRPC TLS 连通性
// 通过 openssl s_client 测试握手, 区分 mTLS/证书无效/端口不通等错误
// 返回 (诊断信息, 错误码), errCode 为空表示通过
//
// 修复 START_FAIL-MTLS (P0): 旧版没有预检, 容器启动后才发现 mTLS 错误(tls: certificate required),
// agent 重试 30 次失败 → START_FAIL → 重试 3 次部署全失败。
// 现在在 Phase 3 阶段就预检, 失败直接报错避免无效部署。
func precheckGRPCTLS(client *ssh.Client, panelAddr string, tlsEnabled bool) (diag string, errCode string) {
	host, port, err := net.SplitHostPort(panelAddr)
	if err != nil {
		return fmt.Sprintf("PANEL_GRPC_ADDR 格式错误: %s (%v)", panelAddr, err), DeployErrUnknown
	}

	if !tlsEnabled {
		// 面板没启用 TLS, 测试 TCP 连通即可
		out, _ := sshRun(client, fmt.Sprintf(
			"timeout 5 bash -c 'cat < /dev/null > /dev/tcp/%s/%s' 2>&1 && echo TCP_OK || echo TCP_FAIL",
			host, port))
		if !strings.Contains(out, "TCP_OK") {
			return fmt.Sprintf("面板 gRPC 端口 %s 不可达(TCP 测试失败): %s", panelAddr, out), DeployErrSSHConnect
		}
		return "", ""
	}

	// TLS 模式: 用 openssl s_client 测试握手
	// -connect: 目标地址  -servername: SNI(让服务端返回正确证书)
	// -brief: 简洁输出(包含 Verify return code)
	cmd := fmt.Sprintf(
		"echo | timeout 8 openssl s_client -connect %s -servername %s -brief 2>&1 | head -30 || true",
		panelAddr, host)
	out, _ := sshRun(client, cmd)

	switch {
	case strings.Contains(out, "certificate required"):
		return fmt.Sprintf(
			"面板启用了 mTLS(双向 TLS), 要求客户端证书, 但 agent 没配置客户端证书。\n"+
				"解决:\n"+
				"  1) 面板 .env 注释掉 GRPC_TLS_CA 改用单向 TLS, 然后 `docker compose up -d panel` 重建面板\n"+
				"  2) 或给 agent 配置 GRPC_TLS_CERT/GRPC_TLS_KEY 客户端证书\n"+
				"openssl 输出:\n%s", out), DeployErrStart
	case strings.Contains(out, "verify error") || strings.Contains(out, "verification failed") || strings.Contains(out, "self-signed"):
		return fmt.Sprintf(
			"面板 TLS 证书校验失败, agent 无法验证面板证书。\n"+
				"解决:\n"+
				"  1) 检查 .env.node 的 GRPC_TLS_CA 路径是否正确\n"+
				"  2) 自签 CA: 确认 grpc-ca.crt 已推送到节点\n"+
				"  3) 公信 CA(Let's Encrypt): GRPC_TLS_CA=/etc/ssl/certs/ca-certificates.crt\n"+
				"openssl 输出:\n%s", out), DeployErrStart
	case strings.Contains(out, "Connection refused") || strings.Contains(out, "No route") ||
		strings.Contains(out, "Connection timed out") || strings.Contains(out, "connect:"):
		return fmt.Sprintf("面板 gRPC 端口 %s 不可达: %s", panelAddr, out), DeployErrSSHConnect
	case strings.Contains(out, "Verification: OK") || strings.Contains(out, "Verify return code: 0"):
		return "", "" // 握手成功
	}

	// 兜底: 输出可疑但仍可能成功, 不阻断但记录日志
	app.Get().Logger.Warn("gRPC TLS 预检输出未匹配已知模式, 继续部署",
		zap.String("addr", panelAddr), zap.String("openssl_out", out))
	return "", ""
}

// hostKeyCallback SSH 主机密钥验证（信任首次连接，后续验证指纹一致性）
func hostKeyCallback(host string) ssh.HostKeyCallback {
	return trustOnFirstUse("deploy:" + host + ":")
}

// ============================================================
// SSE 辅助
// ============================================================

type sseWriter struct {
	mu      sync.Mutex
	flusher http.Flusher
	writer  gin.ResponseWriter
}

func (w *sseWriter) send(data string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// 容错: 前端断开后写入会返回错误，不应影响部署流程
	defer func() {
		_ = recover()
	}()
	fmt.Fprintf(w.writer, "%s\n\n", data)
	w.flusher.Flush()
}

func (w *sseWriter) event(step, status, msg, output string) {
	data, _ := json.Marshal(map[string]string{
		"step": step, "status": status, "msg": msg, "output": output,
	})
	w.send("data: " + string(data))
}

// eventWithCode 带错误码的事件，前端可针对性展示
func (w *sseWriter) eventWithCode(step, status, msg, output, errCode string) {
	data, _ := json.Marshal(map[string]string{
		"step": step, "status": status, "msg": msg, "output": output, "errCode": errCode,
	})
	w.send("data: " + string(data))
}

// ============================================================
// Deploy 主流程 (含 3 次重试)
// ============================================================

// Deploy 一键部署：用 SSE 流式推送进度; 失败自动重试 3 次，每次间隔 30 秒
func (h *AutoDeployHandler) Deploy(c *gin.Context) {
	nodeID := c.Param("id")
	node, err := h.nodeRepo.GetByID(nodeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"msg": "节点不存在"})
		return
	}

	var req autoDeployReq
	if err := c.ShouldBindJSON(&req); err != nil || (req.Password == "" && req.PrivateKey == "") {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "缺少 SSH 密码或私钥"})
		return
	}
	if req.Username == "" {
		req.Username = "root"
	}
	if req.Port == 0 {
		req.Port = 22
	}
	// 安全：将敏感字段移出 req，避免后续误用或日志泄露
	password := req.Password
	privateKey := req.PrivateKey
	port := req.Port
	username := req.Username
	req.Password = ""
	req.PrivateKey = ""
	req.Port = 0
	req.Username = ""

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

	sse := &sseWriter{flusher: flusher, writer: c.Writer}

	// SSE 心跳
	heartbeatDone := make(chan struct{})
	safeGo(func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sse.send(": heartbeat")
			case <-heartbeatDone:
				return
			case <-c.Request.Context().Done():
				return
			}
		}
	})
	defer close(heartbeatDone)

	// ====== 重试循环 (最多 3 次, 间隔 30 秒) ======
	var lastErrCode, lastErrMsg string
	for attempt := 1; attempt <= MaxDeployRetries; attempt++ {
		if attempt > 1 {
			sse.event(PhaseConnectServer, "warning",
				fmt.Sprintf("第 %d/%d 次重试 (等待 %d 秒)...", attempt, MaxDeployRetries, RetryInterval), "")
			// 等待 30 秒, 期间每 5 秒发一次心跳
			for sec := RetryInterval; sec > 0; sec -= 5 {
				select {
				case <-c.Request.Context().Done():
					return
				case <-time.After(5 * time.Second):
					sse.event(PhaseConnectServer, "log", "", fmt.Sprintf("倒计时 %d 秒...", sec))
				}
			}
		}

		// 执行单次部署
		ok, errCode, errMsg := h.runDeployOnce(c, sse, node, password, privateKey, port, username)
		if ok {
			// 安全：完成部署后清除密码/密钥
			password = ""
			privateKey = ""
			sse.event("finish", "done", "一键部署完成！请返回节点列表查看在线状态", "")
			if f, ok2 := c.Writer.(http.Flusher); ok2 {
				f.Flush()
			}
			time.Sleep(100 * time.Millisecond)
			return
		}

		lastErrCode = errCode
		lastErrMsg = errMsg

		// 致命/无意义重试的错误不重试
		if errCode == DeployErrSSHAuth || errCode == DeployErrSSHConnect || errCode == DeployErrDockerNotInstalled || errCode == DeployErrPortConflict {
			reason := "部署失败"
			if errCode == DeployErrDockerNotInstalled {
				reason = "Docker 安装失败 (重试不会改变结果，请手动排查)"
			} else if errCode == DeployErrPortConflict {
				reason = "端口冲突 (Phase 1 已自动清理，仍被占用请手动释放)"
			}
			sse.eventWithCode(PhaseVerify, "error",
				fmt.Sprintf("%s (%s): %s\n\n修复建议: %s", reason, errCode, errMsg, fixSuggestion(errCode)),
				"", errCode)
			return
		}
	}

	// 3 次都失败
	sse.eventWithCode(PhaseVerify, "error",
		fmt.Sprintf("已重试 %d 次仍失败 (%s): %s\n\n最后建议: %s", MaxDeployRetries, lastErrCode, lastErrMsg, fixSuggestion(lastErrCode)),
		"", lastErrCode)
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
	time.Sleep(100 * time.Millisecond)
}

// runDeployOnce 执行一次完整部署; 返回 (成功?, 错误码, 错误信息)
// privateKey 为 SSH 私钥文本(PEM 格式), 与 password 二选一, 密钥优先
func (h *AutoDeployHandler) runDeployOnce(c *gin.Context, sse *sseWriter, node *model.Node, password string, privateKey string, port int, username string) (bool, string, string) {
	panelIP := getPanelIP()
	if panelIP == "" {
		sse.eventWithCode(PhaseConnectServer, "error",
			"面板域名未配置，请在管理后台设置 PanelDomain", "", DeployErrUnknown)
		return false, DeployErrUnknown, "面板域名未配置"
	}

	// 清洗服务器地址（去除首尾空格，防止 DNS 解析失败）
	node.ServerAddress = strings.TrimSpace(node.ServerAddress)

	// 多节点支持: 按节点 ID 前 8 位区分容器名和部署目录
	shortID := node.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	containerName := "nexus-agent-" + shortID
	deployDir := "/root/node-agent-" + shortID

	// 计算需要检测的端口
	listenPort := node.Port
	if listenPort == 0 {
		listenPort = 443
	}
	healthPort := 50052
	grpcPortStr := "50051"
	if listen := app.Get().Cfg.GRPCListen; listen != "" {
		if idx := strings.LastIndex(listen, ":"); idx >= 0 && idx+1 < len(listen) {
			grpcPortStr = listen[idx+1:]
		}
	}

	// ============================================================
	// Phase 1: 连接服务器
	// ============================================================
	sse.event(PhaseConnectServer, "running", "正在连接节点服务器 "+node.ServerAddress+":"+strconv.Itoa(port)+"...", "")

	// 构建 SSH 认证方法: 密钥优先, 密码兜底
	var authMethods []ssh.AuthMethod
	if privateKey != "" {
		// 密钥认证: 支持 PEM 格式私钥 (RSA/Ed25519/ECDSA)
		signer, keyErr := parsePrivateKey(privateKey)
		if keyErr != nil {
			sse.eventWithCode(PhaseConnectServer, "error",
				"SSH 私钥解析失败: "+keyErr.Error()+"\n\n请确认: 1) 私钥为 PEM 格式 2) 已粘贴完整内容(含 -----BEGIN ...----- 和 -----END ...-----)",
				"", DeployErrSSHAuth)
			return false, DeployErrSSHAuth, "SSH 私钥解析失败: " + keyErr.Error()
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
		// 兜底: 密钥失败时尝试密码
		if password != "" {
			authMethods = append(authMethods, ssh.Password(password))
		}
		sse.event(PhaseConnectServer, "log", "", "使用 SSH 密钥认证...")
	} else {
		authMethods = append(authMethods, ssh.Password(password))
	}

	sshConfig := &ssh.ClientConfig{
		User:            username,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback(node.ServerAddress),
		Timeout:         15 * time.Second,
		Config: ssh.Config{
			KeyExchanges: []string{
				"curve25519-sha256",
				"curve25519-sha256@libssh.org",
				"ecdh-sha2-nistp256",
				"ecdh-sha2-nistp384",
				"ecdh-sha2-nistp521",
				"diffie-hellman-group14-sha256",
				"diffie-hellman-group14-sha1",
				"diffie-hellman-group16-sha512",
				"diffie-hellman-group18-sha512",
				"diffie-hellman-group-exchange-sha256",
				"diffie-hellman-group-exchange-sha1",
				"sntrup761x25519-sha512@openssh.com",
			},
			Ciphers: []string{
				"aes256-gcm@openssh.com",
				"aes128-gcm@openssh.com",
				"aes256-ctr",
				"aes192-ctr",
				"aes128-ctr",
				"chacha20-poly1305@openssh.com",
			},
		},
	}
	addr := fmt.Sprintf("%s:%d", node.ServerAddress, port)

	// 兜底: SSH 连接重试 3 次 (网络抖动、ssh 服务重启中等)
	var client *ssh.Client
	var dialErr error
	for retry := 1; retry <= 3; retry++ {
		client, dialErr = ssh.Dial("tcp", addr, sshConfig)
		if dialErr == nil {
			break
		}
		if retry < 3 {
			sse.event(PhaseConnectServer, "log", "", fmt.Sprintf("SSH 连接第 %d 次失败, %d 秒后重试... (%v)", retry, retry*2, dialErr))
			time.Sleep(time.Duration(retry*2) * time.Second)
		}
	}
	if dialErr != nil {
		errStr := dialErr.Error()
		code := classifySSHError(errStr)
		sse.eventWithCode(PhaseConnectServer, "error",
			"SSH 连接失败(重试3次): "+errStr+diagnoseSSHError(dialErr, port), "", code)
		return false, code, "SSH 连接失败: " + errStr
	}
	defer client.Close()
	sse.event(PhaseConnectServer, "done", "SSH 连接成功", "")

	// 兜底: 连接后检测 SSH 会话是否真正可用
	if testOut, testErr := sshRun(client, "echo 'SSH_OK'"); testErr != nil || !strings.Contains(testOut, "SSH_OK") {
		sse.eventWithCode(PhaseConnectServer, "error",
			"SSH 已连接但无法执行命令: "+fmt.Sprintf("%v", testErr), testOut, DeployErrSSHConnect)
		return false, DeployErrSSHConnect, "SSH 会话不可用"
	}

	// 清理同节点旧容器 + 旧部署目录 + 占用端口的旧进程 (容错: 可能本来就不存在)
	// 修复 NODE-RETRY-01 (P0): 旧版只清同名容器, 不清部署目录。
	//   3 次重试间若上次失败留下了脏 .env.node / 半截 agent 二进制 / 旧 xray-cache,
	//   下次部署 mkdir -p 不会删旧文件, 可能与新版本冲突(xray-cache 版本不匹配等)。
	//   现在重试前先 docker compose down + rm -rf 部署目录, 确保每次部署都是干净状态。
	sse.event(PhaseConnectServer, "running", "正在清理旧部署残留(容器+目录+端口)...", "")
	// 兜底: 清理命令逐条执行, 每条独立容错, 避免一条失败导致后续不执行
	cleanSteps := []struct {
		desc string
		cmd  string
	}{
		{"docker compose down", fmt.Sprintf("cd %s 2>/dev/null && docker compose -f docker-compose.node.yml down --timeout 10 2>/dev/null; true", deployDir)},
		{"停止旧容器", fmt.Sprintf("docker stop %s 2>/dev/null; true", containerName)},
		{"删除旧容器", fmt.Sprintf("docker rm -f %s 2>/dev/null; true", containerName)},
		{"释放端口进程", fmt.Sprintf("fuser -k %d/tcp 2>/dev/null; fuser -k %d/tcp 2>/dev/null; true", listenPort, healthPort)},
		{"删除旧目录", fmt.Sprintf("rm -rf %s 2>/dev/null; true", deployDir)},
	}
	var cleanLines []string
	for _, step := range cleanSteps {
		out, err := sshRun(client, step.cmd)
		if err != nil {
			app.Get().Logger.Warn("清理步骤失败(非致命)",
				zap.String("step", step.desc), zap.String("container", containerName), zap.Error(err))
			cleanLines = append(cleanLines, fmt.Sprintf("[跳过] %s: %v", step.desc, err))
		} else {
			cleanLines = append(cleanLines, fmt.Sprintf("[完成] %s: %s", step.desc, strings.TrimSpace(out)))
		}
	}
	// 兜底: 强制清理可能卡住的容器 (docker rm -f 可能因为 containerd 卡住而超时)
	sshRun(client, fmt.Sprintf("docker rm -f %s 2>/dev/null; true", containerName))
	// 兜底: 清理可能残留的 Docker 网络 (旧 compose 项目可能留下网络冲突)
	sshRun(client, fmt.Sprintf("docker network prune -f 2>/dev/null; true"))
	cleanLines = append(cleanLines, "CLEANED")
	sse.event(PhaseConnectServer, "done", "旧残留清理完成", strings.Join(cleanLines, "\n"))

	// ============================================================
	// Phase 2: 环境检测 (逐步推送 SSE 事件)
	// ============================================================
	checkResult := preDeployCheck(client, listenPort, healthPort, sse)
	if !checkResult.OK {
		// 致命错误直接退出
		if checkResult.Fatal {
			sse.eventWithCode(PhaseEnvCheck, "error",
				"环境检测未通过: "+checkResult.Reason+"\n\n修复建议: "+checkResult.FixSuggestion,
				checkResult.Output, checkResult.ErrCode)
			return false, checkResult.ErrCode, checkResult.Reason
		}
		// 非致命警告继续
		sse.eventWithCode(PhaseEnvCheck, "warning",
			"环境检测有警告: "+checkResult.Reason, checkResult.Output, checkResult.ErrCode)
	} else {
		sse.event(PhaseEnvCheck, "done", "环境检测通过", checkResult.Output)
	}

	// ============================================================
	// Phase 3: 准备部署 (目录 + 文件 + 配置 + Docker)
	// ============================================================
	sse.event(PhasePrepare, "running", "正在准备部署环境...", "")

	// 3.1 创建远程目录 (兜底: 失败重试 3 次, 间隔递增)
	var mkdirErr error
	var mkdirOut string
	for retry := 1; retry <= 3; retry++ {
		mkdirOut, mkdirErr = sshRun(client, "mkdir -p "+deployDir)
		if mkdirErr == nil {
			break
		}
		if retry < 3 {
			sse.event(PhasePrepare, "log", "", fmt.Sprintf("创建目录失败(第%d次), %d秒后重试...", retry, retry))
			time.Sleep(time.Duration(retry) * time.Second)
		}
	}
	if mkdirErr != nil {
		sse.eventWithCode(PhasePrepare, "error", "创建目录失败(重试3次): "+mkdirErr.Error(), mkdirOut, DeployErrUnknown)
		return false, DeployErrUnknown, "创建目录失败: " + mkdirErr.Error()
	}

	// 3.2 推送 node_agent (兜底: 失败重试 2 次)
	var uploadErr error
	for retry := 1; retry <= 2; retry++ {
		uploadErr = uploadNodeAgent(client, deployDir)
		if uploadErr == nil {
			break
		}
		if retry < 2 {
			sse.event(PhasePrepare, "log", "", fmt.Sprintf("推送文件失败(第%d次), 正在重试... (%v)", retry, uploadErr))
			// 兜底: 清理可能残留的半截文件
			sshRun(client, "rm -rf "+deployDir+"/* 2>/dev/null; true")
			time.Sleep(2 * time.Second)
		}
	}
	if uploadErr != nil {
		sse.eventWithCode(PhasePrepare, "error", "推送文件失败(重试2次): "+uploadErr.Error(), "", DeployErrTransfer)
		return false, DeployErrTransfer, "推送文件失败: " + uploadErr.Error()
	}

	// 3.3 安装 Docker (如果未安装)
	dockerOK, dockerCode, dockerMsg := ensureDocker(client, sse)
	if !dockerOK {
		sse.eventWithCode(PhasePrepare, "error",
			"Docker 安装失败: "+dockerMsg+"\n\n修复建议: "+fixSuggestion(dockerCode),
			"", dockerCode)
		return false, dockerCode, dockerMsg
	}

	// 兜底: ensureDocker 返回 true 后做二次验证, 确保 docker.sock 可访问,
	// 避免刚装完 Docker daemon 未完全就绪时 compose up 报 "Cannot connect to Docker daemon"
	if !verifyDockerReady(client, sse) {
		sse.eventWithCode(PhasePrepare, "error",
			"Docker 守护进程未就绪(安装完成但无法通信)",
			"", DeployErrDockerNotInstalled)
		return false, DeployErrDockerNotInstalled, "Docker daemon 未就绪"
	}

	// 兜底: 预拉取基础镜像(alpine), 避免 compose up --build 时首次拉取超时
	// 低配 VPS(1GB 内存)拉取 alpine 镜像可能耗时 10-30 秒, compose up 自带的重试太短
	sse.event(PhasePrepare, "running", "拉取基础镜像 (alpine:3.19)...", "")
	pullOut, _ := sshRun(client, "docker pull alpine:3.19 2>&1 | tail -5")
	sse.event(PhasePrepare, "log", "", pullOut)

	// 3.4 创建 .env.node (兜底: 写入失败重试 2 次)
	// 修复 START_FAIL-CDN (P0): 旧版用 panelIP(IP) 写 PANEL_GRPC_ADDR, 但面板用公信 CA 证书
	// (如 Let's Encrypt 签的 bbcdtv.top)时证书 SAN 无 IP, agent TLS 握手失败。
	// 现在用 resolvePanelGrpcAddr 决策:
	//   - 面板启用 TLS + PanelDomain 是域名 → 用域名(让 TLS ServerName 匹配证书 SAN)
	//     同时若 PANEL_GRPC_HOST 已配置(CDN 场景), 通过 /etc/hosts 绕过 CDN 直达面板 IP
	//   - 其他场景 → 用 PANEL_GRPC_HOST 或 PanelDomain
	panelAddrInfo := resolvePanelGrpcAddr()
	grpcAddrHost := panelAddrInfo.Addr
	// 兜底: Addr 为空时回退到 panelIP(保持旧行为)
	if grpcAddrHost == "" {
		grpcAddrHost = panelIP
	}
	envContent := fmt.Sprintf("CONTAINER_NAME=%s\nPANEL_GRPC_ADDR=%s:%s\nNODE_TOKEN=%s\nLISTEN_PORT=%d\nHEALTH_PORT=%d\nXRAY_VERSION=v26.6.1",
		containerName, grpcAddrHost, grpcPortStr, node.NodeToken, listenPort, healthPort)

	// 修复 START_FAIL-CDN (P0): 域名走 CDN 时, 在节点 /etc/hosts 添加 panelRealIP → domain 映射
	// 这样 agent 用域名连 gRPC 时 DNS 解析到面板真实 IP(绕过 CDN),
	// 同时 TLS ServerName=domain 能匹配证书 SAN(如 bbcdtv.top)
	if panelAddrInfo.UseDomain && panelAddrInfo.RealIP != "" {
		sse.event(PhasePrepare, "running",
			fmt.Sprintf("面板域名走 CDN, 写 /etc/hosts 映射 %s → %s 绕过 CDN...",
				panelAddrInfo.RealIP, panelAddrInfo.Addr), "")
		hostsOut, hostsErr := ensureHostsMapping(client, panelAddrInfo.RealIP, panelAddrInfo.Addr)
		if hostsErr != nil {
			sse.eventWithCode(PhasePrepare, "error",
				"写 /etc/hosts 失败: "+hostsErr.Error()+"\n输出: "+hostsOut, "", DeployErrUnknown)
			return false, DeployErrUnknown, "写 /etc/hosts 失败: " + hostsErr.Error()
		}
		sse.event(PhasePrepare, "log", "", "hosts 映射: "+hostsOut)
	}

	// 修复 NODE-TLS-03 (P0): 面板启用 gRPC TLS 时, 主动给 agent 设置 GRPC_TLS_CA,
	// 避免 agent 用明文连 TLS 端口导致 "error reading server preface: EOF" 节点永久离线。
	// - 公信 CA(Let's Encrypt 等): agent 镜像内自带系统 CA bundle, 直接指向即可
	// - 自签 CA: 把面板的 CA 证书推送到节点, 挂载进容器
	if app.Get().Cfg.GRPCTLSEnabled() {
		if caPath := app.Get().Cfg.GRPCTLSCA; caPath != "" {
			// 自签 CA: 读取面板 CA 证书内容, 推送到节点
			caContent, err := os.ReadFile(caPath)
			if err != nil {
				sse.eventWithCode(PhasePrepare, "error", "读取面板 gRPC CA 证书失败: "+err.Error(), "", DeployErrUnknown)
				return false, DeployErrUnknown, "读取 CA 证书失败: " + err.Error()
			}
			// 兜底: CA 证书推送重试 2 次
			var caPushErr error
			for retry := 1; retry <= 2; retry++ {
				caPushErr = sshWriteFile(client, deployDir+"/grpc-ca.crt", string(caContent))
				if caPushErr == nil {
					break
				}
				if retry < 2 {
					sse.event(PhasePrepare, "log", "", fmt.Sprintf("CA 证书推送失败(第%d次), 重试中...", retry))
					time.Sleep(time.Second)
				}
			}
			if caPushErr != nil {
				sse.eventWithCode(PhasePrepare, "error", "推送 gRPC CA 证书失败(重试2次): "+caPushErr.Error(), "", DeployErrTransfer)
				return false, DeployErrTransfer, "推送 CA 证书失败: " + caPushErr.Error()
			}
			// 容器内路径(docker-compose 挂载 ./grpc-ca.crt -> /app/grpc-ca.crt)
			envContent += "\nGRPC_TLS_CA=/app/grpc-ca.crt"
			sse.event(PhasePrepare, "running", "面板 gRPC TLS 已启用(自签 CA), CA 证书已推送到节点", "")
		} else {
			// 公信 CA(Let's Encrypt): agent 镜像内自带系统 CA bundle
			envContent += "\nGRPC_TLS_CA=/etc/ssl/certs/ca-certificates.crt"
			sse.event(PhasePrepare, "running", "面板 gRPC TLS 已启用(公信 CA), agent 将用系统 CA 池验证", "")
		}
	}

	// 兜底: 写入 .env.node 重试 2 次
	var envWriteErr error
	for retry := 1; retry <= 2; retry++ {
		envWriteErr = sshWriteFile(client, deployDir+"/.env.node", envContent)
		if envWriteErr == nil {
			break
		}
		if retry < 2 {
			sse.event(PhasePrepare, "log", "", fmt.Sprintf("写配置文件失败(第%d次), 重试中...", retry))
			// 兜底: 先删掉可能损坏的文件
			sshRun(client, "rm -f "+deployDir+"/.env.node 2>/dev/null; true")
			time.Sleep(time.Second)
		}
	}
	if envWriteErr != nil {
		sse.eventWithCode(PhasePrepare, "error", "写配置文件失败(重试2次): "+envWriteErr.Error(), "", DeployErrUnknown)
		return false, DeployErrUnknown, "写配置失败: " + envWriteErr.Error()
	}

	// 兜底: 验证配置文件内容是否正确写入
	verifyEnvOut, _ := sshRun(client, "cat "+deployDir+"/.env.node 2>/dev/null | wc -l || echo '0'")
	if strings.TrimSpace(verifyEnvOut) == "0" {
		sse.eventWithCode(PhasePrepare, "error", "配置文件写入后验证失败, 文件为空", "", DeployErrUnknown)
		return false, DeployErrUnknown, "配置文件验证失败"
	}

	// 3.5 gRPC 连通性预检(避免容器启动后才发现 mTLS/证书错, 减少 START_FAIL)
	// 修复 START_FAIL-MTLS (P0): 旧版没有预检, 容器启动后才发现 mTLS 错(tls: certificate required),
	// agent 重试 30 次失败 → START_FAIL → 重试 3 次部署全失败。
	// 现在部署前在节点上用 openssl s_client 测握手, 失败立即报错, 给出明确修复建议。
	grpcAddr := fmt.Sprintf("%s:%s", grpcAddrHost, grpcPortStr)
	sse.event(PhasePrepare, "running",
		fmt.Sprintf("预检面板 gRPC 连通性 (%s, TLS=%v)...", grpcAddr, app.Get().Cfg.GRPCTLSEnabled()), "")
	preDiag, preErrCode := precheckGRPCTLS(client, grpcAddr, app.Get().Cfg.GRPCTLSEnabled())
	if preErrCode != "" {
		sse.eventWithCode(PhasePrepare, "error",
			"gRPC 连通性预检失败, 部署中止(避免容器启动后再失败):\n\n"+preDiag+
				"\n\n修复建议: "+fixSuggestion(preErrCode), "", preErrCode)
		return false, preErrCode, "gRPC 预检失败: " + preDiag
	}
	if preDiag != "" {
		sse.event(PhasePrepare, "log", "", "gRPC 预检提示: "+preDiag)
	}
	sse.event(PhasePrepare, "log", "", "gRPC 连通性预检通过 ✓")

	sse.event(PhasePrepare, "done", "部署环境就绪", "目录/文件/Docker/配置 全部就绪")

	// ============================================================
	// Phase 4: 编译程序
	// ============================================================
	sse.event(PhaseBuild, "running", "正在预编译 node-agent 二进制...", "")

	localNodeAgentPath := "/app/node_agent"
	checkBinCmd := fmt.Sprintf(
		"if [ -f %s/agent ]; then "+
			"if find %s/agent -mmin -1440 >/dev/null 2>&1; then echo 'EXISTS_AND_RECENT'; "+
			"else echo 'EXISTS_OLD'; fi; "+
			"else echo 'NOT_EXISTS'; fi",
		localNodeAgentPath, localNodeAgentPath)
	binStatus, _ := sshRunLocal(checkBinCmd)
	if strings.Contains(binStatus, "EXISTS_AND_RECENT") {
		sse.event(PhaseBuild, "done", "使用缓存的 node-agent 二进制(24小时内编译)", binStatus)
	} else {
		// 兜底: 编译增加重试 (网络波动导致 go mod download 失败)
		var buildErr error
		var buildOut string
		for retry := 1; retry <= 2; retry++ {
			if retry > 1 {
				sse.event(PhaseBuild, "log", "", fmt.Sprintf("编译失败, 第 %d 次重试...", retry))
				time.Sleep(3 * time.Second)
			}
			// 优化: 使用 alpine 镜像替代 bullseye, 下载速度提升 3-5 倍, 体积缩小 70%
			hostNodeAgentPath := getHostNodeAgentPath()
			compileCmd := fmt.Sprintf(
				"docker run --rm "+
					"-v %s:/build -w /build "+
					"golang:1.21-alpine "+
					"sh -c 'apk add --no-cache git >/dev/null 2>&1; go mod download && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags=\"-s -w\" -o /build/agent . 2>&1' && ls -lh %s/agent",
				hostNodeAgentPath, hostNodeAgentPath)
			buildOut, buildErr = sshRunLocal(compileCmd)
			if buildErr == nil {
				break
			}
			// 兜底: 编译失败可能是缓存问题, 清理 go mod cache 后重试
			if retry == 1 && (strings.Contains(buildOut, "download") || strings.Contains(buildOut, "verify")) {
				sse.event(PhaseBuild, "log", "", "检测到依赖下载问题, 清理模块缓存后重试...")
				cleanCacheCmd := fmt.Sprintf(
					"docker run --rm -v %s:/build -w /build golang:1.21-alpine sh -c 'go clean -modcache 2>/dev/null; true'",
					getHostNodeAgentPath())
				sshRunLocal(cleanCacheCmd)
			}
		}
		if buildErr != nil {
			sse.eventWithCode(PhaseBuild, "error", "二进制预编译失败(重试2次): "+buildErr.Error(), buildOut, DeployErrBuild)
			return false, DeployErrBuild, "编译失败: " + buildErr.Error()
		}
		sse.event(PhaseBuild, "done", "二进制预编译完成", buildOut)
	}

	// 推送二进制到节点 (兜底: 传输失败重试 2 次)
	var transferOut string
	var transferErr error
	for retry := 1; retry <= 2; retry++ {
		transferOut, transferErr = scpViaSSH(client, "/app/node_agent/agent", deployDir+"/agent")
		if transferErr == nil {
			break
		}
		if retry < 2 {
			sse.event(PhaseBuild, "log", "", fmt.Sprintf("传输失败(第%d次), 重试中... (%v)", retry, transferErr))
			// 兜底: 清理远程可能残留的半截文件
			sshRun(client, "rm -f "+deployDir+"/agent 2>/dev/null; true")
			time.Sleep(2 * time.Second)
		}
	}
	if transferErr != nil {
		sse.eventWithCode(PhaseBuild, "error", "传输失败(重试2次): "+transferErr.Error(), transferOut, DeployErrTransfer)
		return false, DeployErrTransfer, "传输失败: " + transferErr.Error()
	}

	// 兜底: 传输完成后验证文件完整性
	verifyBin, _ := sshRun(client, fmt.Sprintf("ls -la %s/agent 2>/dev/null && file %s/agent 2>/dev/null || echo 'VERIFY_FAIL'", deployDir, deployDir))
	if strings.Contains(verifyBin, "VERIFY_FAIL") || strings.Contains(verifyBin, "No such file") {
		sse.eventWithCode(PhaseBuild, "error", "传输后文件验证失败, agent 二进制不存在", verifyBin, DeployErrTransfer)
		return false, DeployErrTransfer, "二进制文件验证失败"
	}
	// 兜底: 检查是否为有效的 ELF 文件
	if !strings.Contains(verifyBin, "ELF") && !strings.Contains(verifyBin, "executable") {
		sse.eventWithCode(PhaseBuild, "error", "传输后文件验证失败, 非有效可执行文件", verifyBin, DeployErrTransfer)
		return false, DeployErrTransfer, "二进制文件类型异常"
	}

	if _, err := sshRun(client, "chmod +x "+deployDir+"/agent"); err != nil {
		// 兜底: chmod 失败重试一次
		_, err = sshRun(client, "chmod +x "+deployDir+"/agent")
		if err != nil {
			sse.eventWithCode(PhaseBuild, "error", "chmod agent 失败: "+err.Error(), "", DeployErrTransfer)
			return false, DeployErrTransfer, "chmod agent 失败: " + err.Error()
		}
	}

	// ============================================================
	// Phase 5: 启动服务
	// ============================================================
	sse.event(PhaseStart, "running", "构建镜像并启动 "+containerName+"...", "")

	// 兜底: docker compose 启动增加重试 (镜像拉取超时、网络波动)
	var startOut string
	var startErr error
	for retry := 1; retry <= 2; retry++ {
		if retry > 1 {
			sse.event(PhaseStart, "log", "", fmt.Sprintf("启动失败, 第 %d 次重试 (先清理旧容器)...", retry))
			// 兜底: 清理失败的容器和网络残留
			sshRun(client, fmt.Sprintf("cd %s 2>/dev/null && docker compose -f docker-compose.node.yml --env-file .env.node down --timeout 5 2>/dev/null; docker rm -f %s 2>/dev/null; true", deployDir, containerName))
			time.Sleep(3 * time.Second)
		}
		startOut, startErr = sshStream(client, fmt.Sprintf(
			"cd %s && docker compose -f docker-compose.node.yml --env-file .env.node up -d --build 2>&1",
			deployDir), func(line string) {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				return
			}
			if strings.Contains(trimmed, "DONE") ||
				strings.Contains(trimmed, "ERROR") ||
				strings.Contains(trimmed, "error") ||
				strings.Contains(trimmed, "Building") ||
				strings.Contains(trimmed, "Built") ||
				strings.Contains(trimmed, "Started") ||
				strings.Contains(trimmed, "Starting") ||
				strings.Contains(trimmed, "Creating") ||
				strings.Contains(trimmed, "Created") ||
				strings.Contains(trimmed, "Container") ||
				strings.Contains(trimmed, "Pulling") ||
				strings.Contains(trimmed, "Pulled") ||
				strings.Contains(trimmed, "Download") ||
				strings.Contains(trimmed, "Extracting") {
				sse.event(PhaseStart, "log", "", line)
			}
		})
		if startErr == nil {
			break
		}
	}

	if startErr != nil {
		// 兜底: docker compose 失败后尝试诊断原因
		diagOut, _ := sshRun(client, fmt.Sprintf("cd %s 2>/dev/null && docker compose -f docker-compose.node.yml --env-file .env.node config 2>&1 | tail -20; echo '---'; docker ps -a 2>&1 | head -10", deployDir))
		sse.eventWithCode(PhaseStart, "error", "启动失败(重试2次): "+startErr.Error(), startOut+"\n\n诊断信息:\n"+diagOut, DeployErrStart)
		return false, DeployErrStart, "启动失败: " + startErr.Error()
	}
	sse.event(PhaseStart, "done", "node-agent 容器已启动", startOut)

	// ============================================================
	// Phase 6: 验证完成
	// ============================================================
	sse.event(PhaseVerify, "running", "检测容器运行状态和日志...", "")

	// 兜底: 容器可能还在启动中, 等待更长一点
	time.Sleep(3 * time.Second)

	diagResult := diagnoseContainerStartup(client, containerName, listenPort, healthPort)
	if diagResult.fatal {
		// 兜底: 容器启动失败时尝试获取更详细的诊断
		fullLogs, _ := sshRun(client, fmt.Sprintf("docker logs --tail 100 %s 2>&1; echo '---'; docker inspect %s 2>&1 | head -30", containerName, containerName))
		sse.eventWithCode(PhaseVerify, "error", diagResult.summary, diagResult.output+"\n\n完整诊断:\n"+fullLogs, DeployErrStart)
		return false, DeployErrStart, diagResult.summary
	}
	if diagResult.hasWarning {
		sse.event(PhaseVerify, "warning", diagResult.summary, diagResult.output)
	} else {
		sse.event(PhaseVerify, "done", diagResult.summary, diagResult.output)
	}

	// 等待节点注册到面板
	// 修复 NODE-VERIFY-01 (P1): 旧版 12×3s=36s 验证窗口太短。
	//   Xray-core 二进制从 GitHub release 下载(几 MB~几十 MB), 境外节点或网络抖动时
	//   >36s 是常见的, 会被误判为 VERIFY_FAIL。延长到 40×3s=120s 覆盖下载场景。
	//   同时 agent bootstrap 有 30 次重试(150s), 120s 窗口能覆盖首次注册成功。
	sse.event(PhaseVerify, "running", "等待节点注册到面板(最多 120 秒, 含 Xray 下载)...", "")
	var verifyOut string
	var success bool
	// 兜底: 指数退避, 前 10 次间隔 2s, 后 30 次间隔 3s (总计 20s+90s=110s)
	for i := 0; i < 40; i++ {
		if i < 10 {
			time.Sleep(2 * time.Second)
		} else {
			time.Sleep(3 * time.Second)
		}
		// 兜底: docker logs 可能失败, 忽略错误
		verifyOut, _ = sshRun(client, fmt.Sprintf("docker logs --tail 50 %s 2>&1 || echo 'LOGS_UNAVAILABLE'", containerName))
		if strings.Contains(verifyOut, "LOGS_UNAVAILABLE") {
			// 容器可能已崩溃, 检查状态
			statusOut, _ := sshRun(client, fmt.Sprintf("docker ps -a --filter name=%s --format '{{.Status}}' 2>/dev/null || echo 'STATUS_UNAVAILABLE'", containerName))
			if strings.Contains(statusOut, "Exited") || strings.Contains(statusOut, "STATUS_UNAVAILABLE") {
				break
			}
			continue
		}
		if strings.Contains(verifyOut, "注册成功") || strings.Contains(verifyOut, "已注册到面板") || strings.Contains(verifyOut, "Xray 已启动") || strings.Contains(verifyOut, "Xray 进程已启动") {
			success = true
			break
		}
		if strings.Contains(verifyOut, "注册失败") || strings.Contains(verifyOut, "token 无效") || strings.Contains(verifyOut, "Unauthenticated") {
			break
		}
	}
	if success {
		// 兜底: 端口检测多种工具降级
		portCheck, _ := sshRun(client, fmt.Sprintf(
			"(ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null || lsof -i -P -n 2>/dev/null | grep LISTEN) | grep -E ':%d|:%d' || echo 'PORT_CHECK_UNAVAILABLE'",
			listenPort, healthPort))
		sse.event(PhaseVerify, "done", "节点已成功连接面板！代理端口 "+strconv.Itoa(listenPort)+" 正在监听", verifyOut+"\n\n端口监听状态:\n"+portCheck)
		return true, "", ""
	}

	// 智能分析失败原因
	diag := analyzeLogs(verifyOut)
	sse.eventWithCode(PhaseVerify, "warning", "节点容器已启动但未注册成功: "+diag.summary, verifyOut+"\n\n修复建议:\n"+diag.fixSuggestion, DeployErrVerify)
	return false, DeployErrVerify, diag.summary
}

// ============================================================
// preDeployCheck 部署前环境检测 (磁盘/内存/Docker/端口/网络)
// ============================================================

type preCheckResult struct {
	OK            bool
	Fatal         bool
	Reason        string
	Output        string
	FixSuggestion string
	ErrCode       string
}

// preDeployCheck 在部署前检测关键环境 (逐步推送 SSE 事件)
// 检测项: 磁盘空间(>=1G可用) / 内存(>=512M可用) / Docker / 端口冲突 / 网络(curl github.com)
// 每项均有兜底: 命令失败/不存在/超时等异常情况均有降级方案
func preDeployCheck(client *ssh.Client, listenPort, healthPort int, sse *sseWriter) preCheckResult {
	var lines []string
	result := preCheckResult{OK: true}

	// 1. 磁盘空间检测
	sse.event(PhaseEnvCheck, "running", "正在检查磁盘空间...", "")
	diskOut, diskErr := sshRun(client, "df -BG / 2>/dev/null | tail -1 || echo 'DF_FAILED'")
	// 兜底: df -BG 失败时尝试 df -h (某些精简系统不支持 -BG)
	if strings.Contains(diskOut, "DF_FAILED") || (diskErr != nil && strings.TrimSpace(diskOut) == "") {
		sse.event(PhaseEnvCheck, "log", "", "df -BG 不可用, 降级使用 df -h...")
		diskOut, _ = sshRun(client, "df -h / 2>/dev/null | tail -1 || echo 'DF_UNAVAILABLE'")
	}
	lines = append(lines, "=== 磁盘空间 ===")
	lines = append(lines, diskOut)
	if strings.Contains(diskOut, "DF_UNAVAILABLE") {
		// 兜底: 连 df 都不可用, 跳过磁盘检测但给出警告
		lines = append(lines, "[警告] df 命令不可用, 跳过磁盘空间检测")
		sse.event(PhaseEnvCheck, "warning", "无法检测磁盘空间(df 命令不可用), 跳过此检查", diskOut)
	} else {
		availGB := parseAvailGB(diskOut)
		if availGB >= 0 && availGB < 1 {
			result.OK = false
			result.Fatal = true
			result.ErrCode = DeployErrDiskFull
			result.Reason = fmt.Sprintf("磁盘可用空间仅 %dGB, 不足以构建 Docker 镜像 (需要 >=1GB)", availGB)
			result.FixSuggestion = "1) 清理磁盘: docker system prune -a  2) 扩容磁盘  3) 清理大文件: du -sh /* | sort -h"
			result.Output = strings.Join(lines, "\n")
			return result
		}
		sse.event(PhaseEnvCheck, "log", fmt.Sprintf("磁盘空间充足 (可用 %dGB)", availGB), diskOut)
	}

	// 2. 内存检测 (自动创建 swap 兜底)
	sse.event(PhaseEnvCheck, "running", "正在检查内存...", "")
	memOut, memErr := sshRun(client, "free -m 2>/dev/null | head -2 || echo 'FREE_FAILED'")
	// 兜底: free -m 不可用时尝试 /proc/meminfo
	if strings.Contains(memOut, "FREE_FAILED") || (memErr != nil && strings.TrimSpace(memOut) == "") {
		sse.event(PhaseEnvCheck, "log", "", "free -m 不可用, 降级读取 /proc/meminfo...")
		memOut, _ = sshRun(client, "cat /proc/meminfo 2>/dev/null | head -3 || echo 'MEM_UNAVAILABLE'")
	}
	lines = append(lines, "=== 内存 ===")
	lines = append(lines, memOut)
	if strings.Contains(memOut, "MEM_UNAVAILABLE") {
		lines = append(lines, "[警告] 无法检测内存, 跳过此检查")
		sse.event(PhaseEnvCheck, "warning", "无法检测内存信息, 跳过此检查", memOut)
	} else {
		if availMB := parseAvailMB(memOut); availMB >= 0 && availMB < 128 {
			sse.event(PhaseEnvCheck, "warning", fmt.Sprintf("可用内存仅 %dMB, 低于 128MB 要求, 尝试自动创建 swap...", availMB), memOut)
			// 内存严重不足, 尝试自动创建 swap 分区 (需要磁盘 >=1G 可用)
			// 使用带时间戳的独立文件名, 避免多节点共享服务器冲突
			diskForSwap, _ := sshRun(client, "df -BG / 2>/dev/null | tail -1 || df -h / 2>/dev/null | tail -1 || echo '0G'")
			if availGB := parseAvailGB(diskForSwap); availGB >= 1 {
				swapFile := fmt.Sprintf("/swapfile_np_%d", time.Now().UnixNano()%100000)
				sse.event(PhaseEnvCheck, "running", fmt.Sprintf("磁盘可用 %dGB, 正在创建 1GB swap 分区 (%s)...", availGB, swapFile), "")
				var swapCreated bool
				var swapOut string
				for swapRetry := 1; swapRetry <= 2; swapRetry++ {
					if swapRetry > 1 {
						sse.event(PhaseEnvCheck, "log", "", fmt.Sprintf("swap 创建第 %d 次重试...", swapRetry))
						time.Sleep(2 * time.Second)
						swapFile = fmt.Sprintf("/swapfile_np_%d", time.Now().UnixNano()%100000)
					}
					swapCmd := fmt.Sprintf(
						"(fallocate -l 1G %s 2>/dev/null || dd if=/dev/zero of=%s bs=1M count=1024 2>/dev/null) && chmod 600 %s && mkswap %s && swapon %s && echo 'SWAP_CREATED'",
						swapFile, swapFile, swapFile, swapFile, swapFile,
					)
					swapOut, _ = sshRun(client, swapCmd)
					if strings.Contains(swapOut, "SWAP_CREATED") {
						swapCreated = true
						break
					}
				}
				lines = append(lines, swapOut)
				if swapCreated {
					sse.event(PhaseEnvCheck, "done", fmt.Sprintf("swap 创建成功 (%s), 重新检测内存...", swapFile), swapOut)
					memOut2, _ := sshRun(client, "free -m 2>/dev/null | head -2 || cat /proc/meminfo 2>/dev/null | head -3")
					lines = append(lines, memOut2)
					availMB2 := parseAvailMB(memOut2)
					if availMB2 >= 128 {
						sse.event(PhaseEnvCheck, "done", fmt.Sprintf("内存达标, swap 生效后可用 %dMB >= 128MB", availMB2), memOut2)
						lines = append(lines, fmt.Sprintf("[通过] swap 生效, 可用内存 %dMB >= 128MB", availMB2))
					} else {
						lines = append(lines, fmt.Sprintf("[警告] swap 生效后仍仅 %dMB, 但继续尝试部署", availMB2))
						sse.event(PhaseEnvCheck, "warning", fmt.Sprintf("内存仍偏低(%dMB), 但继续部署", availMB2), memOut2)
					}
				} else {
					lines = append(lines, "[警告] swap 创建失败, 但继续尝试部署")
					sse.event(PhaseEnvCheck, "warning", fmt.Sprintf("swap 创建失败, 当前可用内存 %dMB, 继续部署", availMB), swapOut)
				}
			} else {
				lines = append(lines, fmt.Sprintf("[警告] 磁盘空间不足(%dGB), 无法创建 swap, 继续尝试部署", availGB))
				sse.event(PhaseEnvCheck, "warning", fmt.Sprintf("内存仅 %dMB 且磁盘不足无法创建 swap, 继续部署", availMB), diskForSwap)
			}
		} else if availMB := parseAvailMB(memOut); availMB >= 128 && availMB < 256 {
			// 128-256MB: 偏低但不阻断, 仅警告
			sse.event(PhaseEnvCheck, "warning", fmt.Sprintf("可用内存偏低(%dMB), 但不影响部署", availMB), memOut)
			lines = append(lines, fmt.Sprintf("[警告] 可用内存 %dMB 偏低, 但仍满足最低要求", availMB))
		} else {
			sse.event(PhaseEnvCheck, "log", fmt.Sprintf("内存充足 (可用 %dMB)", availMB), memOut)
		}
	}

	// 3. Docker 检测
	sse.event(PhaseEnvCheck, "running", "正在检查 Docker...", "")
	dockerStatus, _ := sshRun(client, "command -v docker >/dev/null 2>&1 && timeout 10 docker info 2>&1 | head -3 || echo 'NOT_INSTALLED'")
	// 兜底: docker info 可能挂死, timeout 10 秒保护; 也可能 Docker 二进制存在但 daemon 挂了
	if strings.Contains(dockerStatus, "NOT_INSTALLED") {
		// 兜底: 用多种方式查找 Docker (静态安装/源码编译可能不在 PATH)
		dockerStatus2, _ := sshRun(client, "which docker 2>/dev/null || type docker 2>/dev/null || ls /usr/bin/docker 2>/dev/null || ls /usr/local/bin/docker 2>/dev/null || echo 'NOT_FOUND'")
		if strings.Contains(dockerStatus2, "NOT_FOUND") {
			dockerStatus = "NOT_INSTALLED"
		} else {
			// 找到了 Docker 二进制, 再试一次 docker info (可能是 PATH 问题)
			dockerStatus, _ = sshRun(client, "timeout 15 docker info 2>&1 | head -5 || echo 'DOCKER_TIMEOUT'")
		}
	}
	lines = append(lines, "=== Docker ===")
	lines = append(lines, dockerStatus)
	dockerInstalled := !strings.Contains(dockerStatus, "NOT_INSTALLED") && !strings.Contains(dockerStatus, "NOT_FOUND")
	dockerRunning := strings.Contains(dockerStatus, "Server Version") || strings.Contains(dockerStatus, "Containers")
	dockerTimeout := strings.Contains(dockerStatus, "DOCKER_TIMEOUT")

	if dockerTimeout {
		// 兜底: Docker 守护进程可能卡住了
		sse.event(PhaseEnvCheck, "warning", "Docker 响应超时, 尝试重启 dockerd...", dockerStatus)
		lines = append(lines, "[警告] Docker 响应超时, 正在尝试重启...")
		restartOut, _ := sshRun(client, "systemctl restart docker 2>&1 || service docker restart 2>&1; sleep 3; timeout 10 docker info 2>&1 | head -3 || echo 'STILL_TIMEOUT'")
		lines = append(lines, restartOut)
		if strings.Contains(restartOut, "STILL_TIMEOUT") {
			kernelVer, _ := sshRun(client, "uname -r 2>/dev/null | tr -d '\\n'")
			result.OK = false
			result.Fatal = true
			result.ErrCode = DeployErrDockerNotInstalled
			result.Reason = fmt.Sprintf("Docker 守护进程无响应 (内核 %s), 重启后仍超时", strings.TrimSpace(kernelVer))
			result.FixSuggestion = "1) 检查内核是否支持: lsmod | grep overlay && mount | grep cgroup\n2) 查看 dockerd 日志: tail -50 /var/log/dockerd.log\n3) 低配 VPS 可能内核裁剪过, 不支持 Docker, 请更换支持 Docker 的系统镜像"
			result.Output = strings.Join(lines, "\n")
			return result
		}
		dockerRunning = strings.Contains(restartOut, "Server Version") || strings.Contains(restartOut, "Containers")
		sse.event(PhaseEnvCheck, "done", "Docker 已重启恢复", restartOut)
	} else if !dockerInstalled {
		sse.event(PhaseEnvCheck, "log", "Docker 未安装, 将在准备阶段自动安装", dockerStatus)
		lines = append(lines, "[提示] Docker 未安装, 将在准备阶段自动安装")
	} else if !dockerRunning {
		sse.event(PhaseEnvCheck, "warning", "Docker 已安装但未运行, 正在尝试启动...", dockerStatus)
		lines = append(lines, "[警告] Docker 已安装但未运行, 正在尝试启动...")
		// 兜底: 尝试 systemctl 和 service 两种启动方式
		startOut, _ := sshRun(client, "systemctl start docker 2>&1 || service docker start 2>&1; sleep 2; systemctl enable docker 2>&1 || true; timeout 10 docker info 2>&1 | head -3 || echo 'START_FAILED'")
		lines = append(lines, startOut)
		if !strings.Contains(startOut, "Server Version") && !strings.Contains(startOut, "Containers") {
			result.OK = false
			result.Fatal = false
			result.ErrCode = DeployErrDockerNotInstalled
			result.Reason = "Docker 已安装但启动失败, 将在准备阶段重试"
			result.FixSuggestion = "1) 检查 /var/log/dockerd.log  2) 确认内核支持: lsmod | grep overlay  3) 面板将自动重试启动"
		} else {
			sse.event(PhaseEnvCheck, "done", "Docker 已成功启动", startOut)
			lines = append(lines, "[恢复] Docker 已成功启动, 继续部署")
		}
	} else {
		sse.event(PhaseEnvCheck, "log", "Docker 运行正常", dockerStatus)
	}

	// 4. 端口冲突检测
	sse.event(PhaseEnvCheck, "running", fmt.Sprintf("正在检查端口 %d/%d...", listenPort, healthPort), "")
	// 兜底: ss → netstat → lsof → 跳过 (逐级降级)
	portCheck, _ := sshRun(client, fmt.Sprintf(
		"(ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null || lsof -i -P -n 2>/dev/null | grep LISTEN) | grep -E ':%d|:%d' || echo 'PORTS_AVAILABLE'",
		listenPort, healthPort))
	lines = append(lines, "=== 端口 ===")
	lines = append(lines, portCheck)
	if !strings.Contains(portCheck, "PORTS_AVAILABLE") && strings.TrimSpace(portCheck) != "" {
		result.OK = false
		result.Fatal = true
		result.ErrCode = DeployErrPortConflict
		result.Reason = fmt.Sprintf("端口 %d 或 %d 已被占用", listenPort, healthPort)
		result.FixSuggestion = fmt.Sprintf("1) 释放端口: fuser -k %d/tcp %d/tcp  2) 修改节点端口  3) 停止占用进程", listenPort, healthPort)
		result.Output = strings.Join(lines, "\n")
		return result
	}
	sse.event(PhaseEnvCheck, "log", "端口未被占用", portCheck)

	// 5. 网络检测 (是否能访问 Docker Hub / get.docker.com)
	sse.event(PhaseEnvCheck, "running", "正在检测网络连通性 (Docker Hub)...", "")
	// 兜底: curl → wget → ping 逐级降级检测网络
	netOut, _ := sshRun(client, "curl -sI --max-time 5 https://get.docker.com 2>&1 | head -1; echo '---'; curl -sI --max-time 5 https://registry-1.docker.io 2>&1 | head -1")
	if !strings.Contains(netOut, "HTTP/") {
		// 兜底: curl 不可用或网络不通, 尝试 wget
		sse.event(PhaseEnvCheck, "log", "", "curl 检测失败, 降级使用 wget...")
		netOut2, _ := sshRun(client, "wget --spider --timeout=5 https://get.docker.com 2>&1 | head -3; echo '---'; wget --spider --timeout=5 https://registry-1.docker.io 2>&1 | head -3")
		netOut = netOut + "\n" + netOut2
		if !strings.Contains(netOut2, "200") && !strings.Contains(netOut2, "OK") && !strings.Contains(netOut2, "exists") {
			// 兜底: wget 也失败, 尝试 ping 判断基础网络
			netOut3, _ := sshRun(client, "ping -c 2 -W 3 8.8.8.8 2>&1 || echo 'PING_FAILED'")
			netOut = netOut + "\n" + netOut3
			lines = append(lines, "=== 网络 ===")
			lines = append(lines, netOut)
			if strings.Contains(netOut3, "PING_FAILED") || strings.Contains(netOut3, "100% packet loss") {
				lines = append(lines, "[严重] 服务器完全无网络连接!")
				result.OK = false
				result.Fatal = true
				result.ErrCode = DeployErrUnknown
				result.Reason = "服务器网络完全不通, 无法继续部署"
				result.FixSuggestion = "1) 检查服务器网络配置  2) 检查 DNS: cat /etc/resolv.conf  3) 检查网关: ip route"
				result.Output = strings.Join(lines, "\n")
				return result
			}
			lines = append(lines, "[警告] 基础网络正常但无法访问 Docker 仓库, 自动安装可能失败, 将使用国内镜像源回退")
			result.Reason = "网络受限, Docker 自动安装可能失败, 将尝试国内镜像源"
			result.FixSuggestion = "1) 检查防火墙  2) 配置代理  3) 手动安装 Docker 后重试"
			result.ErrCode = DeployErrUnknown
			result.OK = false
			sse.event(PhaseEnvCheck, "warning", "无法访问 Docker 仓库, 将尝试国内镜像源回退", netOut)
		} else {
			lines = append(lines, "=== 网络 ===")
			lines = append(lines, netOut)
			sse.event(PhaseEnvCheck, "log", "网络连通正常 (wget)", netOut)
		}
	} else {
		lines = append(lines, "=== 网络 ===")
		lines = append(lines, netOut)
		sse.event(PhaseEnvCheck, "log", "网络连通正常", netOut)
	}

	result.Output = strings.Join(lines, "\n")
	return result
}

// parseAvailGB 从 "df -BG" 输出解析可用空间 GB
func parseAvailGB(s string) int {
	// 格式: /dev/sda1  50G  20G  30G  40% /
	fields := strings.Fields(s)
	for i, f := range fields {
		if strings.HasSuffix(f, "G") {
			if v, err := strconv.Atoi(strings.TrimSuffix(f, "G")); err == nil {
				// 通常第 4 列是可用空间 (Filesystem Size Used Avail Use% Mounted)
				if i == 3 {
					return v
				}
			}
		}
	}
	return -1
}

// parseAvailMB 从 "free -m" 解析可用内存 MB
func parseAvailMB(s string) int {
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Mem:") || strings.HasPrefix(line, "      ") {
			fields := strings.Fields(line)
			// 格式: total used free shared buff/cache available
			if len(fields) >= 7 {
				// available 是最后一列
				if v, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
					return v
				}
			} else if len(fields) >= 3 && fields[0] == "Mem:" {
				if v, err := strconv.Atoi(fields[3]); err == nil {
					return v
				}
			}
		}
	}
	return -1
}

// dockerDiag 保存 Docker 启动失败诊断结果
type dockerDiag struct {
	fatal  bool   // true=内核不支持, 无需重试; false=可能是临时问题
	reason string // 人类可读的诊断原因
}

// diagnoseDockerFailure 全面诊断 Docker 无法启动的原因
// 检测: cgroup 支持 (v1/v2), overlay 模块, 内核版本, dockerd 日志
func diagnoseDockerFailure(client *ssh.Client, sse *sseWriter) dockerDiag {
	kernelVer, _ := sshRun(client, "uname -r 2>/dev/null | tr -d '\\n'")
	sse.event(PhasePrepare, "log", "", fmt.Sprintf("=== Docker 启动失败诊断 (内核: %s) ===", strings.TrimSpace(kernelVer)))

	// 1. 检测 cgroup 挂载 (v1 和 v2)
	cgroupV1, _ := sshRun(client, "mount 2>/dev/null | grep 'cgroup ' | head -3 || echo 'NO_CGROUP_V1'")
	cgroupV2, _ := sshRun(client, "mount 2>/dev/null | grep 'cgroup2 ' | head -1 || echo 'NO_CGROUP_V2'")
	hasCgroupV1 := !strings.Contains(cgroupV1, "NO_CGROUP_V1") && strings.TrimSpace(cgroupV1) != ""
	hasCgroupV2 := !strings.Contains(cgroupV2, "NO_CGROUP_V2") && strings.TrimSpace(cgroupV2) != ""

	if hasCgroupV1 {
		sse.event(PhasePrepare, "log", "", "  cgroup v1: 已挂载")
	} else {
		sse.event(PhasePrepare, "log", "", "  cgroup v1: 未挂载")
	}
	if hasCgroupV2 {
		sse.event(PhasePrepare, "log", "", "  cgroup v2: 已挂载")
	} else {
		sse.event(PhasePrepare, "log", "", "  cgroup v2: 未挂载")
	}

	// 2. 检测 overlay 模块 (Docker 存储驱动必须)
	overlayCheck, _ := sshRun(client, "lsmod 2>/dev/null | grep overlay || modprobe overlay 2>&1 || echo 'OVERLAY_MISSING'")
	overlayOK := !strings.Contains(overlayCheck, "OVERLAY_MISSING") && !strings.Contains(overlayCheck, "not found")
	if overlayOK {
		sse.event(PhasePrepare, "log", "", "  overlay 模块: 可用")
	} else {
		sse.event(PhasePrepare, "log", "", "  overlay 模块: 缺失 (Docker 存储驱动依赖此模块)")
	}

	// 3. 检测内核配置中是否启用 cgroup (检查 /proc/filesystems 和 /proc/cgroups)
	cgroupFilesystems, _ := sshRun(client, "cat /proc/filesystems 2>/dev/null | grep cgroup || echo 'NO_CGROUP_FS'")
	cgroupProcs, _ := sshRun(client, "cat /proc/cgroups 2>/dev/null | head -5 || echo 'NO_PROCCGROUPS'")
	sse.event(PhasePrepare, "log", "", fmt.Sprintf("  /proc/filesystems cgroup: %s", strings.TrimSpace(strings.ReplaceAll(cgroupFilesystems, "\n", ", "))))
	sse.event(PhasePrepare, "log", "", fmt.Sprintf("  /proc/cgroups: %s", strings.TrimSpace(strings.ReplaceAll(cgroupProcs, "\n", "; "))))

	// 4. 读取 dockerd 日志最后几行
	dockerLog, _ := sshRun(client, "tail -30 /var/log/dockerd.log 2>/dev/null || journalctl -u docker -n 20 --no-pager 2>/dev/null || echo 'NO_LOG'")
	sse.event(PhasePrepare, "log", "", fmt.Sprintf("  dockerd 日志: %s", strings.TrimSpace(dockerLog)))

	// 5. 判断是否致命 (内核不支持, 重装/重试都没用)
	if !hasCgroupV1 && !hasCgroupV2 {
		// 完全不支持 cgroup → 致命
		return dockerDiag{
			fatal:  true,
			reason: fmt.Sprintf("系统内核 (%s) 不支持 cgroup (v1 和 v2 均未挂载), Docker 无法在此系统运行。请更换支持 Docker 的系统镜像 (如 Ubuntu 20.04+, Debian 11+, CentOS 7+)", strings.TrimSpace(kernelVer)),
		}
	}
	if !overlayOK {
		// 缺少 overlay 模块 → 致命 (可以用 vfs 驱动但性能极差, 不推荐)
		return dockerDiag{
			fatal:  true,
			reason: fmt.Sprintf("系统内核 (%s) 缺少 overlay 模块, Docker 存储驱动无法工作。请确保内核编译了 overlay 模块, 或更换支持 Docker 的系统镜像", strings.TrimSpace(kernelVer)),
		}
	}

	// 6. 分析 dockerd 日志中的具体错误
	if strings.Contains(dockerLog, "cgroup") && (strings.Contains(dockerLog, "not found") || strings.Contains(dockerLog, "no such file")) {
		return dockerDiag{
			fatal:  true,
			reason: fmt.Sprintf("dockerd 报告 cgroup 子系统缺失 (内核 %s)。这是低配 VPS 常见问题: 内核裁剪掉了 cgroup 支持。请更换系统镜像", strings.TrimSpace(kernelVer)),
		}
	}
	if strings.Contains(dockerLog, "overlay") && (strings.Contains(dockerLog, "not supported") || strings.Contains(dockerLog, "permission denied")) {
		return dockerDiag{
			fatal:  true,
			reason: fmt.Sprintf("dockerd 报告 overlay 存储驱动不支持 (内核 %s)。可能缺少 overlay 内核模块", strings.TrimSpace(kernelVer)),
		}
	}
	if strings.Contains(dockerLog, "iptables") || strings.Contains(dockerLog, "nat") {
		return dockerDiag{
			fatal:  true,
			reason: fmt.Sprintf("dockerd 报告 iptables/nat 不可用 (内核 %s)。可能内核缺少 netfilter 模块", strings.TrimSpace(kernelVer)),
		}
	}

	// 非致命: cgroup 和 overlay 都有, 但 dockerd 还是启动失败 (可能是端口冲突/配置损坏等)
	return dockerDiag{
		fatal:  false,
		reason: fmt.Sprintf("内核兼容性检查通过 (cgroup+overlay 均可用), 但 dockerd 仍无法启动。请查看完整日志排查"),
	}
}

// ensureDocker 检查并安装 Docker; 已安装则跳过
// 兜底: get.docker.com 不可用时自动回退到阿里云镜像源安装
// 修复: 已安装但启动失败时, 先做全面内核诊断, 如果内核不支持则直接报错不重试;
//
//	如果是系统重装后二进制残留, 卸载后重新安装
func ensureDocker(client *ssh.Client, sse *sseWriter) (bool, string, string) {
	// 检测 Docker
	checkOut, _ := sshRun(client, "command -v docker >/dev/null 2>&1 && echo 'INSTALLED' || echo 'NOT_INSTALLED'")
	if strings.Contains(checkOut, "INSTALLED") {
		// 已安装, 检测是否运行
		// 兜底: timeout 防止 docker info 卡死
		runOut, _ := sshRun(client, "timeout 10 docker info 2>&1 | head -3 || echo 'TIMEOUT'")
		if strings.Contains(runOut, "Server Version") {
			sse.event(PhasePrepare, "done", "Docker 已安装并运行", runOut)
			return true, "", ""
		}
		// 兜底: docker info 超时, 尝试重启 dockerd
		if strings.Contains(runOut, "TIMEOUT") {
			sse.event(PhasePrepare, "log", "", "Docker 守护进程超时, 尝试重启...")
			sshRun(client, "systemctl restart docker 2>&1 || service docker restart 2>&1; sleep 3; true")
		}
		// 启动 Docker
		sse.event(PhasePrepare, "log", "", "Docker 已安装, 尝试启动...")
		// 修复: 静态安装的 systemd 可能指向错误的 dockerd 路径
		sshRun(client, "if systemctl cat docker 2>/dev/null | grep -q 'ExecStart=/usr/bin/dockerd' "+
			"&& [ ! -x /usr/bin/dockerd ] && [ -x /usr/local/bin/dockerd ]; then "+
			"sed -i 's|ExecStart=/usr/bin/dockerd|ExecStart=/usr/local/bin/dockerd|g' "+
			"/lib/systemd/system/docker.service && systemctl daemon-reload; fi; true")
		sshRun(client, "systemctl start docker 2>&1 || service docker start 2>&1; systemctl enable docker 2>&1 || true")
		time.Sleep(3 * time.Second)
		runOut, _ = sshRun(client, "timeout 10 docker info 2>&1 | head -3 || echo 'START_FAILED'")
		if !strings.Contains(runOut, "Server Version") && !strings.Contains(runOut, "Containers") {
			// 兜底: systemctl/service 对静态安装(阿里云/中科大镜像)无效,
			// 尝试手动启动 dockerd
			sse.event(PhasePrepare, "log", "", "systemctl 启动失败, 尝试手动启动 dockerd (静态安装)...")
			// setsid 比 nohup 更可靠: 创建新会话, dockerd 完全独立于 SSH,
			// SSH 断连不会杀死 daemon
			sshRun(client, "pkill dockerd 2>/dev/null; rm -f /var/run/docker.pid /run/docker.pid; "+
				"setsid dockerd > /var/log/dockerd.log 2>&1 < /dev/null & sleep 3; true")
			for i := 0; i < 10; i++ {
				time.Sleep(2 * time.Second)
				vOut, _ := sshRun(client, "timeout 5 docker info 2>&1 | head -3")
				if strings.Contains(vOut, "Server Version") || strings.Contains(vOut, "Containers") {
					sse.event(PhasePrepare, "done", "Docker 手动启动成功", vOut)
					return true, "", ""
				}
			}
			// 兜底: 手动启动也失败, 再给一次机会等待更长时间
			sse.event(PhasePrepare, "log", "", "Docker 启动较慢, 再等 5 秒...")
			time.Sleep(5 * time.Second)
			runOut, _ = sshRun(client, "timeout 10 docker info 2>&1 | head -3 || echo 'START_FAILED'")
			if !strings.Contains(runOut, "Server Version") && !strings.Contains(runOut, "Containers") {
				// 启动彻底失败, 做全面内核兼容性诊断
				diag := diagnoseDockerFailure(client, sse)
				if diag.fatal {
					// 内核不支持 Docker, 重装/重试都没用
					return false, DeployErrDockerNotInstalled, diag.reason
				}
				// 可能是服务器重装后二进制残留但守护进程损坏, 尝试强制卸载后重新安装
				sse.event(PhasePrepare, "log", "", "Docker 启动失败(可能是系统重装后残留), 正在卸载旧版本并重新安装...")
				sshRun(client, "systemctl stop docker 2>&1; systemctl disable docker 2>&1; rm -f /usr/bin/docker /usr/local/bin/docker /usr/bin/dockerd /usr/local/bin/dockerd /usr/bin/docker-compose /usr/local/bin/docker-compose /usr/bin/containerd /usr/local/bin/containerd 2>&1; rm -rf /var/lib/docker /var/lib/containerd 2>&1; true")
				// 跳转到安装流程
			} else {
				sse.event(PhasePrepare, "done", "Docker 启动成功(等待后)", runOut)
				return true, "", ""
			}
		} else {
			sse.event(PhasePrepare, "done", "Docker 启动成功", runOut)
			return true, "", ""
		}
	}

	// 未安装, 自动安装 (兜底: 官方源 → 阿里云镜像 → 中科大镜像)
	sse.event(PhasePrepare, "log", "", "Docker 未安装, 正在自动安装 (约需 1-3 分钟)...")

	// 兜底: 先尝试检测哪个源可用
	_, mirrorErr := sshRun(client, "curl -sI --max-time 5 https://get.docker.com 2>&1 | head -1 | grep -q 'HTTP/' && echo 'OFFICIAL_OK' || echo 'OFFICIAL_FAIL'")

	var installCmd string
	if mirrorErr == nil {
		// 官方源可达
		sse.event(PhasePrepare, "log", "", "使用 Docker 官方源安装...")
		installCmd = "curl -fsSL https://get.docker.com | sh 2>&1 && systemctl enable docker && systemctl start docker && echo 'INSTALL_OK' || echo 'INSTALL_FAIL'"
	} else {
		// 兜底: 官方源不可达, 用阿里云镜像
		sse.event(PhasePrepare, "log", "", "Docker 官方源不可达, 回退使用阿里云镜像源...")
		installCmd = "curl -fsSL https://mirrors.aliyun.com/docker-ce/linux/static/stable/x86_64/docker-24.0.7.tgz -o /tmp/docker.tgz 2>&1 && " +
			"tar -xzf /tmp/docker.tgz -C /usr/local/bin/ --strip-components=1 2>&1 && " +
			"rm -f /tmp/docker.tgz && " +
			"curl -fsSL https://mirrors.aliyun.com/docker-ce/linux/static/stable/x86_64/docker-compose-linux-x86_64 -o /usr/local/bin/docker-compose 2>&1 && " +
			"chmod +x /usr/local/bin/docker-compose && " +
			"echo 'INSTALL_OK' || echo 'INSTALL_FAIL'"
	}

	out, installErr := sshStream(client, installCmd, func(line string) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			return
		}
		sse.event(PhasePrepare, "log", "", trimmed)
	})

	if !strings.Contains(out, "INSTALL_OK") {
		// 兜底: 阿里云也失败, 尝试中科大镜像
		if installErr != nil || strings.Contains(out, "INSTALL_FAIL") {
			sse.event(PhasePrepare, "log", "", "阿里云镜像源也失败, 尝试中科大镜像源...")
			fallbackCmd := "curl -fsSL https://mirrors.ustc.edu.cn/docker-ce/linux/static/stable/x86_64/docker-24.0.7.tgz -o /tmp/docker.tgz 2>&1 && " +
				"tar -xzf /tmp/docker.tgz -C /usr/local/bin/ --strip-components=1 2>&1 && " +
				"rm -f /tmp/docker.tgz && " +
				"echo 'INSTALL_OK' || echo 'INSTALL_FAIL'"
			out2, _ := sshStream(client, fallbackCmd, func(line string) {
				if trimmed := strings.TrimSpace(line); trimmed != "" {
					sse.event(PhasePrepare, "log", "", trimmed)
				}
			})
			if strings.Contains(out2, "INSTALL_OK") {
				// 手动启动 Docker daemon (setsid 确保独立于 SSH 会话)
				sshRun(client, "pkill dockerd 2>/dev/null; rm -f /var/run/docker.pid /run/docker.pid; "+
					"setsid dockerd > /var/log/dockerd.log 2>&1 < /dev/null & sleep 3; true")
				sse.event(PhasePrepare, "done", "Docker 安装完成 (中科大镜像源)", "")
				return true, "", ""
			}
		}
		return false, DeployErrDockerNotInstalled, "Docker 自动安装失败(已尝试官方/阿里云/中科大三个源)"
	}

	// 兜底: 检查安装后 Docker daemon 是否需要手动启动
	// 修复: 静态安装(阿里云/中科大)把 dockerd 放到 /usr/local/bin/,
	// 但 systemd docker.service 可能指向 /usr/bin/dockerd, 导致 service 启动失败。
	// 统一修正路径后优先尝试 systemctl, 不行再用 nohup 兜底。
	sshRun(client, "if systemctl cat docker 2>/dev/null | grep -q 'ExecStart=/usr/bin/dockerd' "+
		"&& [ ! -x /usr/bin/dockerd ] && [ -x /usr/local/bin/dockerd ]; then "+
		"sed -i 's|ExecStart=/usr/bin/dockerd|ExecStart=/usr/local/bin/dockerd|g' "+
		"/lib/systemd/system/docker.service && systemctl daemon-reload; fi; true")
	time.Sleep(1 * time.Second)
	verifyOut, _ := sshRun(client, "docker info 2>&1 | head -3 || echo 'DAEMON_NOT_RUNNING'")
	if strings.Contains(verifyOut, "DAEMON_NOT_RUNNING") || strings.Contains(verifyOut, "Cannot connect") {
		// systemctl 方式启动(优先, 让 systemd 管理生命周期, 避免 nohup 孤儿进程)
		startedWithSystemd := false
		sysOut, _ := sshRun(client, "systemctl start docker 2>&1 && sleep 2 && timeout 5 docker info 2>&1 | head -3")
		if strings.Contains(sysOut, "Server Version") || strings.Contains(sysOut, "Containers") {
			startedWithSystemd = true
			sse.event(PhasePrepare, "done", "Docker 通过 systemd 启动成功", "")
		}
		if !startedWithSystemd {
			// 静态安装方式兜底: setsid 手动启动 daemon (独立于 SSH 会话)
			sshRun(client, "pkill dockerd 2>/dev/null; rm -f /var/run/docker.pid /run/docker.pid; "+
				"setsid dockerd > /var/log/dockerd.log 2>&1 < /dev/null & sleep 3; true")
			// 等待 dockerd 启动 (最多等 20 秒)
			dockerStarted := false
			for i := 0; i < 10; i++ {
				time.Sleep(2 * time.Second)
				vOut, _ := sshRun(client, "docker info 2>&1 | head -3")
				if strings.Contains(vOut, "Server Version") || strings.Contains(vOut, "Containers") {
					dockerStarted = true
					break
				}
			}
			if !dockerStarted {
				// dockerd 启动失败, 做全面内核兼容性诊断
				diag := diagnoseDockerFailure(client, sse)
				return false, DeployErrDockerNotInstalled, fmt.Sprintf("Docker 已安装但 daemon 无法启动 - %s", diag.reason)
			}
		}
	}

	sse.event(PhasePrepare, "done", "Docker 自动安装完成", "")
	return true, "", ""
}

// verifyDockerReady 二次验证 Docker daemon 是否真正可用。
// 修复背景:
//  1. 静态安装(USTC/阿里云)后 dockerd 启动慢, socket 未就绪
//  2. 服务器重启后 systemd docker.service 在 containerd 就绪前启动 → 失败
//  3. 低配 VPS(1GB 内存) docker info 首次调用可能耗时 10-15 秒
//
// 策略: 先确认 containerd 存活(若死亡则尝试拉起), 然后轮询 docker info
// 最多 30 秒(6 次 x 5s)。若超时, 尝试启动 dockerd 后继续轮询。
func verifyDockerReady(client *ssh.Client, sse *sseWriter) bool {
	// 先检查 containerd (docker daemon 依赖 containerd)
	containerdOut, _ := sshRun(client, "ps aux 2>/dev/null | grep -v grep | grep containerd | head -1 || echo 'CONTAINERD_DEAD'")
	if strings.Contains(containerdOut, "CONTAINERD_DEAD") {
		sse.event(PhasePrepare, "log", "", "containerd 未运行, 尝试启动...")
		sshRun(client, "systemctl start containerd 2>/dev/null || (setsid containerd > /var/log/containerd.log 2>&1 < /dev/null &); sleep 3; true")
	}

	// 轮询 docker info, 最多 6 次(每次 5s = 30s, 覆盖低配 VPS 场景)
	// 修复: 此前用 head -3 截断 "Cannot connect to Docker daemon" — docker info 在
	// daemon 不可用时先输出 Client 段(3-6 行)才输出 "Cannot connect",
	// head -3 恰好在错误信息之前截断, 误判为"其他错误"反复杀进程, 而非走等待重试。
	// 改为 grep 精准匹配: 只取 "Server:" 或 "Cannot connect" 行, 不受行数截断影响。
	for i := 0; i < 6; i++ {
		out, _ := sshRun(client, "timeout 5 docker info 2>&1 | grep -E '^(Server:|Cannot connect)' | head -1")
		if strings.HasPrefix(strings.TrimSpace(out), "Server:") {
			return true
		}
		if strings.Contains(out, "Cannot connect") || out == "" {
			elapsed := (i + 1) * 5
			sse.event(PhasePrepare, "log", "", fmt.Sprintf("等待 Docker daemon 就绪... (%ds/%ds)", elapsed, 30))
			time.Sleep(5 * time.Second)
			continue
		}
		// docker 命令不可用(未安装)
		if strings.Contains(out, "not found") || strings.Contains(out, "command not found") {
			return false
		}
		// 其他错误: systemctl 状态异常, 尝试修复
		sse.event(PhasePrepare, "log", "", fmt.Sprintf("Docker 异常(尝试修复): %s", strings.TrimSpace(out)))
		sshRun(client, "systemctl stop docker 2>/dev/null; "+
			"pkill -9 dockerd 2>/dev/null; "+
			"rm -f /var/run/docker.pid /run/docker.pid /var/run/docker.sock /run/docker.sock; "+
			"sleep 2; "+
			"systemctl start docker 2>/dev/null || "+
			"(setsid dockerd > /var/log/dockerd.log 2>&1 < /dev/null &); "+
			"sleep 3; true")
		time.Sleep(5 * time.Second)
	}
	return false
}

// classifySSHError 分类 SSH 错误码
func classifySSHError(errStr string) string {
	switch {
	case strings.Contains(errStr, "connection refused"):
		return DeployErrSSHConnect
	case strings.Contains(errStr, "timeout"), strings.Contains(errStr, "i/o timeout"):
		return DeployErrSSHTimeout
	case strings.Contains(errStr, "authenticate"), strings.Contains(errStr, "unable to authenticate"),
		strings.Contains(errStr, "handshake failed"):
		return DeployErrSSHAuth
	case strings.Contains(errStr, "no such host"):
		return DeployErrSSHConnect
	default:
		return DeployErrSSHConnect
	}
}

// fixSuggestion 根据错误码返回修复建议
func fixSuggestion(errCode string) string {
	switch errCode {
	case DeployErrDockerNotInstalled:
		return "1) 内核可能不支持: 执行 uname -r 查看内核版本\n2) 检查 cgroup: mount | grep cgroup\n3) 检查 overlay: lsmod | grep overlay\n4) 手动安装: curl -fsSL https://get.docker.com | sh\n5) 如果低配 VPS 内核不支持 Docker, 请考虑升级系统或使用支持 Docker 的镜像"
	case DeployErrPortConflict:
		return "1) 释放被占用端口: fuser -k <端口>/tcp\n2) 修改节点的代理端口\n3) 停止占用端口的进程后重试"
	case DeployErrDiskFull:
		return "1) 清理磁盘: docker system prune -a\n2) 清理大文件: du -sh /* | sort -h\n3) 扩容服务器磁盘"
	case DeployErrMemoryLow:
		return "1) 升级服务器内存 (推荐 >=1GB)\n2) 创建 swap 分区: fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile"
	case DeployErrSSHConnect:
		return "1) 检查服务器 IP 是否正确\n2) 检查防火墙/安全组是否放行 SSH 端口\n3) 确认 SSH 服务已启动: systemctl start sshd"
	case DeployErrSSHAuth:
		return "1) 确认密码正确\n2) 检查 /etc/ssh/sshd_config 中 PasswordAuthentication yes\n3) 或改用 SSH 密钥认证"
	case DeployErrSSHTimeout:
		return "1) 检查网络连通性: ping <节点IP>\n2) 检查防火墙是否放行 SSH 端口\n3) 尝试 telnet <节点IP> 22"
	case DeployErrBuild:
		return "1) 检查 node_agent 源码是否完整\n2) 检查 Go 模块依赖: 进入面板 node_agent 目录执行 go mod tidy\n3) 查看完整编译日志"
	case DeployErrTransfer:
		return "1) 检查网络稳定性\n2) 确认磁盘有足够空间\n3) 重试部署"
	case DeployErrStart:
		return "1) 查看容器日志: docker logs <容器名>\n2) 检查端口冲突\n3) 确认 Docker 正常运行"
	case DeployErrVerify:
		return "1) 查看容器日志: docker logs <容器名>\n2) 确认面板 gRPC 端口可达\n3) 检查 NODE_TOKEN 是否与面板一致"
	default:
		return "1) 查看完整部署日志\n2) 确认服务器满足最低要求 (1GB 内存, 1GB 磁盘, Docker 已安装)\n3) 联系管理员"
	}
}

// ============================================================
// 容器启动诊断
// ============================================================

type diagResult struct {
	summary       string
	output        string
	hasWarning    bool
	fatal         bool
	fixSuggestion string
}

func diagnoseContainerStartup(client *ssh.Client, containerName string, listenPort, healthPort int) diagResult {
	time.Sleep(5 * time.Second)

	var lines []string

	// 兜底: docker ps 可能超时或报错, 捕获异常
	rawStatus, _ := sshRun(client, fmt.Sprintf("timeout 10 docker ps -a --filter name=%s --format '{{.ID}} {{.Status}}' 2>/dev/null; echo '---ALL---'; timeout 10 docker ps -a --format '{{.Names}} {{.Status}}' 2>/dev/null | head -20 || echo 'DOCKER_PS_FAILED'", containerName))
	lines = append(lines, "=== 容器状态 ===")
	lines = append(lines, rawStatus)

	containerStatus := strings.TrimSpace(rawStatus)

	// 兜底: docker ps 完全失败
	if strings.Contains(containerStatus, "DOCKER_PS_FAILED") {
		// 尝试 service 或 systemctl 检查 docker
		dockerSvcOut, _ := sshRun(client, "systemctl status docker 2>&1 | head -5 || service docker status 2>&1 | head -5 || echo 'UNKNOWN'")
		lines = append(lines, "=== Docker 服务状态 ===")
		lines = append(lines, dockerSvcOut)
		return diagResult{
			summary:       "Docker 服务异常, 无法获取容器状态",
			output:        strings.Join(lines, "\n"),
			fatal:         true,
			fixSuggestion: "1) 检查 Docker 服务: systemctl status docker  2) 重启 Docker: systemctl restart docker  3) 检查 /var/log/dockerd.log",
		}
	}

	if containerStatus == "" || strings.TrimSpace(strings.Split(containerStatus, "---ALL---")[0]) == "" {
		raw2, _ := sshRun(client, "timeout 10 docker ps -a 2>/dev/null || echo 'NO_CONTAINERS'")
		lines = append(lines, "=== 所有容器 ===")
		lines = append(lines, raw2)
		if !strings.Contains(raw2, "nexus") {
			return diagResult{
				summary:       "容器未创建，docker compose 可能执行失败",
				output:        strings.Join(lines, "\n"),
				fatal:         true,
				fixSuggestion: "请检查 docker-compose.node.yml 是否存在，手动执行: cd /root/node-agent && docker compose -f docker-compose.node.yml --env-file .env.node up -d --build",
			}
		}
		containerStatus = raw2
	}

	if strings.Contains(containerStatus, "Exited") || strings.Contains(containerStatus, "Restarting") {
		crashLogs, _ := sshRun(client, fmt.Sprintf("docker logs --tail 100 %s 2>&1 || echo 'LOGS_FAILED'", containerName))
		lines = append(lines, "=== 崩溃日志 ===")
		lines = append(lines, crashLogs)

		// 兜底: 额外检查容器退出码
		exitCode, _ := sshRun(client, fmt.Sprintf("docker inspect %s --format '{{.State.ExitCode}}' 2>/dev/null || echo 'UNKNOWN'", containerName))
		lines = append(lines, "=== 退出码: "+strings.TrimSpace(exitCode)+" ===")

		diag := analyzeLogs(crashLogs)
		return diagResult{
			summary:       "容器启动后立即退出 (退出码: " + strings.TrimSpace(exitCode) + "): " + diag.summary,
			output:        strings.Join(lines, "\n"),
			fatal:         true,
			fixSuggestion: diag.fixSuggestion,
		}
	}

	logs, _ := sshRun(client, fmt.Sprintf("docker logs --tail 60 %s 2>&1 || echo 'LOGS_FAILED'", containerName))
	lines = append(lines, "=== 启动日志 ===")
	lines = append(lines, logs)

	hasWarning := false
	warningMsg := ""
	if strings.Contains(logs, "error") || strings.Contains(logs, "Error") || strings.Contains(logs, "ERROR") || strings.Contains(logs, "warn") || strings.Contains(logs, "WARN") {
		if !strings.Contains(logs, "注册成功") && !strings.Contains(logs, "Xray 已启动") && !strings.Contains(logs, "Xray 进程已启动") {
			hasWarning = true
			warningMsg = "日志中发现错误/警告但容器仍在运行，可能正在重试"
		}
	}

	time.Sleep(3 * time.Second)
	// 兜底: 端口检测使用多种工具降级
	// [P3 fix 2026-07-19] 修正 fmt.Sprintf 参数数量(原 3 个 listenPort, 实际只需要 2 个 %d)
	portCheck, _ := sshRun(client, fmt.Sprintf(
		"(ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null || lsof -i -P -n 2>/dev/null | grep LISTEN) | grep ':%d' || echo '端口 %d 尚未监听'",
		listenPort, listenPort))
	lines = append(lines, "=== 端口监听检测 ===")
	lines = append(lines, portCheck)

	if strings.Contains(portCheck, "尚未监听") {
		if strings.Contains(logs, "下载 Xray") || strings.Contains(logs, "Download") {
			lines = append(lines, "Xray-core 正在下载中，请稍候...")
			hasWarning = true
			warningMsg = "Xray-core 正在下载中，预计还需 30-60 秒"
		} else if !strings.Contains(logs, "Xray 已启动") && !strings.Contains(logs, "Xray 进程已启动") {
			hasWarning = true
			warningMsg = fmt.Sprintf("代理端口 %d 尚未监听，Xray 可能仍在启动中或需等待下载完成", listenPort)
		}
	}

	if hasWarning {
		return diagResult{
			summary:    warningMsg,
			output:     strings.Join(lines, "\n"),
			hasWarning: true,
		}
	}

	return diagResult{
		summary: "容器运行正常，日志无异常",
		output:  strings.Join(lines, "\n"),
	}
}

// ============================================================
// 日志智能分析
// ============================================================

type logDiagnosis struct {
	summary       string
	fixSuggestion string
}

func analyzeLogs(logs string) logDiagnosis {
	logsLower := strings.ToLower(logs)

	// 兜底: 日志为空或获取失败
	if strings.TrimSpace(logs) == "" || strings.Contains(logs, "LOGS_FAILED") || strings.Contains(logs, "LOGS_UNAVAILABLE") {
		return logDiagnosis{
			summary:       "无法获取容器日志, 容器可能已停止或 Docker 异常",
			fixSuggestion: "1. 手动检查容器状态: docker ps -a | grep nexus\n2. 检查 Docker 服务: systemctl status docker\n3. 手动查看日志: docker logs <容器名>",
		}
	}

	if strings.Contains(logs, "token 无效") || strings.Contains(logs, "Unauthenticated") || strings.Contains(logs, "unauthenticated") {
		return logDiagnosis{
			summary:       "节点 Token 认证失败",
			fixSuggestion: "1. 检查 .env.node 中的 NODE_TOKEN 是否与面板一致\n2. 在面板节点列表点「轮换Token」获取新 token\n3. 更新 .env.node 后重启: docker restart nexus-node-agent",
		}
	}

	if strings.Contains(logs, "connection refused") || strings.Contains(logs, "dial tcp") || strings.Contains(logs, "no such host") || strings.Contains(logs, "connect:") {
		return logDiagnosis{
			summary:       "无法连接面板 gRPC 服务",
			fixSuggestion: "1. 检查 .env.node 中 PANEL_GRPC_ADDR 的 IP 和端口是否正确\n2. 确认面板服务器的 50051 端口已放行\n3. 在节点服务器手动测试: telnet <面板IP> 50051\n4. 如果面板使用 Docker，确认 gRPC 端口已映射",
		}
	}

	if strings.Contains(logsLower, "download") && (strings.Contains(logsLower, "xray") || strings.Contains(logsLower, "failed") || strings.Contains(logsLower, "timeout")) {
		return logDiagnosis{
			summary:       "Xray-core 下载失败或超时",
			fixSuggestion: "1. 节点服务器无法访问 GitHub，检查网络\n2. 手动下载: wget https://github.com/XTLS/Xray-core/releases/download/v26.6.1/Xray-linux-64.zip\n3. 放到 /app/xray/xray 并赋予执行权限\n4. 重启容器: docker restart nexus-node-agent",
		}
	}

	if strings.Contains(logs, "config") && (strings.Contains(logs, "invalid") || strings.Contains(logs, "parse") || strings.Contains(logs, "malformed")) {
		return logDiagnosis{
			summary:       "Xray 配置文件解析失败",
			fixSuggestion: "1. 在面板检查节点协议配置是否正确\n2. 查看完整日志: docker logs nexus-node-agent\n3. 尝试重建节点并重新部署",
		}
	}

	if strings.Contains(logs, "bind") && (strings.Contains(logs, "address already in use") || strings.Contains(logs, "permission denied") || strings.Contains(logs, "cannot assign")) {
		return logDiagnosis{
			summary:       "端口绑定失败",
			fixSuggestion: "1. 检查端口是否被占用: ss -tlnp | grep <端口>\n2. 杀掉占用进程: fuser -k <端口>/tcp\n3. 如果是非 root 用户，1024 以下端口需要 root 权限\n4. 重启容器: docker restart nexus-node-agent",
		}
	}

	if strings.Contains(logs, "permission denied") && strings.Contains(logs, "docker") {
		return logDiagnosis{
			summary:       "Docker 权限不足",
			fixSuggestion: "1. 确认以 root 用户部署\n2. 或将用户加入 docker 组: usermod -aG docker $USER\n3. 重启 Docker: systemctl restart docker",
		}
	}

	if strings.Contains(logs, "timeout") || strings.Contains(logs, "i/o timeout") || strings.Contains(logs, "deadline exceeded") {
		return logDiagnosis{
			summary:       "网络超时",
			fixSuggestion: "1. 检查节点服务器网络是否正常\n2. 检查 DNS 是否可用: nslookup github.com\n3. 如果是 gRPC 超时，检查面板 IP 是否可达: ping <面板IP>",
		}
	}

	if strings.Contains(logs, "panic") || strings.Contains(logs, "SIGSEGV") || strings.Contains(logs, "segmentation fault") {
		return logDiagnosis{
			summary:       "程序异常崩溃(panic)",
			fixSuggestion: "1. 查看完整日志: docker logs nexus-node-agent\n2. 尝试重新构建镜像: docker compose -f docker-compose.node.yml --env-file .env.node up -d --build\n3. 如持续崩溃请联系开发者并提供完整日志",
		}
	}

	// 兜底: OOM (Out of Memory)
	if strings.Contains(logsLower, "out of memory") || strings.Contains(logsLower, "oom") || strings.Contains(logsLower, "killed") {
		return logDiagnosis{
			summary:       "容器内存不足被系统杀死 (OOM)",
			fixSuggestion: "1. 升级服务器内存\n2. 增加 swap 分区\n3. 检查是否有其他程序占用内存: top -o %MEM",
		}
	}

	// 兜底: DNS 解析失败
	if strings.Contains(logsLower, "no such host") || strings.Contains(logsLower, "name resolution") || strings.Contains(logsLower, "dns") {
		return logDiagnosis{
			summary:       "DNS 解析失败, 无法解析域名",
			fixSuggestion: "1. 检查 DNS 配置: cat /etc/resolv.conf\n2. 尝试添加公共 DNS: echo 'nameserver 8.8.8.8' >> /etc/resolv.conf\n3. 检查面板域名是否可解析: nslookup <面板域名>",
		}
	}

	// 兜底: TLS/证书问题
	if strings.Contains(logsLower, "tls") || strings.Contains(logsLower, "certificate") || strings.Contains(logsLower, "x509") {
		return logDiagnosis{
			summary:       "TLS/证书验证失败",
			fixSuggestion: "1. 检查 CA 证书是否正确: cat /app/grpc-ca.crt\n2. 面板 gRPC TLS 自签证书需要推送到节点\n3. 如果使用 Let's Encrypt 证书, 确认系统 CA 包路径: /etc/ssl/certs/ca-certificates.crt",
		}
	}

	// 兜底: 磁盘空间不足 (在容器内)
	if strings.Contains(logsLower, "no space left") || strings.Contains(logsLower, "disk full") || strings.Contains(logsLower, "disk quota") {
		return logDiagnosis{
			summary:       "磁盘空间不足",
			fixSuggestion: "1. 清理磁盘: docker system prune -a\n2. 清理 Xray 日志缓存\n3. 扩容服务器磁盘",
		}
	}

	return logDiagnosis{
		summary:       "未知问题，请查看完整日志",
		fixSuggestion: "1. 查看完整日志: docker logs nexus-node-agent\n2. 检查容器状态: docker ps -a | grep nexus\n3. 手动重启: docker restart nexus-node-agent",
	}
}

// ============================================================
// SSH 错误诊断
// ============================================================

func diagnoseSSHError(err error, port int) string {
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") {
		return fmt.Sprintf("\n\n可能原因：\n1. 节点服务器 SSH 服务未启动（执行: systemctl start sshd）\n2. 防火墙屏蔽了 %d 端口\n3. SSH 端口不是 %d\n4. 服务器刚重装系统", port, port)
	} else if strings.Contains(errStr, "timeout") {
		return fmt.Sprintf("\n\n可能原因：\n1. IP 不可达或安全组未放行 %d 端口\n2. 服务器防火墙拦截\n3. 检查: ping <节点IP>", port)
	} else if strings.Contains(errStr, "authenticate") || strings.Contains(errStr, "handshake") {
		return "\n\n可能原因：\n1. 密码错误\n2. 服务器禁止密码登录（需在 sshd_config 设置 PasswordAuthentication yes）\n3. SSH 密钥交换不兼容"
	}
	return ""
}

// ============================================================
// SSH 辅助函数
// ============================================================

// parsePrivateKey 解析 PEM 格式的 SSH 私钥文本，返回 signer
// 支持: RSA, Ed25519, ECDSA 私钥(含加密/无加密)
// 私钥文本从用户输入直接粘贴，无需换行符转换
func parsePrivateKey(pemData string) (ssh.Signer, error) {
	// 尝试直接解析(支持加密私钥)
	signer, err := ssh.ParsePrivateKey([]byte(pemData))
	if err == nil {
		return signer, nil
	}
	// 加密私钥需要密码，目前不支持加密密钥的密码输入
	// 如果解析失败且是加密密钥，返回明确错误
	if strings.Contains(err.Error(), "encrypted") {
		return nil, fmt.Errorf("私钥已加密，暂不支持加密私钥。请使用无密码私钥: ssh-keygen -p -f ~/.ssh/id_ed25519")
	}
	// 尝试修复常见的粘贴格式问题: 去掉可能的前后空白
	trimmed := strings.TrimSpace(pemData)
	if trimmed != pemData {
		signer, err2 := ssh.ParsePrivateKey([]byte(trimmed))
		if err2 == nil {
			return signer, nil
		}
	}
	return nil, fmt.Errorf("私钥格式无效: %w", err)
}

// sshRunLocal 在面板服务器本地执行 shell 命令(用于预编译镜像)
func sshRunLocal(cmd string) (string, error) {
	parts := []string{"-c", cmd}
	out, err := exec.Command("sh", parts...).CombinedOutput()
	return string(out), err
}

// scpViaSSH 通过已建立的 SSH 连接传输文件到远程
// 使用 SSH stdin 管道 + base64 解码，避免 scp 协议兼容性问题
func scpViaSSH(client *ssh.Client, localPath, remotePath string) (string, error) {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("读取本地文件失败: %w", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("本地文件为空: %s", localPath)
	}

	if _, err := sshRun(client, "rm -f "+remotePath); err != nil {
		return "", fmt.Errorf("清空远程文件失败: %w", err)
	}

	chunkSize := 1024 * 1024
	totalChunks := (len(data) + chunkSize - 1) / chunkSize

	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[start:end]
		encoded := base64.StdEncoding.EncodeToString(chunk)

		session, err := client.NewSession()
		if err != nil {
			return "", fmt.Errorf("创建 session 失败(块 %d/%d): %w", i+1, totalChunks, err)
		}
		stdin, err := session.StdinPipe()
		if err != nil {
			session.Close()
			return "", fmt.Errorf("创建 stdin 管道失败(块 %d/%d): %w", i+1, totalChunks, err)
		}
		var errBuf bytes.Buffer
		session.Stderr = &errBuf
		if err := session.Start("export " + defaultPath + "; base64 -d >> " + remotePath); err != nil {
			session.Close()
			return "", fmt.Errorf("启动命令失败(块 %d/%d): %w", i+1, totalChunks, err)
		}
		_, err = stdin.Write([]byte(encoded))
		if err != nil {
			session.Close()
			return "", fmt.Errorf("写入数据失败(块 %d/%d): %w", i+1, totalChunks, err)
		}
		stdin.Close()
		if err := session.Wait(); err != nil {
			session.Close()
			return "", fmt.Errorf("传输块 %d/%d 失败: %w, stderr: %s", i+1, totalChunks, err, errBuf.String())
		}
		session.Close()
	}

	sshRun(client, "chmod +x "+remotePath)

	verifyOut, _ := sshRun(client, "ls -la "+remotePath)
	return fmt.Sprintf("已传输 %d 字节(分 %d 块), 远程文件: %s", len(data), totalChunks, verifyOut), nil
}

// defaultPath SSH 会话中注入的默认 PATH，防止某些服务器 SSH 会话 PATH 为空导致命令找不到
const defaultPath = "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func sshRun(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	// 重要: stdout/stderr 必须用独立 buffer, bytes.Buffer 不是并发安全的!
	// Go SSH 库内部可能并发写 stdout/stderr, 共用同一 buffer 会导致 data race 使输出丢失或损坏
	var outBuf, errBuf bytes.Buffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf
	// 兜底: 部分服务器 SSH 会话 PATH 为空, 导致 docker/curl/tar 等命令找不到
	err = session.Run("export " + defaultPath + "; " + cmd)
	if err != nil && errBuf.Len() > 0 {
		return outBuf.String(), fmt.Errorf("%w (stderr: %s)", err, errBuf.String())
	}
	return outBuf.String() + errBuf.String(), err
}

// sshWriteFile 通过 SSH 安全写入文件内容，避免命令注入风险
func sshWriteFile(client *ssh.Client, path, content string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}

	if err := session.Start("export " + defaultPath + "; cat > " + path); err != nil {
		stdin.Close()
		return err
	}

	safeGo(func() {
		stdin.Write([]byte(content))
		stdin.Close()
	})

	return session.Wait()
}

// sshStream 执行命令，实时回调每一行输出
func sshStream(client *ssh.Client, cmd string, onLine func(string)) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	pr, pw := io.Pipe()
	session.Stdout = pw
	session.Stderr = pw

	// 兜底: 部分服务器 SSH 会话 PATH 为空, 导致 docker/curl/tar 等命令找不到
	if err := session.Start("export " + defaultPath + "; " + cmd); err != nil {
		pw.Close()
		return "", err
	}

	var buf bytes.Buffer
	done := make(chan struct{})
	safeGo(func() {
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			buf.WriteString(line + "\n")
			onLine(line)
		}
		close(done)
	})

	err = session.Wait()
	pw.Close()
	<-done
	return buf.String(), err
}

// uploadNodeAgent 打包 node_agent 目录并通过 SSH stdin 传输到远程指定目录
func uploadNodeAgent(client *ssh.Client, deployDir string) error {
	var tarBuf bytes.Buffer
	gw := gzip.NewWriter(&tarBuf)
	tw := tar.NewWriter(gw)

	err := filepath.Walk(nodeAgentPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(nodeAgentPath, path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return fmt.Errorf("打包失败: %w", err)
	}
	tw.Close()
	gw.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	if err := session.Start("export " + defaultPath + "; tar -xzf - -C " + deployDir); err != nil {
		return err
	}

	safeGo(func() {
		stdin.Write(tarBuf.Bytes())
		stdin.Close()
	})

	return session.Wait()
}

// [P0#6 2026-07-14] safeGo 安全启动 goroutine,捕获 panic 防止进程崩溃
func safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[AUTO_DEPLOY] goroutine panic recovered: %v", r)
			}
		}()
		fn()
	}()
}
