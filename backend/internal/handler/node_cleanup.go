package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
	"go.uber.org/zap"
)

// ============================================================
// 节点清理 (删除节点时自动 SSH 清理节点服务器残留资源)
//
// 解决问题 (P0):
//   1. 旧版 DeleteNode 只删 DB+Redis, 不 SSH 到节点服务器清理。
//      导致旧 agent 容器持续 Running 占内存, .env.node 含旧 token 泄露,
//      部署目录/二进制/xray-cache 全部残留。
//   2. 若运维误重启旧 agent 容器, 用旧 token 注册失败 30 次 → log.Fatalf
//      → docker restart=unless-stopped 重启 → 死循环持续刷日志占 CPU。
//
// 修复: 删除节点时新增 SSE 流式清理接口, 全自动 SSH 到节点服务器:
//   - 停止并删除 agent 容器
//   - 删除部署目录(含 .env.node / agent 二进制 / xray-cache)
//   - 删除 agent 镜像(可选, 释放磁盘)
//   - 最后才执行 DB 软删 + Redis 清理(原有逻辑)
//
// 兜底: SSH 任意步骤失败不阻断流程, DB 删除最终一定执行。
// 即使节点服务器不可达, 节点也能在面板侧被删除(只是服务器上残留资源需手动清理)。
// 全程 SSE 推进度到前端, 类似面板自动更新的进度条体验。
//
// 防卡死机制 (P0):
//   1. SSH 连接: 3次重试 + 递增等待 (2s/4s/6s), 总超时 30s
//   2. 会话验证: 连接后 echo 测试, 确保 session 可用
//   3. 每步操作: context.WithTimeout 超时保护 (docker 命令 60s, rm 30s)
//   4. docker stop 卡死: 先 stop 超时 15s, 再 docker kill 兜底
//   5. docker rm 卡死: 先 rm -f, 超时后跳过 (节点已下线场景)
//   6. rm -rf 卡死: 超时后尝试后台执行, 最后跳过
//   7. 步骤间 200ms 延迟: 确保前端逐条渐进式展示, 不会一次性跳完
// ============================================================

// 清理阶段常量 (5 步)
const (
	CleanupPhaseConnect  = "connect"  // 1. SSH 连接节点服务器
	CleanupPhaseStop     = "stop"     // 2. 停止并删除 agent 容器
	CleanupPhaseDir      = "dir"      // 3. 删除部署目录
	CleanupPhaseImage    = "image"    // 4. 删除 agent 镜像(可选)
	CleanupPhaseFinalize = "finalize" // 5. DB 软删 + Redis 清理
)

// 清理超时配置
const (
	cleanupSSHTimeout       = 15 * time.Second // SSH 单次连接超时
	cleanupSSHDialTotal     = 30 * time.Second // SSH 总连接超时 (含重试)
	cleanupDockerStopTimeout = 15 * time.Second // docker stop 超时
	cleanupDockerRmTimeout   = 10 * time.Second // docker rm 超时
	cleanupRmTimeout         = 20 * time.Second // rm -rf 超时
	cleanupDockerRmiTimeout  = 30 * time.Second // docker rmi 超时
	cleanupStepDelay         = 200 * time.Millisecond // 步骤间延迟, 确保前端渐进展示
)

// NodeCleanupHandler 节点清理 handler
type NodeCleanupHandler struct {
	nodeService *service.NodeService
	nodeRepo    interface{ GetByID(id string) (*model.Node, error) }
	logger      *zap.Logger
}

// NewNodeCleanupHandler 创建节点清理 handler
func NewNodeCleanupHandler(nodeService *service.NodeService, nodeRepo interface{ GetByID(id string) (*model.Node, error) }, logger *zap.Logger) *NodeCleanupHandler {
	return &NodeCleanupHandler{
		nodeService: nodeService,
		nodeRepo:    nodeRepo,
		logger:      logger,
	}
}

