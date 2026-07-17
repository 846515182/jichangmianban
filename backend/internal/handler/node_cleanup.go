package handler

import (
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
// ============================================================

// 清理阶段常量 (5 步)
const (
	CleanupPhaseConnect  = "connect"  // 1. SSH 连接节点服务器
	CleanupPhaseStop     = "stop"     // 2. 停止并删除 agent 容器
	CleanupPhaseDir      = "dir"      // 3. 删除部署目录
	CleanupPhaseImage    = "image"    // 4. 删除 agent 镜像(可选)
	CleanupPhaseFinalize = "finalize" // 5. DB 软删 + Redis 清理
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
			sse.event(CleanupPhaseFinalize, "running", "正在执行面板侧清理 (DB+Redis)...", "")
			if err := h.nodeService.DeleteNode(nodeID); err != nil {
				sse.eventWithCode(CleanupPhaseFinalize, "warning",
					"面板侧清理失败: "+err.Error()+" (节点服务器资源已清理, 可稍后重试)", "", "")
			} else {
				sse.event(CleanupPhaseFinalize, "done", "面板侧清理完成", "")
			}
		}
		sse.event("finish", "done", "节点清理完成", "")
		if f, ok := c.Writer.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(100 * time.Millisecond)
	}()

	// ====== Phase 1: SSH 连接节点服务器 ======
	// 没有密码则跳过 SSH 清理, 直接走 DB 删除
	if password == "" {
		sse.event(CleanupPhaseConnect, "warning", "未提供 SSH 密码, 跳过节点服务器清理, 仅执行面板侧删除", "")
		return
	}
	sse.event(CleanupPhaseConnect, "running",
		fmt.Sprintf("正在连接节点服务器 %s:%d...", node.ServerAddress, req.Port), "")

	sshConfig := &ssh.ClientConfig{
		User:            req.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: hostKeyCallback(node.ServerAddress),
		Timeout:         15 * time.Second,
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
	addr := fmt.Sprintf("%s:%d", node.ServerAddress, req.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		// SSH 连不上不阻断: 节点可能已下线/重装系统, DB 删除仍需执行
		sse.eventWithCode(CleanupPhaseConnect, "warning",
			"SSH 连接失败: "+err.Error()+" (节点服务器资源未清理, 仅执行面板侧删除)", "",
			classifySSHError(err.Error()))
		return
	}
	defer client.Close()
	sse.event(CleanupPhaseConnect, "done", "SSH 连接成功", "")

	// ====== 计算 shortID (与部署时一致) ======
	shortID := node.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	containerName := "nexus-agent-" + shortID
	deployDir := "/root/node-agent-" + shortID

	// ====== Phase 2: 停止并删除 agent 容器 ======
	sse.event(CleanupPhaseStop, "running", "正在停止并删除 agent 容器 "+containerName+"...", "")
	stopOut, stopErr := sshRun(client, fmt.Sprintf(
		"docker stop %s 2>/dev/null; docker rm -f %s 2>/dev/null; echo DONE", containerName, containerName))
	if stopErr != nil {
		// 容器可能本就不存在(docker stop 对不存在容器返回非0), 不阻断
		sse.event(CleanupPhaseStop, "warning",
			"停止容器返回错误(可能本就不存在): "+stopErr.Error(), strings.TrimSpace(stopOut))
	} else {
		sse.event(CleanupPhaseStop, "done", "容器已停止并删除", strings.TrimSpace(stopOut))
	}

	// ====== Phase 3: 删除部署目录 ======
	sse.event(CleanupPhaseDir, "running", "正在删除部署目录 "+deployDir+"...", "")
	dirOut, dirErr := sshRun(client, fmt.Sprintf("rm -rf %s && echo DONE", deployDir))
	if dirErr != nil {
		sse.event(CleanupPhaseDir, "warning",
			"删除目录失败(可能权限不足): "+dirErr.Error(), strings.TrimSpace(dirOut))
	} else {
		sse.event(CleanupPhaseDir, "done", "部署目录已删除 (含 .env.node / agent / xray-cache)", strings.TrimSpace(dirOut))
	}

	// ====== Phase 4: 删除 docker 镜像 (可选) ======
	if req.RemoveImg {
		sse.event(CleanupPhaseImage, "running", "正在删除 agent 镜像 nexus-node-agent:latest...", "")
		imgOut, imgErr := sshRun(client, "docker rmi nexus-node-agent:latest 2>/dev/null; echo DONE")
		if imgErr != nil {
			sse.event(CleanupPhaseImage, "warning",
				"删除镜像失败(可能正在被其他节点使用): "+imgErr.Error(), strings.TrimSpace(imgOut))
		} else {
			sse.event(CleanupPhaseImage, "done", "镜像已删除", strings.TrimSpace(imgOut))
		}
	} else {
		sse.event(CleanupPhaseImage, "done", "跳过镜像删除 (如需删除请勾选)", "")
	}

	// ====== Phase 5: 面板侧 DB + Redis 清理 ======
	sse.event(CleanupPhaseFinalize, "running", "正在执行面板侧清理 (DB软删 + Redis)...", "")
	if err := h.nodeService.DeleteNode(nodeID); err != nil {
		sse.eventWithCode(CleanupPhaseFinalize, "error",
			"面板侧清理失败: "+err.Error(), "", "")
		// 即使 DB 删除失败, 节点服务器资源已清理, 仍标记完成
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
