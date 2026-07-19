package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/security"
	"golang.org/x/crypto/ssh"
)

// SSH WebSocket 终端：前端通过 WebSocket 连接面板，面板代理 SSH 到节点服务器
// 协议：
//   前端->后端: 第一条 TextMessage=JSON认证 {password,username,port,cols,rows}
//               之后 BinaryMessage=终端输入, TextMessage(JSON)=控制(resize)
//   后端->前端: TextMessage(JSON)=事件(ready/error/closed), BinaryMessage=终端输出

// sshAllowedOrigins 从环境变量 SSH_TERMINAL_ALLOWED_ORIGINS (逗号分隔) 读取允许的 Origin。
// 未配置时回退到本地开发常用 Origin。生产环境应通过环境变量配置面板实际域名,
// 例如: SSH_TERMINAL_ALLOWED_ORIGINS=https://panel.example.com,https://panel2.example.com
func sshAllowedOrigins() []string {
	if v := strings.TrimSpace(os.Getenv("SSH_TERMINAL_ALLOWED_ORIGINS")); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"http://localhost", "http://127.0.0.1", "https://localhost", "https://127.0.0.1"}
}

var sshUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// [S4 fix 2026-07-14] WebSocket CheckOrigin whitelist
		// [P1 fix 2026-07-19] 支持从环境变量配置生产域名, 避免生产环境 403
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false  // [P0#2 2026-07-14] 拒绝空 Origin,防 CSRF 绕过
		}
		for _, a := range sshAllowedOrigins() {
			if origin == a || origin == a+":8080" || origin == a+":80" || origin == a+":443" || origin == a+":5173" {
				return true
			}
			// 也允许 origin 直接等于配置项(用户可在环境变量里写完整带端口的 origin)
		}
		// 同时允许 origin 的 host 部分等于请求 Host(同源场景, 反向代理后常见)
		if u, err := url.Parse(origin); err == nil && u.Host == r.Host {
			return true
		}
		log.Printf("[ssh_terminal] reject websocket origin=%s remote=%s", origin, r.RemoteAddr)
		return false
	},
	HandshakeTimeout: 15 * time.Second,
	ReadBufferSize:   4096,
	WriteBufferSize:  4096,
}

type SSHTerminalHandler struct {
	nodeRepo *repo.NodeRepo
	jwtMgr   *security.JWTManager
}

func NewSSHTerminalHandler(nodeRepo *repo.NodeRepo, jwt *security.JWTManager) *SSHTerminalHandler {
	return &SSHTerminalHandler{nodeRepo: nodeRepo, jwtMgr: jwt}
}

type sshAuthMsg struct {
	Password string `json:"password"`
	Username string `json:"username"`
	Port     int    `json:"port"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
}

// safeConn 线程安全的 WebSocket 写入
type safeConn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (s *safeConn) writeBinary(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ws.WriteMessage(websocket.BinaryMessage, data)
}

func (s *safeConn) writeJSON(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ws.WriteJSON(v)
}

func (h *SSHTerminalHandler) Terminal(c *gin.Context) {
	// 验证 JWT (query token，WebSocket 无法设置 header)
	token := c.Query("token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "缺少 token"})
		return
	}
	claims, err := h.jwtMgr.ValidateAccess(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "token 无效"})
		return
	}
	if claims.Role != security.RoleAdmin && claims.Role != "super_admin" {
		c.JSON(http.StatusForbidden, gin.H{"msg": "无权限"})
		return
	}

	nodeID := c.Param("id")
	node, err := h.nodeRepo.GetByID(nodeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"msg": "节点不存在"})
		return
	}

	ws, err := sshUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	sc := &safeConn{ws: ws}

	// 读取第一条认证消息
	ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		sc.writeJSON(gin.H{"type": "error", "msg": "未收到认证信息"})
		return
	}
	ws.SetReadDeadline(time.Time{})

	var auth sshAuthMsg
	if err := json.Unmarshal(msg, &auth); err != nil || auth.Password == "" {
		sc.writeJSON(gin.H{"type": "error", "msg": "认证信息格式错误或缺密码"})
		return
	}
	if auth.Username == "" {
		auth.Username = "root"
	}
	if auth.Port == 0 {
		auth.Port = 22
	}
	if auth.Cols == 0 {
		auth.Cols = 100
	}
	if auth.Rows == 0 {
		auth.Rows = 28
	}

	sshConfig := &ssh.ClientConfig{
		User:            auth.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(auth.Password)},
		// [P0#1 2026-07-14] 严格 known_hosts 模式: 拒绝未在白名单中的主机
		// 首次部署需执行: ssh-keyscan -H <host> >> /etc/nexus-panel/ssh_known_hosts
		HostKeyCallback: loadStrictHostKey("/etc/nexus-panel/ssh_known_hosts"),
		Timeout:         15 * time.Second,
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", node.ServerAddress, auth.Port), sshConfig)
	if err != nil {
		sc.writeJSON(gin.H{"type": "error", "msg": "SSH 连接失败: " + err.Error()})
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		sc.writeJSON(gin.H{"type": "error", "msg": "创建会话失败: " + err.Error()})
		return
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", auth.Rows, auth.Cols, modes); err != nil {
		sc.writeJSON(gin.H{"type": "error", "msg": "请求 PTY 失败: " + err.Error()})
		return
	}
	stdin, _ := session.StdinPipe()
	stdout, _ := session.StdoutPipe()
	stderr, _ := session.StderrPipe()

	if err := session.Shell(); err != nil {
		sc.writeJSON(gin.H{"type": "error", "msg": "启动 shell 失败: " + err.Error()})
		return
	}

	sc.writeJSON(gin.H{"type": "ready", "msg": fmt.Sprintf("已连接到节点「%s」(%s)  输入 exit 或 Ctrl+D 退出", node.Name, node.ServerAddress)})

	// stdout/stderr -> ws (BinaryMessage)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				sc.writeBinary(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				sc.writeBinary(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	// ws -> stdin
	go func() {
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				stdin.Close()
				return
			}
			if mt == websocket.TextMessage {
				var ctrl struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" {
					session.WindowChange(ctrl.Rows, ctrl.Cols)
					continue
				}
			}
			stdin.Write(data)
		}
	}()

	err = session.Wait()
	if err != nil {
		sc.writeJSON(gin.H{"type": "closed", "msg": "会话结束: " + err.Error()})
	} else {
		sc.writeJSON(gin.H{"type": "closed", "msg": "会话已正常结束"})
	}
}