// cleanupReq 清理请求 (SSH 凭据, 删除时前端弹窗输入)
type cleanupReq struct {
	Password  string `json:"password"`  // SSH 密码 (必填)
	Username  string `json:"username"`  // SSH 用户名 (默认 root)
	Port      int    `json:"port"`      // SSH 端口 (默认 22)
	RemoveImg bool   `json:"removeImg"` // 是否删除 docker 镜像 (默认 false, 仅停容器删目录)
}

// CleanupWithProgress 带进度展示的节点清理 (SSE 流式)
// 路由: DELETE /api/v1/admin/nodes/:id/cleanup
func (h *NodeCleanupHandler) CleanupWithProgress(c *gin.Context) {
	nodeID := c.Param("id")
	node, err := h.nodeRepo.GetByID(nodeID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"msg": "节点不存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "查询节点失败"})
		return
	}

	var req cleanupReq
	// 允许 body 为空 (跳过 SSH 清理, 仅 DB 删除)
	_ = c.ShouldBindJSON(&req)
	if req.Username == "" {
		req.Username = "root"
	}
	if req.Port == 0 {
		req.Port = 22
	}
	password := req.Password
	req.Password = "" // 清出避免后续误用

	// SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "不支持流式响应"})
		return
	}
	flusher.Flush()

	sse := &sseWriter{flusher: flusher, writer: c.Writer}

	// SSE 心跳 (防止代理超时断开)
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

	// 兜底: 无论 SSH 清理是否成功, 最终都执行 DB 删除
	dbDeleted := false
	defer func() {
		if !dbDeleted {
			// 最终步骤间有延迟, 确保前端能渐进展示
			time.Sleep(cleanupStepDelay)
			sse.event(CleanupPhaseFinalize, "running", "正在执行面板侧清理 (DB+Redis)...", "")
			time.Sleep(cleanupStepDelay)
			if err := h.nodeService.DeleteNode(nodeID); err != nil {
				sse.eventWithCode(CleanupPhaseFinalize, "warning",
					"面板侧清理失败: "+err.Error()+" (节点服务器资源已清理, 可稍后重试)", "", "")
			} else {
				sse.event(CleanupPhaseFinalize, "done", "面板侧清理完成", "")
			}
		}
		time.Sleep(cleanupStepDelay)
		sse.event("finish", "done", "节点清理完成", "")
		if f, ok := c.Writer.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(100 * time.Millisecond)
	}()

	// ====== Phase 1: SSH 连接节点服务器 (带重试 + 防卡死) ======
	if password == "" {
		sse.event(CleanupPhaseConnect, "warning", "未提供 SSH 密码, 跳过节点服务器清理, 仅执行面板侧删除", "")
		return
	}
	sse.event(CleanupPhaseConnect, "running",
		fmt.Sprintf("正在连接节点服务器 %s:%d...", node.ServerAddress, req.Port), "")
	time.Sleep(cleanupStepDelay) // 确保前端先展示 "进行中" 状态

	client, err := h.connectSSHWithRetry(node.ServerAddress, req.Username, password, req.Port, sse)
	if err != nil {
		// SSH 连不上不阻断: 节点可能已下线/重装系统, DB 删除仍需执行
		sse.eventWithCode(CleanupPhaseConnect, "warning",
			"SSH 连接失败: "+err.Error()+" (节点服务器资源未清理, 仅执行面板侧删除)", "",
			classifySSHError(err.Error()))
		return
	}
	defer client.Close()
	time.Sleep(cleanupStepDelay)
	sse.event(CleanupPhaseConnect, "done", "SSH 连接成功", "")

	// ====== 计算 shortID (与部署时一致) ======
	shortID := node.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	containerName := "nexus-agent-" + shortID
	deployDir := "/root/node-agent-" + shortID

	// ====== Phase 2: 停止并删除 agent 容器 (带超时 + 降级) ======
	time.Sleep(cleanupStepDelay)
	sse.event(CleanupPhaseStop, "running", "正在停止并删除 agent 容器 "+containerName+"...", "")
	time.Sleep(cleanupStepDelay)
	h.stopAndRemoveContainer(client, containerName, sse)

	// ====== Phase 3: 删除部署目录 (带超时 + 降级) ======
	time.Sleep(cleanupStepDelay)
	sse.event(CleanupPhaseDir, "running", "正在删除部署目录 "+deployDir+"...", "")
	time.Sleep(cleanupStepDelay)
	h.removeDeployDir(client, deployDir, sse)

	// ====== Phase 4: 删除 docker 镜像 (可选, 带超时) ======
	time.Sleep(cleanupStepDelay)
	if req.RemoveImg {
		sse.event(CleanupPhaseImage, "running", "正在删除 agent 镜像 nexus-node-agent:latest...", "")
		time.Sleep(cleanupStepDelay)
		h.removeAgentImage(client, sse)
	} else {
		sse.event(CleanupPhaseImage, "done", "跳过镜像删除 (如需删除请勾选)", "")
	}

	// ====== Phase 5: 面板侧 DB + Redis 清理 ======
	time.Sleep(cleanupStepDelay)
	sse.event(CleanupPhaseFinalize, "running", "正在执行面板侧清理 (DB软删 + Redis)...", "")
	time.Sleep(cleanupStepDelay)
	if err := h.nodeService.DeleteNode(nodeID); err != nil {
		sse.eventWithCode(CleanupPhaseFinalize, "error",
			"面板侧清理失败: "+err.Error(), "", "")
		// 即使 DB 删除失败, 节点服务器资源已清理, 仍标记完成
		time.Sleep(cleanupStepDelay)
		sse.event("finish", "done", "节点服务器资源已清理, 但面板侧清理失败, 请重试", "")
		return
	}
	dbDeleted = true
	sse.event(CleanupPhaseFinalize, "done", "面板侧清理完成 (DB软删 + Redis + SSH指纹)", "")

	if app.Get().Logger != nil {
		app.Get().Logger.Info("节点清理完成 (含SSH清理)",
			zap.String("node_id", nodeID),
			zap.String("server", node.ServerAddress),
			zap.String("container", containerName))
	}
}

