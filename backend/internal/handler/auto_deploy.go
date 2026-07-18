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
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"

	"nexus-panel/internal/app"
	"go.uber.org/zap"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/security"
)

// ====== 部署错误码（前端可针对性提示） ======
const (
	DeployErrDockerNotInstalled = "DOCKER_NOT_INSTALLED" // Docker 未安装（已自动安装则不报）
	DeployErrPortConflict       = "PORT_CONFLICT"         // 目标端口已被占用
	DeployErrDiskFull           = "DISK_FULL"             // 磁盘空间不足
	DeployErrMemoryLow          = "MEMORY_LOW"            // 内存不足 1G
	DeployErrSSHConnect         = "SSH_CONNECT_FAIL"      // SSH 连接失败
	DeployErrSSHAuth            = "SSH_AUTH_FAIL"         // SSH 认证失败
	DeployErrSSHTimeout         = "SSH_TIMEOUT"           // SSH 连接超时
	DeployErrBuild              = "BUILD_FAIL"            // 编译失败
	DeployErrTransfer           = "TRANSFER_FAIL"         // 传输失败
	DeployErrStart              = "START_FAIL"            // 容器启动失败
	DeployErrVerify             = "VERIFY_FAIL"           // 节点注册验证失败
	DeployErrUnknown            = "UNKNOWN"
)

