package handler

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"

	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/security"
)

// AutoDeployHandler 一键部署处理器
type AutoDeployHandler struct {
	nodeRepo *repo.NodeRepo
	jwtMgr   *security.JWTManager
}

// NewAutoDeployHandler 创建一键部署处理器
func NewAutoDeployHandler(nodeRepo *repo.NodeRepo, jwtMgr *security.JWTManager) *AutoDeployHandler {
	return &AutoDeployHandler{
		nodeRepo: nodeRepo,
		jwtMgr:   jwtMgr,
	}
}

// Deploy POST /api/v1/admin/nodes/:id/auto-deploy
// 一键部署节点 agent
func (h *AutoDeployHandler) Deploy(c *gin.Context) {
	nodeID := c.Param("id")

	// 查询节点
	node, err := h.nodeRepo.GetByID(nodeID)
	if err != nil {
		response.FailMsg(c, response.CodeNotFound, "节点不存在")
		return
	}

	// 解析节点配置
	sshUser := "root"
	sshPort := 22
	sshPassword := ""
	sshKeyPath := ""

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
		response.FailMsg(c, response.CodeParamError, "SSH 认证凭据未配置，请先在节点配置中填写 SSH 密码或密钥路径")
		return
	}

	sshConfig := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", node.ServerAddress, sshPort)
	sshClient, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, fmt.Sprintf("SSH 连接失败: %v", err))
		return
	}
	defer sshClient.Close()

	// 执行部署脚本
	// 下载 node_agent 二进制并启动 systemd 服务
	deployScript := fmt.Sprintf(`#!/bin/bash
set -e

echo ">>> Nexus-Panel 节点一键部署开始 <<<"

# 创建目录
mkdir -p /opt/nexus-node

# 下载 node_agent
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    BIN_URL="https://github.com/nexus-panel/nexus-panel/releases/latest/download/node_agent_linux_amd64"
elif [ "$ARCH" = "aarch64" ]; then
    BIN_URL="https://github.com/nexus-panel/nexus-panel/releases/latest/download/node_agent_linux_arm64"
else
    echo "不支持的架构: $ARCH"
    exit 1
fi

echo ">>> 下载 node_agent..."
curl -fsSL "$BIN_URL" -o /opt/nexus-node/node_agent
chmod +x /opt/nexus-node/node_agent

# 创建 systemd 服务
cat > /etc/systemd/system/nexus-node.service <<'SERVICE_EOF'
[Unit]
Description=Nexus Panel Node Agent
After=network.target

[Service]
Type=simple
ExecStart=/opt/nexus-node/node_agent
Restart=always
RestartSec=10
Environment=NODE_TOKEN=%s
Environment=GRPC_PORT=%d
Environment=PANEL_ADDR=%s

[Install]
WantedBy=multi-user.target
SERVICE_EOF

# 启动服务
systemctl daemon-reload
systemctl enable nexus-node
systemctl start nexus-node

echo ">>> 部署完成，节点已启动 <<<"
`, node.NodeToken, node.GrpcPort, "面板服务器地址")

	session, err := sshClient.NewSession()
	if err != nil {
		response.FailMsg(c, response.CodeServerError, fmt.Sprintf("创建 SSH 会话失败: %v", err))
		return
	}
	defer session.Close()

	output, err := session.CombinedOutput(deployScript)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, fmt.Sprintf("部署脚本执行失败: %v\n%s", err, string(output)))
		return
	}

	response.OK(c, gin.H{
		"node_id": nodeID,
		"output":  string(output),
		"message": "节点部署成功",
	})
}