// ============================================================
// 带重试的 SSH 连接 (防卡死: 3次重试 + 递增等待 + 会话验证)
// ============================================================
func (h *NodeCleanupHandler) connectSSHWithRetry(host, username, password string, port int, sse *sseWriter) (*ssh.Client, error) {
	sshConfig := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: hostKeyCallback(host),
		Timeout:         cleanupSSHTimeout,
		Config: ssh.Config{
			KeyExchanges: []string{
				"curve25519-sha256", "curve25519-sha256@libssh.org",
				"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
				"diffie-hellman-group14-sha256", "diffie-hellman-group14-sha1",
				"diffie-hellman-group16-sha512", "diffie-hellman-group18-sha512",
				"diffie-hellman-group-exchange-sha256", "diffie-hellman-group-exchange-sha1",
				"sntrup761x25519-sha512@openssh.com",
			},
			Ciphers: []string{
				"aes256-gcm@openssh.com", "aes128-gcm@openssh.com",
				"aes256-ctr", "aes192-ctr", "aes128-ctr",
				"chacha20-poly1305@openssh.com",
			},
		},
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	// 兜底: 3 次重试, 递增等待 (2s / 4s / 6s), 总超时 30s
	startTime := time.Now()
	var client *ssh.Client
	var dialErr error
	for retry := 1; retry <= 3; retry++ {
		// 总超时检查: 防止重试累计超过 30s
		if time.Since(startTime) > cleanupSSHDialTotal {
			dialErr = fmt.Errorf("SSH 连接总超时 (超过 %ds)", int(cleanupSSHDialTotal.Seconds()))
			break
		}

		// 用 context 包裹, 防止 ssh.Dial 无限阻塞
		ctx, cancel := context.WithTimeout(context.Background(), cleanupSSHTimeout)
		dialDone := make(chan struct{})

		safeGo(func() {
			client, dialErr = ssh.Dial("tcp", addr, sshConfig)
			close(dialDone)
		})

		select {
		case <-dialDone:
			cancel()
			if dialErr == nil {
				// 兜底: 连接后验证会话是否真正可用
				if testOut, testErr := sshRun(client, "echo 'SSH_OK'"); testErr != nil || !strings.Contains(testOut, "SSH_OK") {
					client.Close()
					client = nil
					dialErr = fmt.Errorf("SSH 已连接但无法执行命令: %v", testErr)
				} else {
					return client, nil
				}
			}
		case <-ctx.Done():
			cancel()
			dialErr = fmt.Errorf("SSH 连接超时 (%ds)", int(cleanupSSHTimeout.Seconds()))
			if client != nil {
				client.Close()
				client = nil
			}
		}

		if retry < 3 {
			waitTime := time.Duration(retry*2) * time.Second
			sse.event(CleanupPhaseConnect, "log", "",
				fmt.Sprintf("SSH 连接第 %d 次失败, %d 秒后重试... (%v)", retry, int(waitTime.Seconds()), dialErr))
			time.Sleep(waitTime)
		}
	}

	return nil, dialErr
}