// ====== 部署阶段常量（6 步） ======
const (
	PhaseConnectServer = "connect_server"  // 1. 连接服务器
	PhaseEnvCheck      = "env_check"       // 2. 环境检测
	PhasePrepare       = "prepare"         // 3. 准备部署(目录+文件+配置+Docker)
	PhaseBuild         = "build"           // 4. 编译程序
	PhaseStart         = "start"           // 5. 启动服务
	PhaseVerify        = "verify"          // 6. 验证完成
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
	Password string `json:"password"`
	Username string `json:"username"`
	Port     int    `json:"port"`
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
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "缺少 SSH 密码"})
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
	port := req.Port
	username := req.Username
	req.Password = ""
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
		ok, errCode, errMsg := h.runDeployOnce(c, sse, node, password, port, username)
		if ok {
			// 安全：完成部署后清除密码
			password = ""
			sse.event("finish", "done", "一键部署完成！请返回节点列表查看在线状态", "")
			if f, ok2 := c.Writer.(http.Flusher); ok2 {
				f.Flush()
			}
			time.Sleep(100 * time.Millisecond)
			return
		}

		lastErrCode = errCode
		lastErrMsg = errMsg

		// 致命错误不重试（用户密码错、权限不足等）
		if errCode == DeployErrSSHAuth || errCode == DeployErrSSHConnect {
			sse.eventWithCode(PhaseVerify, "error",
				fmt.Sprintf("部署失败 (%s): %s\n\n修复建议: %s", errCode, errMsg, fixSuggestion(errCode)),
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
func (h *AutoDeployHandler) runDeployOnce(c *gin.Context, sse *sseWriter, node *model.Node, password string, port int, username string) (bool, string, string) {
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
	sshConfig := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
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
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		errStr := err.Error()
		code := classifySSHError(errStr)
		sse.eventWithCode(PhaseConnectServer, "error",
			"SSH 连接失败: "+errStr+diagnoseSSHError(err, port), "", code)
		return false, code, "SSH 连接失败: " + errStr
	}
	defer client.Close()
	sse.event(PhaseConnectServer, "done", "SSH 连接成功", "")

	// 清理同节点旧容器 + 旧部署目录 (容错: 可能本来就不存在)
	// 修复 NODE-RETRY-01 (P0): 旧版只清同名容器, 不清部署目录。
	//   3 次重试间若上次失败留下了脏 .env.node / 半截 agent 二进制 / 旧 xray-cache,
	//   下次部署 mkdir -p 不会删旧文件, 可能与新版本冲突(xray-cache 版本不匹配等)。
	//   现在重试前先 docker compose down + rm -rf 部署目录, 确保每次部署都是干净状态。
	sse.event(PhaseConnectServer, "running", "正在清理旧部署残留(容器+目录)...", "")
	cleanOut, cleanErr := sshRun(client, fmt.Sprintf(
		"cd %s 2>/dev/null && docker compose -f docker-compose.node.yml down 2>/dev/null; "+
			"docker stop %s 2>/dev/null; docker rm -f %s 2>/dev/null; "+
			"rm -rf %s 2>/dev/null; echo CLEANED",
		deployDir, containerName, containerName, deployDir))
	if cleanErr != nil {
		app.Get().Logger.Warn("清理旧残留失败 (通常无害, 可能首次部署无残留)",
			zap.String("container", containerName), zap.Error(cleanErr))
	}
	sse.event(PhaseConnectServer, "done", "旧残留清理完成", strings.TrimSpace(cleanOut))

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

	// 3.1 创建远程目录
	if out, err := sshRun(client, "mkdir -p "+deployDir); err != nil {
		sse.eventWithCode(PhasePrepare, "error", "创建目录失败: "+err.Error(), out, DeployErrUnknown)
		return false, DeployErrUnknown, "创建目录失败: " + err.Error()
	}

	// 3.2 推送 node_agent
	if err := uploadNodeAgent(client, deployDir); err != nil {
		sse.eventWithCode(PhasePrepare, "error", "推送文件失败: "+err.Error(), "", DeployErrTransfer)
		return false, DeployErrTransfer, "推送文件失败: " + err.Error()
	}

	// 3.3 安装 Docker (如果未安装)
	dockerOK, dockerCode, dockerMsg := ensureDocker(client, sse)
	if !dockerOK {
		sse.eventWithCode(PhasePrepare, "error",
			"Docker 安装失败: "+dockerMsg+"\n\n修复建议: "+fixSuggestion(dockerCode),
			"", dockerCode)
		return false, dockerCode, dockerMsg
	}

	// 3.4 创建 .env.node
	envContent := fmt.Sprintf("CONTAINER_NAME=%s\nPANEL_GRPC_ADDR=%s:%s\nNODE_TOKEN=%s\nLISTEN_PORT=%d\nHEALTH_PORT=%d\nXRAY_VERSION=v26.6.1",
		containerName, panelIP, grpcPortStr, node.NodeToken, listenPort, healthPort)
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
			if err := sshWriteFile(client, deployDir+"/grpc-ca.crt", string(caContent)); err != nil {
				sse.eventWithCode(PhasePrepare, "error", "推送 gRPC CA 证书失败: "+err.Error(), "", DeployErrTransfer)
				return false, DeployErrTransfer, "推送 CA 证书失败: " + err.Error()
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
	if err := sshWriteFile(client, deployDir+"/.env.node", envContent); err != nil {
		sse.eventWithCode(PhasePrepare, "error", "写配置文件失败: "+err.Error(), "", DeployErrUnknown)
		return false, DeployErrUnknown, "写配置失败: " + err.Error()
	}

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
		// 优化: 使用 alpine 镜像替代 bullseye, 下载速度提升 3-5 倍, 体积缩小 70%
		hostNodeAgentPath := "/root/nexus-panel/node_agent"
		compileCmd := fmt.Sprintf(
			"docker run --rm "+
				"-v %s:/build -w /build "+
				"golang:1.21-alpine "+
				"sh -c 'apk add --no-cache git >/dev/null 2>&1; go mod download && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags=\"-s -w\" -o /build/agent . 2>&1' && ls -lh %s/agent",
			hostNodeAgentPath, hostNodeAgentPath)
		buildOut, buildErr := sshRunLocal(compileCmd)
		if buildErr != nil {
			sse.eventWithCode(PhaseBuild, "error", "二进制预编译失败: "+buildErr.Error(), buildOut, DeployErrBuild)
			return false, DeployErrBuild, "编译失败: " + buildErr.Error()
		}
		sse.event(PhaseBuild, "done", "二进制预编译完成", buildOut)
	}

	// 推送二进制到节点
	if transferOut, transferErr := scpViaSSH(client, "/app/node_agent/agent", deployDir+"/agent"); transferErr != nil {
		sse.eventWithCode(PhaseBuild, "error", "传输失败: "+transferErr.Error(), transferOut, DeployErrTransfer)
		return false, DeployErrTransfer, "传输失败: " + transferErr.Error()
	}
	if _, err := sshRun(client, "chmod +x "+deployDir+"/agent"); err != nil {
		return false, DeployErrTransfer, "chmod agent 失败: " + err.Error()
	}

	// ============================================================
	// Phase 5: 启动服务
	// ============================================================
	sse.event(PhaseStart, "running", "构建镜像并启动 "+containerName+"...", "")
	startOut, err := sshStream(client, fmt.Sprintf(
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
			strings.Contains(trimmed, "Container") {
			sse.event(PhaseStart, "log", "", line)
		}
	})
	if err != nil {
		sse.eventWithCode(PhaseStart, "error", "启动失败: "+err.Error(), startOut, DeployErrStart)
		return false, DeployErrStart, "启动失败: " + err.Error()
	}
	sse.event(PhaseStart, "done", "node-agent 容器已启动", startOut)

	// ============================================================
	// Phase 6: 验证完成
	// ============================================================
	sse.event(PhaseVerify, "running", "检测容器运行状态和日志...", "")
	diagResult := diagnoseContainerStartup(client, containerName, listenPort, healthPort)
	if diagResult.fatal {
		sse.eventWithCode(PhaseVerify, "error", diagResult.summary, diagResult.output, DeployErrStart)
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
	for i := 0; i < 40; i++ {
		time.Sleep(3 * time.Second)
		verifyOut, _ = sshRun(client, fmt.Sprintf("docker logs --tail 50 %s 2>&1", containerName))
		if strings.Contains(verifyOut, "注册成功") || strings.Contains(verifyOut, "已注册到面板") || strings.Contains(verifyOut, "Xray 已启动") || strings.Contains(verifyOut, "Xray 进程已启动") {
			success = true
			break
		}
		if strings.Contains(verifyOut, "注册失败") || strings.Contains(verifyOut, "token 无效") {
			break
		}
	}
	if success {
		portCheck, _ := sshRun(client, fmt.Sprintf("ss -tlnp | grep -E ':%d|:%d' 2>/dev/null || netstat -tlnp 2>/dev/null | grep -E ':%d|:%d'", listenPort, healthPort, listenPort, healthPort))
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
func preDeployCheck(client *ssh.Client, listenPort, healthPort int, sse *sseWriter) preCheckResult {
	var lines []string
	result := preCheckResult{OK: true}

	// 1. 磁盘空间检测
	sse.event(PhaseEnvCheck, "running", "正在检查磁盘空间...", "")
	diskOut, _ := sshRun(client, "df -BG / | tail -1")
	lines = append(lines, "=== 磁盘空间 ===")
	lines = append(lines, diskOut)
	if availGB := parseAvailGB(diskOut); availGB >= 0 && availGB < 1 {
		result.OK = false
		result.Fatal = true
		result.ErrCode = DeployErrDiskFull
		result.Reason = fmt.Sprintf("磁盘可用空间仅 %dGB, 不足以构建 Docker 镜像 (需要 >=1GB)", availGB)
		result.FixSuggestion = "1) 清理磁盘: docker system prune -a  2) 扩容磁盘  3) 清理大文件: du -sh /* | sort -h"
		result.Output = strings.Join(lines, "\n")
		return result
	}
	sse.event(PhaseEnvCheck, "log", fmt.Sprintf("磁盘空间充足 (可用 %dGB)", availGB), diskOut)

	// 2. 内存检测 (自动创建 swap 兜底)
	sse.event(PhaseEnvCheck, "running", "正在检查内存...", "")
	memOut, _ := sshRun(client, "free -m | head -2")
	lines = append(lines, "=== 内存 ===")
	lines = append(lines, memOut)
	if availMB := parseAvailMB(memOut); availMB >= 0 && availMB < 512 {
		sse.event(PhaseEnvCheck, "warning", fmt.Sprintf("可用内存仅 %dMB, 低于 512MB 要求, 尝试自动创建 swap...", availMB), memOut)
		// 内存不足, 先尝试自动创建 swap 分区 (需要磁盘 >=2G 可用)
		diskForSwap, _ := sshRun(client, "df -BG / | tail -1")
		if availGB := parseAvailGB(diskForSwap); availGB >= 2 {
			sse.event(PhaseEnvCheck, "running", fmt.Sprintf("磁盘可用 %dGB, 正在创建 2GB swap 分区 (fallocate→dd 自动回退)...", availGB), "")
			// 先尝试 fallocate (快), 失败回退到 dd (兼容性更好)
			swapCmd := "(" +
				"fallocate -l 2G /swapfile 2>/dev/null || " +
				"dd if=/dev/zero of=/swapfile bs=1M count=2048 2>/dev/null" +
				") && " +
				"chmod 600 /swapfile && " +
				"mkswap /swapfile && " +
				"swapon /swapfile && " +
				"echo 'SWAP_CREATED'"
			swapOut, swapErr := sshRun(client, swapCmd)
			lines = append(lines, swapOut)
			if swapErr == nil && strings.Contains(swapOut, "SWAP_CREATED") {
				sse.event(PhaseEnvCheck, "done", "swap 分区创建成功, 重新检测内存...", swapOut)
				// 重新检测内存
				memOut2, _ := sshRun(client, "free -m | head -2")
				lines = append(lines, memOut2)
				if availMB2 := parseAvailMB(memOut2); availMB2 >= 512 {
					// swap 生效, 内存达标
					sse.event(PhaseEnvCheck, "done", fmt.Sprintf("内存达标, swap 生效后可用 %dMB >= 512MB", availMB2), memOut2)
					lines = append(lines, fmt.Sprintf("[通过] swap 生效, 可用内存 %dMB >= 512MB", availMB2))
				} else {
					result.OK = false
					result.Fatal = true
					result.ErrCode = DeployErrMemoryLow
					result.Reason = fmt.Sprintf("swap 已创建但仍不足, 可用内存仅 %dMB (需要 >=512MB)", availMB2)
					result.FixSuggestion = "1) 升级服务器内存 (推荐 >=1GB)  2) 关闭其他占用内存的进程"
					result.Output = strings.Join(lines, "\n")
					return result
				}
			} else {
				lines = append(lines, fmt.Sprintf("[失败] swap 创建失败: %v", swapErr))
				result.OK = false
				result.Fatal = true
				result.ErrCode = DeployErrMemoryLow
				result.Reason = fmt.Sprintf("可用内存仅 %dMB, swap 自动创建失败 (需要 >=512MB)", availMB)
				result.FixSuggestion = "1) 升级服务器内存 (推荐 >=1GB)  2) 手动创建 swap 分区: fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile"
				result.Output = strings.Join(lines, "\n")
				return result
			}
		} else {
			// 磁盘也不够, 无法创建 swap
			result.OK = false
			result.Fatal = true
			result.ErrCode = DeployErrMemoryLow
			result.Reason = fmt.Sprintf("可用内存仅 %dMB, 磁盘空间也不足无法创建 swap (需要 >=512MB)", availMB)
			result.FixSuggestion = "1) 升级服务器内存 (推荐 >=1GB)  2) 清理磁盘空间后重试, 面板将自动创建 swap"
			result.Output = strings.Join(lines, "\n")
			return result
		}
	} else {
		sse.event(PhaseEnvCheck, "log", fmt.Sprintf("内存充足 (可用 %dMB)", availMB), memOut)
	}

	// 3. Docker 检测
	sse.event(PhaseEnvCheck, "running", "正在检查 Docker...", "")
	dockerStatus, _ := sshRun(client, "command -v docker >/dev/null 2>&1 && docker info 2>&1 | head -3 || echo 'NOT_INSTALLED'")
	lines = append(lines, "=== Docker ===")
	lines = append(lines, dockerStatus)
	dockerInstalled := !strings.Contains(dockerStatus, "NOT_INSTALLED")
	dockerRunning := strings.Contains(dockerStatus, "Server Version") || strings.Contains(dockerStatus, "Containers")
	if !dockerInstalled {
		sse.event(PhaseEnvCheck, "log", "Docker 未安装, 将在准备阶段自动安装", dockerStatus)
		lines = append(lines, "[提示] Docker 未安装, 将在准备阶段自动安装")
	} else if !dockerRunning {
		sse.event(PhaseEnvCheck, "warning", "Docker 已安装但未运行, 正在尝试启动...", dockerStatus)
		lines = append(lines, "[警告] Docker 已安装但未运行, 正在尝试启动...")
		startOut, _ := sshRun(client, "systemctl start docker 2>&1; systemctl enable docker 2>&1; sleep 2; docker info 2>&1 | head -3")
		lines = append(lines, startOut)
		if !strings.Contains(startOut, "Server Version") && !strings.Contains(startOut, "Containers") {
			result.OK = false
			result.Fatal = false
			result.ErrCode = DeployErrDockerNotInstalled
			result.Reason = "Docker 已安装但首次启动失败, 将在准备阶段重试"
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
	portCheck, _ := sshRun(client, fmt.Sprintf(
		"ss -tlnp 2>/dev/null | grep -E ':%d|:%d' || netstat -tlnp 2>/dev/null | grep -E ':%d|:%d' || echo 'PORTS_AVAILABLE'",
		listenPort, healthPort, listenPort, healthPort))
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
	netOut, _ := sshRun(client, "curl -sI --max-time 5 https://get.docker.com 2>&1 | head -1; echo '---'; curl -sI --max-time 5 https://registry-1.docker.io 2>&1 | head -1")
	lines = append(lines, "=== 网络 ===")
	lines = append(lines, netOut)
	if !strings.Contains(netOut, "HTTP/") {
		lines = append(lines, "[警告] 无法访问 get.docker.com, Docker 自动安装可能失败")
		result.Reason = "网络受限, Docker 自动安装可能失败"
		result.FixSuggestion = "1) 检查防火墙  2) 配置代理  3) 手动安装 Docker 后重试"
		result.ErrCode = DeployErrUnknown
		result.OK = false
		sse.event(PhaseEnvCheck, "warning", "无法访问 Docker 仓库, 自动安装可能失败", netOut)
	} else {
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

// ensureDocker 检查并安装 Docker; 已安装则跳过
func ensureDocker(client *ssh.Client, sse *sseWriter) (bool, string, string) {
	// 检测 Docker
	checkOut, _ := sshRun(client, "command -v docker >/dev/null 2>&1 && echo 'INSTALLED' || echo 'NOT_INSTALLED'")
	if strings.Contains(checkOut, "INSTALLED") {
		// 已安装, 检测是否运行
		runOut, _ := sshRun(client, "docker info 2>&1 | head -3")
		if strings.Contains(runOut, "Server Version") {
			sse.event(PhasePrepare, "done", "Docker 已安装并运行", runOut)
			return true, "", ""
		}
		// 启动 Docker
		sse.event(PhasePrepare, "log", "", "Docker 已安装, 尝试启动...")
		sshRun(client, "systemctl start docker 2>&1; systemctl enable docker 2>&1")
		time.Sleep(2 * time.Second)
		runOut, _ = sshRun(client, "docker info 2>&1 | head -3")
		if !strings.Contains(runOut, "Server Version") {
			return false, DeployErrDockerNotInstalled, "Docker 已安装但无法启动"
		}
		return true, "", ""
	}

	// 未安装, 自动安装
	sse.event(PhasePrepare, "log", "", "Docker 未安装, 正在自动安装 (约需 1-3 分钟)...")
	installCmd := "curl -fsSL https://get.docker.com | sh 2>&1 && systemctl enable docker && systemctl start docker && echo 'INSTALL_OK' || echo 'INSTALL_FAIL'"
	out, _ := sshStream(client, installCmd, func(line string) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			return
		}
		sse.event(PhasePrepare, "log", "", trimmed)
	})
	if !strings.Contains(out, "INSTALL_OK") {
		return false, DeployErrDockerNotInstalled, "Docker 自动安装失败"
	}
	sse.event(PhasePrepare, "done", "Docker 自动安装完成", "")
	return true, "", ""
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
		return "1) 手动安装 Docker: curl -fsSL https://get.docker.com | sh\n2) 检查防火墙是否屏蔽了 Docker 安装脚本\n3) 确认服务器能访问 get.docker.com"
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
		return "1) 检查 node_agent 源码是否完整\n2) 检查 Go 模块依赖: cd /root/nexus-panel/node_agent && go mod tidy\n3) 查看完整编译日志"
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

	rawStatus, _ := sshRun(client, fmt.Sprintf("docker ps -a --filter name=%s --format '{{.ID}} {{.Status}}' 2>/dev/null; docker ps -a --format '{{.Names}} {{.Status}}' 2>/dev/null | head -20", containerName))
	lines = append(lines, "=== 容器状态 ===")
	lines = append(lines, rawStatus)

	containerStatus := strings.TrimSpace(rawStatus)

	if containerStatus == "" {
		raw2, _ := sshRun(client, "docker ps -a 2>/dev/null")
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
		crashLogs, _ := sshRun(client, fmt.Sprintf("docker logs --tail 80 %s 2>&1", containerName))
		lines = append(lines, "=== 崩溃日志 ===")
		lines = append(lines, crashLogs)

		diag := analyzeLogs(crashLogs)
		return diagResult{
			summary:       "容器启动后立即退出: " + diag.summary,
			output:        strings.Join(lines, "\n"),
			fatal:         true,
			fixSuggestion: diag.fixSuggestion,
		}
	}

	logs, _ := sshRun(client, fmt.Sprintf("docker logs --tail 50 %s 2>&1", containerName))
	lines = append(lines, "=== 启动日志 ===")
	lines = append(lines, logs)

	hasWarning := false
	warningMsg := ""
	if strings.Contains(logs, "error") || strings.Contains(logs, "Error") || strings.Contains(logs, "ERROR") {
		if !strings.Contains(logs, "注册成功") && !strings.Contains(logs, "Xray 已启动") && !strings.Contains(logs, "Xray 进程已启动") {
			hasWarning = true
			warningMsg = "日志中发现错误但容器仍在运行，可能正在重试"
		}
	}

	time.Sleep(3 * time.Second)
	portCheck, _ := sshRun(client, fmt.Sprintf("ss -tlnp 2>/dev/null | grep ':%d' || netstat -tlnp 2>/dev/null | grep ':%d' || echo '端口 %d 尚未监听'", listenPort, listenPort, listenPort))
	lines = append(lines, "=== 端口监听检测 ===")
	lines = append(lines, portCheck)

	if strings.Contains(portCheck, "尚未监听") {
		if strings.Contains(logs, "下载 Xray") {
			lines = append(lines, "Xray-core 正在下载中，请稍候...")
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

	if strings.Contains(logs, "token 无效") || strings.Contains(logs, "Unauthenticated") {
		return logDiagnosis{
			summary:       "节点 Token 认证失败",
			fixSuggestion: "1. 检查 .env.node 中的 NODE_TOKEN 是否与面板一致\n2. 在面板节点列表点「轮换Token」获取新 token\n3. 更新 .env.node 后重启: docker restart nexus-node-agent",
		}
	}

	if strings.Contains(logs, "connection refused") || strings.Contains(logs, "dial tcp") || strings.Contains(logs, "no such host") {
		return logDiagnosis{
			summary:       "无法连接面板 gRPC 服务",
			fixSuggestion: "1. 检查 .env.node 中 PANEL_GRPC_ADDR 的 IP 和端口是否正确\n2. 确认面板服务器的 50051 端口已放行\n3. 在节点服务器手动测试: telnet <面板IP> 50051\n4. 如果面板使用 Docker，确认 gRPC 端口已映射",
		}
	}

	if strings.Contains(logsLower, "download") && (strings.Contains(logsLower, "xray") || strings.Contains(logsLower, "failed")) {
		return logDiagnosis{
			summary:       "Xray-core 下载失败",
			fixSuggestion: "1. 节点服务器无法访问 GitHub，检查网络\n2. 手动下载: wget https://github.com/XTLS/Xray-core/releases/download/v26.6.1/Xray-linux-64.zip\n3. 放到 /app/xray/xray 并赋予执行权限\n4. 重启容器: docker restart nexus-node-agent",
		}
	}

	if strings.Contains(logs, "config") && (strings.Contains(logs, "invalid") || strings.Contains(logs, "parse")) {
		return logDiagnosis{
			summary:       "Xray 配置文件解析失败",
			fixSuggestion: "1. 在面板检查节点协议配置是否正确\n2. 查看完整日志: docker logs nexus-node-agent\n3. 尝试重建节点并重新部署",
		}
	}

	if strings.Contains(logs, "bind") && (strings.Contains(logs, "address already in use") || strings.Contains(logs, "permission denied")) {
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

	if strings.Contains(logs, "timeout") || strings.Contains(logs, "i/o timeout") {
		return logDiagnosis{
			summary:       "网络超时",
			fixSuggestion: "1. 检查节点服务器网络是否正常\n2. 检查 DNS 是否可用: nslookup github.com\n3. 如果是 gRPC 超时，检查面板 IP 是否可达: ping <面板IP>",
		}
	}

	if strings.Contains(logs, "panic") {
		return logDiagnosis{
			summary:       "程序异常崩溃(panic)",
			fixSuggestion: "1. 查看完整日志: docker logs nexus-node-agent\n2. 尝试重新构建镜像: docker compose -f docker-compose.node.yml --env-file .env.node up -d --build\n3. 如持续崩溃请联系开发者并提供完整日志",
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
		if err := session.Start("base64 -d >> " + remotePath); err != nil {
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

func sshRun(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf
	err = session.Run(cmd)
	return buf.String(), err
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

	if err := session.Start("cat > " + path); err != nil {
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

	if err := session.Start(cmd); err != nil {
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
	if err := session.Start("tar -xzf - -C " + deployDir); err != nil {
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
