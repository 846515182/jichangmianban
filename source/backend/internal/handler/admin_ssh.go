package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"

	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// SSHTerminalHandler SSH WebSocket 终端处理器
type SSHTerminalHandler struct {
	nodeRepo *repo.NodeRepo
	jwtMgr   *security.JWTManager
}

// NewSSHTerminalHandler 创建 SSH 终端处理器
func NewSSHTerminalHandler(nodeRepo *repo.NodeRepo, jwtMgr *security.JWTManager) *SSHTerminalHandler {
	return &SSHTerminalHandler{
		nodeRepo: nodeRepo,
		jwtMgr:   jwtMgr,
	}
}

// Terminal GET /api/v1/admin/nodes/:id/terminal (WebSocket)
// 通过 WebSocket 提供 SSH 终端功能
func (h *SSHTerminalHandler) Terminal(c *gin.Context) {
	nodeID := c.Param("id")
	token := c.Query("token")

	// 验证 JWT token
	if token == "" {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}
	claims, err := h.jwtMgr.ValidateAccess(token)
	if err != nil {
		response.Fail(c, response.CodeTokenInvalid)
		return
	}
	if claims.Role != security.RoleAdmin {
		response.Fail(c, response.CodeNoPermission)
		return
	}

	// 查询节点
	node, err := h.nodeRepo.GetByID(nodeID)
	if err != nil {
		response.FailMsg(c, response.CodeNotFound, "节点不存在")
		return
	}

	// 升级为 WebSocket
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 从节点配置中获取 SSH 凭据
	sshUser := "root"
	sshPort := 22
	sshPassword := ""
	sshKeyPath := ""

	// 解析 server_config 中的 SSH 配置
	if node.ServerConfig != nil {
		var cfg map[string]interface{}
		if err := json.Unmarshal(node.ServerConfig, &cfg); err == nil {
			if u, ok := cfg["ssh_user"].(string); ok && u != "" {
				sshUser = u
			}
			if p, ok := cfg["ssh_port"].(float64); ok {
				sshPort = int(p)
			}
			if pw, ok := cfg["ssh_password"].(string); ok {
				sshPassword = pw
			}
			if kp, ok := cfg["ssh_key_path"].(string); ok {
				sshKeyPath = kp
			}
		}
	}

	// 配置 SSH 认证
	var authMethods []ssh.AuthMethod
	if sshPassword != "" {
		authMethods = append(authMethods, ssh.Password(sshPassword))
	}
	if sshKeyPath != "" {
		key, err := os.ReadFile(sshKeyPath)
		if err == nil {
			signer, err := ssh.ParsePrivateKey(key)
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}
	if len(authMethods) == 0 {
		conn.WriteMessage(websocket.TextMessage, []byte("SSH 认证凭据未配置\r\n"))
		return
	}

	sshConfig := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", node.ServerAddress, sshPort)
	sshClient, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("SSH 连接失败: %v\r\n", err)))
		return
	}
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("创建 SSH 会话失败: %v\r\n", err)))
		return
	}
	defer session.Close()

	// 获取 PTY
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", 40, 80, modes); err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("请求 PTY 失败: %v\r\n", err)))
		return
	}

	// 获取 stdin/stdout/stderr
	stdinPipe, _ := session.StdinPipe()
	stdoutPipe, _ := session.StdoutPipe()
	stderrPipe, _ := session.StderrPipe()

	// 启动 shell
	if err := session.Shell(); err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("启动 shell 失败: %v\r\n", err)))
		return
	}

	// 读取 stdout/stderr 并发送到 WebSocket
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				conn.WriteMessage(websocket.TextMessage, buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				conn.WriteMessage(websocket.TextMessage, buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	// 读取 WebSocket 消息并写入 stdin
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			stdinPipe.Write(msg)
		}
	}()

	<-done
}