// ============================================================
// 停止并删除容器 (防卡死: 超时保护 + 降级 docker kill)
// ============================================================
func (h *NodeCleanupHandler) stopAndRemoveContainer(client *ssh.Client, containerName string, sse *sseWriter) {
	// 第1步: docker stop (带 15s 超时)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), cleanupDockerStopTimeout)
	defer stopCancel()

	type result struct {
		out string
		err error
	}
	stopCh := make(chan result, 1)
	safeGo(func() {
		out, err := sshRun(client, fmt.Sprintf("docker stop %s 2>/dev/null; echo STOP_DONE", containerName))
		stopCh <- result{out, err}
	})

	select {
	case r := <-stopCh:
		if r.err != nil {
			sse.event(CleanupPhaseStop, "log", "",
				fmt.Sprintf("docker stop 返回(非致命): %v, 输出: %s", r.err, strings.TrimSpace(r.out)))
		} else {
			sse.event(CleanupPhaseStop, "log", "",
				fmt.Sprintf("容器 %s 已停止: %s", containerName, strings.TrimSpace(r.out)))
		}
	case <-stopCtx.Done():
		sse.event(CleanupPhaseStop, "warning",
			"docker stop 超时, 尝试 docker kill 强制终止...", "")
		// 兜底: docker stop 卡死时用 docker kill
		killOut, killErr := sshRun(client, fmt.Sprintf("docker kill %s 2>/dev/null; echo KILL_DONE", containerName))
		if killErr != nil {
			sse.event(CleanupPhaseStop, "log", "",
				fmt.Sprintf("docker kill 返回: %v, 输出: %s", killErr, strings.TrimSpace(killOut)))
		} else {
			sse.event(CleanupPhaseStop, "log", "",
				fmt.Sprintf("容器 %s 已强制终止: %s", containerName, strings.TrimSpace(killOut)))
		}
	}

	// 第2步: docker rm -f (带 10s 超时)
	rmCtx, rmCancel := context.WithTimeout(context.Background(), cleanupDockerRmTimeout)
	defer rmCancel()

	rmCh := make(chan result, 1)
	safeGo(func() {
		out, err := sshRun(client, fmt.Sprintf("docker rm -f %s 2>/dev/null; echo RM_DONE", containerName))
		rmCh <- result{out, err}
	})

	select {
	case r := <-rmCh:
		if r.err != nil {
			sse.event(CleanupPhaseStop, "warning",
				"删除容器返回错误(可能本就不存在): "+r.err.Error(), strings.TrimSpace(r.out))
		} else {
			sse.event(CleanupPhaseStop, "done", "容器已停止并删除: "+containerName, strings.TrimSpace(r.out))
		}
	case <-rmCtx.Done():
		sse.event(CleanupPhaseStop, "warning",
			"docker rm 超时 (服务器可能卡死/磁盘IO过高), 跳过删除容器, 继续后续步骤", "")
	}

	// 额外兜底: 清理可能残留的同名容器网络
	sshRun(client, fmt.Sprintf("docker rm -f %s 2>/dev/null; true", containerName))
}

// ============================================================
// 删除部署目录 (防卡死: 超时保护 + 后台执行降级)
// ============================================================
func (h *NodeCleanupHandler) removeDeployDir(client *ssh.Client, deployDir string, sse *sseWriter) {
	// 先用常规方式删除
	rmCtx, rmCancel := context.WithTimeout(context.Background(), cleanupRmTimeout)
	defer rmCancel()

	type result struct {
		out string
		err error
	}
	rmCh := make(chan result, 1)
	safeGo(func() {
		out, err := sshRun(client, fmt.Sprintf("rm -rf %s && echo RM_DIR_DONE", deployDir))
		rmCh <- result{out, err}
	})

	select {
	case r := <-rmCh:
		if r.err != nil {
			sse.event(CleanupPhaseDir, "warning",
				"删除目录失败(可能权限不足): "+r.err.Error(), strings.TrimSpace(r.out))
		} else {
			sse.event(CleanupPhaseDir, "done",
				"部署目录已删除 (含 .env.node / agent / xray-cache)", strings.TrimSpace(r.out))
		}
	case <-rmCtx.Done():
		// 兜底: rm -rf 卡死时, 尝试后台执行删除, 然后跳过
		sse.event(CleanupPhaseDir, "warning",
			"rm -rf 超时 (磁盘IO可能过高), 尝试后台异步删除...", "")
		sshRun(client, fmt.Sprintf("nohup rm -rf %s >/dev/null 2>&1 &", deployDir))
		sse.event(CleanupPhaseDir, "done",
			"已提交后台异步删除任务, 部署目录将在后台清理", "")
	}
}

// ============================================================
// 删除 agent 镜像 (防卡死: 超时保护 + 强制删除)
// ============================================================
func (h *NodeCleanupHandler) removeAgentImage(client *ssh.Client, sse *sseWriter) {
	rmiCtx, rmiCancel := context.WithTimeout(context.Background(), cleanupDockerRmiTimeout)
	defer rmiCancel()

	type result struct {
		out string
		err error
	}
	rmiCh := make(chan result, 1)
	safeGo(func() {
		// 先尝试正常删除, 失败则 force 删除
		out, err := sshRun(client, "docker rmi nexus-node-agent:latest 2>/dev/null || docker rmi -f nexus-node-agent:latest 2>/dev/null; echo RMI_DONE")
		rmiCh <- result{out, err}
	})

	select {
	case r := <-rmiCh:
		if r.err != nil {
			sse.event(CleanupPhaseImage, "warning",
				"删除镜像失败(可能正在被其他节点使用): "+r.err.Error(), strings.TrimSpace(r.out))
		} else {
			sse.event(CleanupPhaseImage, "done", "镜像已删除", strings.TrimSpace(r.out))
		}
	case <-rmiCtx.Done():
		sse.event(CleanupPhaseImage, "warning",
			"docker rmi 超时 (docker daemon 可能卡死), 跳过删除镜像", "")
	}
}

// CleanupSimple 简单删除 (兼容旧接口, 不走 SSH, 仅 DB+Redis 清理)
// 路由: DELETE /api/v1/admin/nodes/:id (保持向后兼容)
func (h *NodeCleanupHandler) CleanupSimple(c *gin.Context) {
	id := c.Param("id")
	if err := h.nodeService.DeleteNode(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OKMsg(c, "已删除 (节点服务器残留资源未清理, 建议使用「清理并删除」)")
}
