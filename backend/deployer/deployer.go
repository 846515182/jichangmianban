package deployer

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ====== 部署阶段枚举 ======

type Stage int

const (
	StageConnect        Stage = iota + 1
	StageEnvCheck
	StageInstallDocker
	StagePrepareFiles
	StageCompile
	StageGRPCPreCheck
	StageStartService
	StageVerify
)

func (s Stage) String() string {
	names := map[Stage]string{
		StageConnect:        "连接节点服务器",
		StageEnvCheck:       "环境检测",
		StageInstallDocker:  "安装 Docker",
		StagePrepareFiles:   "准备部署文件",
		StageCompile:        "编译程序",
		StageGRPCPreCheck:   "gRPC 连通性预检",
		StageStartService:   "启动服务",
		StageVerify:         "验证完成",
	}
	if name, ok := names[s]; ok {
		return name
	}
	return "未知阶段"
}

// ====== 部署错误码 ======

const (
	ErrDockerNotInstalled = "DOCKER_NOT_INSTALLED"
	ErrPortConflict       = "PORT_CONFLICT"
	ErrDiskFull           = "DISK_FULL"
	ErrMemoryLow          = "MEMORY_LOW"
	ErrSSHConnect         = "SSH_CONNECT_FAIL"
	ErrSSHAuth            = "SSH_AUTH_FAIL"
	ErrSSHTimeout         = "SSH_TIMEOUT"
	ErrBuild              = "BUILD_FAIL"
	ErrTransfer           = "TRANSFER_FAIL"
	ErrStart              = "START_FAIL"
	ErrVerify             = "VERIFY_FAIL"
	ErrUnknown            = "UNKNOWN"
)

func fixSuggestion(errCode string) string {
	suggestions := map[string]string{
		ErrDockerNotInstalled: "1) 内核可能不支持: uname -r\n2) 检查 cgroup: mount | grep cgroup\n3) 手动安装: curl -fsSL https://get.docker.com | sh",
		ErrPortConflict:       "1) 释放端口: fuser -k <端口>/tcp\n2) 修改节点代理端口",
		ErrDiskFull:           "1) 清理磁盘: docker system prune -a\n2) 清理大文件: du -sh /* | sort -h",
		ErrMemoryLow:          "1) 升级内存(推荐 >=1GB)\n2) 创建 swap: fallocate -l 2G /swapfile && mkswap /swapfile && swapon /swapfile",
		ErrSSHConnect:         "1) 检查 IP 是否正确\n2) 检查防火墙是否放行 SSH 端口",
		ErrSSHAuth:            "1) 确认密码正确\n2) 检查 sshd_config: PasswordAuthentication yes",
		ErrSSHTimeout:         "1) ping <节点IP>\n2) telnet <节点IP> 22",
		ErrBuild:              "1) 检查 node_agent 源码完整性\n2) go mod tidy",
		ErrTransfer:           "1) 检查网络稳定性\n2) 确认磁盘有足够空间",
		ErrStart:              "1) docker logs <容器名>\n2) 检查端口冲突",
		ErrVerify:             "1) docker logs <容器名>\n2) 检查 NODE_TOKEN 是否一致",
	}
	if s, ok := suggestions[errCode]; ok {
		return s
	}
	return "1) 查看完整部署日志\n2) 确认服务器满足最低要求 (1GB 内存, 1GB 磁盘)"
}

// ====== 日志条目 ======

type LogEntry struct {
	Stage     Stage  `json:"stage"`
	StageName string `json:"stage_name"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// ====== 部署配置 ======

type Config struct {
	NodeID       string
	Host         string
	Port         int
	User         string
	Password     string
	PrivateKey   string
	PanelGRPCAddr string
	NodeToken    string
	DeployDir    string
	ContainerName string
	ListenPort   int
	HealthPort   int
	GRPCPort     int
	PanelDomain  string
	PanelRealIP  string
	GRPCTLS      bool
	GRPCTLSCA    string
	NodeAgentPath string
	Timeout      time.Duration
}

// ====== 部署器 ======

type Deployer struct {
	cfg          Config
	client       *ssh.Client
	logChan      chan LogEntry
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	currentStage Stage
}

func New(cfg Config) *Deployer {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Minute
	}
	if cfg.User == "" {
		cfg.User = "root"
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.ListenPort == 0 {
		cfg.ListenPort = 443
	}
	if cfg.HealthPort == 0 {
		cfg.HealthPort = 50052
	}
	if cfg.GRPCPort == 0 {
		cfg.GRPCPort = 50051
	}
	shortID := cfg.NodeID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if cfg.ContainerName == "" {
		cfg.ContainerName = "nexus-agent-" + shortID
	}
	if cfg.DeployDir == "" {
		cfg.DeployDir = "/root/node-agent-" + shortID
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	return &Deployer{
		cfg:     cfg,
		logChan: make(chan LogEntry, 200),
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (d *Deployer) LogChan() <-chan LogEntry { return d.logChan }
func (d *Deployer) Ctx() context.Context     { return d.ctx }
func (d *Deployer) Config() Config          { return d.cfg }

func (d *Deployer) emitLog(level, format string, args ...interface{}) {
	entry := LogEntry{
		Stage:     d.currentStage,
		StageName: d.currentStage.String(),
		Level:     level,
		Message:   fmt.Sprintf(format, args...),
		Timestamp: time.Now().UnixMilli(),
	}
	select {
	case d.logChan <- entry:
	default:
	}
}

func (d *Deployer) setStage(s Stage) {
	d.mu.Lock()
	d.currentStage = s
	d.mu.Unlock()
	d.emitLog("info", "开始执行: %s", s.String())
}

// ====== 主部署流程 ======

func (d *Deployer) Deploy() (ok bool, errCode, errMsg string) {
	defer d.cancel()
	defer close(d.logChan)

	stages := []struct {
		stage Stage
		fn    func() (string, string)
	}{
		{StageConnect, d.phaseConnect},
		{StageEnvCheck, d.phaseEnvCheck},
		{StageInstallDocker, d.phaseInstallDocker},
		{StagePrepareFiles, d.phasePrepareFiles},
		{StageCompile, d.phaseCompile},
		{StageGRPCPreCheck, d.phaseGRPCPreCheck},
		{StageStartService, d.phaseStartService},
		{StageVerify, d.phaseVerify},
	}

	for _, s := range stages {
		select {
		case <-d.ctx.Done():
			d.emitLog("error", "部署超时")
			return false, ErrUnknown, "部署超时"
		default:
		}

		d.setStage(s.stage)
		if errCode, errMsg = s.fn(); errCode != "" {
			d.emitLog("error", "%s 失败: %s [%s]", s.stage.String(), errMsg, errCode)
			d.saveProgress("failed", errCode, errMsg)
			return false, errCode, errMsg
		}
		d.emitLog("success", "%s 完成 ✓", s.stage.String())
		d.saveProgress("running", "", "")
	}

	d.emitLog("success", "🎉 节点部署完成！")
	d.emitLog("info", "  服务器: %s", d.cfg.Host)
	d.emitLog("info", "  部署目录: %s", d.cfg.DeployDir)
	d.emitLog("info", "  容器: %s", d.cfg.ContainerName)
	d.saveProgress("success", "", "")
	return true, "", ""
}

func (d *Deployer) saveProgress(status, errCode, errMsg string) {
	log.Printf("[DeployProgress] NodeID=%s Stage=%d(%s) Status=%s ErrCode=%s Err=%s",
		d.cfg.NodeID, d.currentStage, d.currentStage.String(), status, errCode, errMsg)
}

// ====== Phase 1: SSH 连接 ======

func (d *Deployer) phaseConnect() (string, string) {
	d.emitLog("info", "正在连接 %s:%d...", d.cfg.Host, d.cfg.Port)

	var authMethods []ssh.AuthMethod
	if d.cfg.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(d.cfg.PrivateKey))
		if err != nil {
			return ErrSSHAuth, "SSH 私钥解析失败: " + err.Error()
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
		if d.cfg.Password != "" {
			authMethods = append(authMethods, ssh.Password(d.cfg.Password))
		}
	} else {
		authMethods = append(authMethods, ssh.Password(d.cfg.Password))
	}

	sshConfig := &ssh.ClientConfig{
		User:            d.cfg.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
		Config: ssh.Config{
			KeyExchanges: []string{
				"curve25519-sha256", "curve25519-sha256@libssh.org",
				"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
				"diffie-hellman-group14-sha256", "diffie-hellman-group14-sha1",
				"diffie-hellman-group16-sha512", "diffie-hellman-group18-sha512",
				"diffie-hellman-group-exchange-sha256",
			},
			Ciphers: []string{
				"aes256-gcm@openssh.com", "aes128-gcm@openssh.com",
				"aes256-ctr", "aes192-ctr", "aes128-ctr",
				"chacha20-poly1305@openssh.com",
			},
		},
	}

	addr := fmt.Sprintf("%s:%d", d.cfg.Host, d.cfg.Port)
	var lastErr error
	for retry := 1; retry <= 3; retry++ {
		client, err := ssh.Dial("tcp", addr, sshConfig)
		if err == nil {
			d.client = client
			break
		}
		lastErr = err
		if retry < 3 {
			d.emitLog("warn", "SSH 第 %d 次失败, %ds 后重试...", retry, retry*2)
			time.Sleep(time.Duration(retry*2) * time.Second)
		}
	}
	if lastErr != nil {
		return classifySSHError(lastErr.Error()), "SSH 连接失败: " + lastErr.Error()
	}

	d.emitLog("success", "SSH 连接成功")

	if out, err := d.exec("echo SSH_OK", 5*time.Second); err != nil || !strings.Contains(out, "SSH_OK") {
		return ErrSSHConnect, "SSH 会话不可用"
	}

	// SSH 保活
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-d.ctx.Done():
				return
			case <-ticker.C:
				if d.client != nil {
					d.client.SendRequest("keepalive@openssh.com", true, nil)
				}
			}
		}
	}()

	// 清理旧残留
	d.emitLog("info", "清理旧部署残留...")
	cleanScript := d.buildCleanScript()
	out, _ := d.exec(cleanScript, 60*time.Second)
	d.emitLog("info", out)
	return "", ""
}

func (d *Deployer) buildCleanScript() string {
	return fmt.Sprintf(`
set +e
DEPLOY_DIR="%s"
CONTAINER="%s"
echo "--- docker compose down ---"
cd "$DEPLOY_DIR" 2>/dev/null && docker compose -f docker-compose.node.yml down --timeout 10 2>/dev/null; true
echo "--- stop/rm container ---"
docker stop %s 2>/dev/null; docker rm -f %s 2>/dev/null; true
echo "--- kill ports ---"
fuser -k %d/tcp 2>/dev/null; fuser -k %d/tcp 2>/dev/null; fuser -k %d/tcp 2>/dev/null; true
echo "--- clean network ---"
docker network ls --filter name=nexus --format '{{.Name}}' 2>/dev/null | xargs -r docker network rm 2>/dev/null; true
echo "--- clean swap ---"
swapoff /swapfile_np_* 2>/dev/null; rm -f /swapfile_np_* 2>/dev/null; true
echo "--- clean hosts ---"
sed -i '/nexus-panel/d' /etc/hosts 2>/dev/null; true
echo "--- restore systemd ---"
rm -f /etc/systemd/system/docker.service.d/override.conf 2>/dev/null; systemctl daemon-reload 2>/dev/null; true
echo "--- rm deploy dir ---"
chmod -R +w "$DEPLOY_DIR" 2>/dev/null; rm -rf "$DEPLOY_DIR" 2>/dev/null; true
echo "CLEANED"
`, d.cfg.DeployDir, d.cfg.ContainerName, d.cfg.ContainerName, d.cfg.ContainerName,
		d.cfg.ListenPort, d.cfg.HealthPort, d.cfg.GRPCPort)
}

// ====== Phase 2: 环境检测 ======

func (d *Deployer) phaseEnvCheck() (string, string) {
	d.emitLog("info", "检测磁盘空间...")
	diskOut, _ := d.exec("df -BG / 2>/dev/null | tail -1 || df -h / 2>/dev/null | tail -1", 10*time.Second)
	if availGB := parseDiskAvailGB(diskOut); availGB >= 0 && availGB < 1 {
		return ErrDiskFull, fmt.Sprintf("磁盘仅 %dGB, 需要 >=1GB", availGB)
	}
	d.emitLog("success", "磁盘空间充足")

	d.emitLog("info", "检测内存...")
	memOut, _ := d.exec("free -m 2>/dev/null | head -2 || cat /proc/meminfo 2>/dev/null | head -3", 10*time.Second)
	if availMB := parseMemAvailMB(memOut); availMB >= 0 && availMB < 768 {
		d.emitLog("warn", "内存仅 %dMB, 创建 swap...", availMB)
		if swapOut, _ := d.exec(d.buildSwapScript(), 60*time.Second); !strings.Contains(swapOut, "SWAP_OK") {
			d.emitLog("warn", "swap 创建失败, 继续部署")
		} else {
			d.emitLog("success", "swap 已创建")
		}
	} else {
		d.emitLog("success", "内存充足 (%dMB)", availMB)
	}

	d.emitLog("info", "检测端口 %d/%d...", d.cfg.ListenPort, d.cfg.HealthPort)
	portCheck, _ := d.exec(fmt.Sprintf(
		"(ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null || lsof -i -P -n 2>/dev/null | grep LISTEN) | grep -E ':%d|:%d' || echo 'FREE'",
		d.cfg.ListenPort, d.cfg.HealthPort), 10*time.Second)
	if !strings.Contains(portCheck, "FREE") {
		return ErrPortConflict, fmt.Sprintf("端口 %d/%d 被占用", d.cfg.ListenPort, d.cfg.HealthPort)
	}
	d.emitLog("success", "端口正常")

	d.emitLog("info", "检测网络...")
	netOut, _ := d.exec("curl -sI --max-time 5 https://get.docker.com 2>&1 | head -1", 15*time.Second)
	if !strings.Contains(netOut, "HTTP/") {
		netOut2, _ := d.exec("ping -c 2 -W 3 8.8.8.8 2>&1 || echo 'PING_FAIL'", 15*time.Second)
		if strings.Contains(netOut2, "PING_FAIL") || strings.Contains(netOut2, "100% loss") {
			return ErrUnknown, "服务器无网络连接"
		}
		d.emitLog("warn", "网络受限(无法访问 Docker Hub), 将使用国内镜像源")
	} else {
		d.emitLog("success", "网络正常")
	}

	return "", ""
}

func (d *Deployer) buildSwapScript() string {
	return `
set -e
SWAP_FILE="/swapfile_np_$(date +%s | tail -c 6)"
AVAIL_KB=$(df / --output=avail | tail -1 | tr -d ' ')
if [ "$AVAIL_KB" -lt 1048576 ]; then echo "NO_SPACE"; exit 1; fi
fallocate -l 1G $SWAP_FILE 2>/dev/null || dd if=/dev/zero of=$SWAP_FILE bs=1M count=1024 2>/dev/null
chmod 600 $SWAP_FILE && mkswap $SWAP_FILE && swapon $SWAP_FILE && echo "SWAP_OK" || echo "SWAP_FAIL"
`
}

// ====== Phase 3: 安装 Docker ======

func (d *Deployer) phaseInstallDocker() (string, string) {
	d.emitLog("info", "检查 Docker 状态...")

	status := d.checkDockerStatus()
	d.emitLog("info", "Docker 状态: %s", status)

	switch status {
	case "running":
		d.emitLog("success", "Docker 已运行")
		return "", ""
	case "installed_broken":
		d.emitLog("info", "Docker 异常, 尝试修复...")
		if d.repairDocker() {
			if d.waitDockerReady(30) {
				d.emitLog("success", "Docker 修复成功")
				return "", ""
			}
		}
		d.emitLog("warn", "修复失败, 卸载重装...")
		d.uninstallDocker()
	}

	d.emitLog("info", "安装 Docker...")

	installers := []string{
		"curl -fsSL https://get.docker.com | sh",
		"curl -fsSL https://mirrors.aliyun.com/docker-ce/linux/static/stable/x86_64/docker-24.0.7.tgz -o /tmp/d.tgz && tar -xzf /tmp/d.tgz -C /usr/local/bin/ --strip-components=1 && curl -fsSL https://mirrors.aliyun.com/docker-ce/linux/static/stable/x86_64/docker-compose-linux-x86_64 -o /usr/local/bin/docker-compose && chmod +x /usr/local/bin/docker-compose && rm -f /tmp/d.tgz",
		"curl -fsSL https://mirrors.ustc.edu.cn/docker-ce/linux/static/stable/x86_64/docker-24.0.7.tgz -o /tmp/d.tgz && tar -xzf /tmp/d.tgz -C /usr/local/bin/ --strip-components=1 && rm -f /tmp/d.tgz",
	}

	var installed bool
	for _, installer := range installers {
		out, err := d.exec(installer, 180*time.Second)
		if err == nil && !strings.Contains(out, "INSTALL_FAIL") {
			installed = true
			break
		}
		d.emitLog("warn", "安装源不可用, 尝试下一个...")
	}

	if !installed {
		return ErrDockerNotInstalled, "Docker 安装失败 (已尝试 3 个源)"
	}

	d.createDockerSystemdService()
	d.exec("systemctl enable docker 2>/dev/null; systemctl restart docker 2>/dev/null; sleep 3", 30*time.Second)

	if !d.waitDockerReady(30) {
		if d.runFixScript() {
			d.emitLog("success", "Docker 一键修复成功")
			return "", ""
		}
		if diag := d.diagnoseDocker(); diag != "" {
			return ErrDockerNotInstalled, "Docker 无法启动: " + diag
		}
		return ErrDockerNotInstalled, "Docker daemon 未就绪"
	}

	d.emitLog("success", "Docker 安装完成")
	d.pullBaseImage()
	return "", ""
}

func (d *Deployer) checkDockerStatus() string {
	out, _ := d.exec(`
if command -v dockerd &>/dev/null || [ -f /usr/bin/dockerd ] || [ -f /usr/local/bin/dockerd ]; then
    if systemctl is-active --quiet docker 2>/dev/null; then echo "RUNNING"
    else echo "INSTALLED_BROKEN"; fi
else echo "NOT_INSTALLED"; fi
`, 10*time.Second)
	switch {
	case strings.Contains(out, "RUNNING"):
		return "running"
	case strings.Contains(out, "INSTALLED_BROKEN"):
		return "installed_broken"
	default:
		return "not_installed"
	}
}

func (d *Deployer) repairDocker() bool {
	script := `
set -e
DOCKERD_PATH=""
for p in /usr/bin/dockerd /usr/local/bin/dockerd /usr/sbin/dockerd; do
    if [ -x "$p" ]; then DOCKERD_PATH="$p"; break; fi
done
[ -z "$DOCKERD_PATH" ] && exit 1

SERVICE_BIN=$(systemctl cat docker 2>/dev/null | grep ExecStart= | head -1 | sed 's/.*ExecStart=//' | awk '{print $1}')
if [ "$SERVICE_BIN" != "$DOCKERD_PATH" ]; then
    mkdir -p /etc/systemd/system/docker.service.d
    cat > /etc/systemd/system/docker.service.d/override.conf << EOF
[Service]
ExecStart=
ExecStart=$DOCKERD_PATH -H fd:// --containerd=/run/containerd/containerd.sock
EOF
    systemctl daemon-reload
fi
systemctl unmask docker 2>/dev/null || true
systemctl restart docker 2>/dev/null
sleep 3
systemctl is-active --quiet docker && echo "REPAIRED_OK" || echo "REPAIR_FAIL"
`
	out, _ := d.exec(script, 60*time.Second)
	return strings.Contains(out, "REPAIRED_OK")
}

func (d *Deployer) uninstallDocker() {
	d.exec(`
systemctl stop docker containerd 2>/dev/null
apt-get purge -y docker-ce docker-ce-cli containerd.io 2>/dev/null || true
rm -rf /var/lib/docker /var/lib/containerd
rm -f /etc/systemd/system/docker.service.d/override.conf
systemctl daemon-reload
`, 60*time.Second)
}

func (d *Deployer) waitDockerReady(timeout int) bool {
	for i := 0; i < timeout; i++ {
		out, _ := d.exec("docker info 2>&1 | grep -E '^(Server:|Containers:)' | head -1", 5*time.Second)
		if strings.HasPrefix(strings.TrimSpace(out), "Server:") || strings.HasPrefix(strings.TrimSpace(out), "Containers:") {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

func (d *Deployer) createDockerSystemdService() {
	d.exec(`
cat > /etc/systemd/system/containerd.service << 'EOF'
[Unit]
Description=containerd
After=network.target
[Service]
ExecStart=/usr/local/bin/containerd
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
EOF
cat > /etc/systemd/system/docker.service << 'EOF'
[Unit]
Description=Docker
After=network.target containerd.service
Requires=containerd.service
[Service]
ExecStart=/usr/local/bin/dockerd -H fd:// --containerd=/run/containerd/containerd.sock
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable containerd docker 2>/dev/null
`, 10*time.Second)
}

func (d *Deployer) runFixScript() bool {
	script := `DOCKERD_PATH=""
for p in /usr/bin/dockerd /usr/local/bin/dockerd; do if [ -f "$p" ]; then DOCKERD_PATH="$p"; break; fi; done
if [ -z "$DOCKERD_PATH" ]; then DOCKERD_PATH=$(find / -name dockerd -type f 2>/dev/null | head -1); fi
if [ -z "$DOCKERD_PATH" ]; then curl -fsSL https://get.docker.com | sh; DOCKERD_PATH=$(which dockerd 2>/dev/null || echo "/usr/bin/dockerd"); fi
if [ ! -f /usr/bin/dockerd ] && [ -f /usr/local/bin/dockerd ]; then ln -sf /usr/local/bin/dockerd /usr/bin/dockerd; systemctl daemon-reload; fi
if [ ! -f /etc/systemd/system/docker.service ]; then cat > /etc/systemd/system/docker.service << EOF
[Unit]
Description=Docker
After=network-online.target
[Service]
ExecStart=${DOCKERD_PATH} -H fd:// --containerd=/run/containerd/containerd.sock
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
EOF
fi
systemctl unmask docker docker.socket 2>/dev/null || true
systemctl daemon-reload
mkdir -p /etc/docker
cat > /etc/docker/daemon.json << 'EOF'
{"storage-driver":"overlay2","log-driver":"json-file","log-opts":{"max-size":"10m","max-file":"3"},"live-restore":true}
EOF
systemctl enable docker 2>/dev/null || true
systemctl restart docker
for i in $(seq 1 15); do if docker info >/dev/null 2>&1; then echo "DOCKER_OK"; exit 0; fi; sleep 1; done
echo "DOCKER_FAIL"; exit 1`
	out, _ := d.exec(script, 60*time.Second)
	return strings.Contains(out, "DOCKER_OK")
}

func (d *Deployer) diagnoseDocker() string {
	out, _ := d.exec(`
echo "--- kernel ---"; uname -r
echo "--- cgroup ---"; mount | grep -E 'cgroup |cgroup2' | head -3 || echo "none"
echo "--- overlay ---"; lsmod | grep overlay || echo "missing"
echo "--- containerd ---"; ps aux | grep containerd | grep -v grep | head -1 || echo "dead"
echo "--- dockerd log ---"; tail -10 /var/log/dockerd.log 2>/dev/null || echo "no log"
`, 10*time.Second)

	kernelVer := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "--- kernel ---") {
			continue
		}
		if strings.HasPrefix(line, "--- ") {
			continue
		}
		if strings.Contains(line, "cgroup") && strings.Contains(line, "none") {
			return fmt.Sprintf("内核 %s 不支持 cgroup", kernelVer)
		}
		if strings.Contains(line, "overlay") && strings.Contains(line, "missing") {
			return fmt.Sprintf("内核 %s 缺少 overlay 模块", kernelVer)
		}
		if strings.Contains(line, "containerd") && strings.Contains(line, "dead") {
			return "containerd 未运行"
		}
	}
	return ""
}

func (d *Deployer) pullBaseImage() {
	out, _ := d.exec("docker pull alpine:3.19 2>&1 | tail -3", 120*time.Second)
	d.emitLog("info", "镜像拉取: %s", strings.TrimSpace(out))
}

// ====== Phase 4: 准备文件 ======

func (d *Deployer) phasePrepareFiles() (string, string) {
	d.emitLog("info", "准备部署文件到 %s...", d.cfg.DeployDir)

	d.exec(fmt.Sprintf("mkdir -p %s", d.cfg.DeployDir), 5*time.Second)

	if err := d.writeRemoteFile(d.cfg.DeployDir+"/.env.node", d.buildEnvContent()); err != nil {
		return ErrTransfer, "写入 .env.node 失败: " + err.Error()
	}

	if err := d.writeRemoteFile(d.cfg.DeployDir+"/docker-compose.node.yml", d.buildComposeContent()); err != nil {
		return ErrTransfer, "写入 compose 失败: " + err.Error()
	}

	return "", ""
}

func (d *Deployer) buildEnvContent() string {
	cfg := d.cfg
	return fmt.Sprintf(`CONTAINER_NAME=%s
PANEL_GRPC_ADDR=%s:%d
NODE_TOKEN=%s
LISTEN_PORT=%d
HEALTH_PORT=%d
XRAY_VERSION=v26.6.1
`, cfg.ContainerName, cfg.PanelGRPCAddr, cfg.GRPCPort, cfg.NodeToken, cfg.ListenPort, cfg.HealthPort)
}

func (d *Deployer) buildComposeContent() string {
	cfg := d.cfg
	return fmt.Sprintf(`version: "3.8"
services:
  nexus-agent:
    image: alpine:3.19
    container_name: %s
    restart: unless-stopped
    network_mode: host
    volumes:
      - %s/agent:/usr/local/bin/agent:ro
      - %s/.env.node:/etc/nexus/.env.node:ro
    entrypoint: ["/usr/local/bin/agent"]
    environment:
      - NODE_TOKEN=${NODE_TOKEN}
      - PANEL_GRPC_ADDR=${PANEL_GRPC_ADDR}
`, cfg.ContainerName, cfg.DeployDir, cfg.DeployDir)
}

// ====== Phase 5: 编译 ======

func (d *Deployer) phaseCompile() (string, string) {
	d.emitLog("info", "检查二进制缓存...")

	hashOut, _ := d.execLocal(fmt.Sprintf(
		"find %s -name '*.go' -o -name 'go.mod' -o -name 'go.sum' 2>/dev/null | sort | xargs md5sum 2>/dev/null | md5sum | cut -d' ' -f1",
		d.cfg.NodeAgentPath))
	hash := strings.TrimSpace(hashOut)

	checkOut, _ := d.execLocal(fmt.Sprintf(
		"if [ -f %s/agent ] && [ -f %s/agent.hash ] && [ \"$(cat %s/agent.hash)\" = '%s' ]; then echo 'CACHED'; else echo 'NEED_BUILD'; fi",
		d.cfg.NodeAgentPath, d.cfg.NodeAgentPath, d.cfg.NodeAgentPath, hash))

	if strings.Contains(checkOut, "CACHED") {
		d.emitLog("success", "使用缓存二进制")
	} else {
		d.emitLog("info", "编译二进制...")
		out, err := d.execLocal(fmt.Sprintf(
			"docker run --rm -v %s:/build -w /build golang:1.21-alpine sh -c 'apk add --no-cache git >/dev/null 2>&1; go mod download && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags=\"-s -w\" -o /build/agent . 2>&1'",
			d.cfg.NodeAgentPath))
		if err != nil {
			return ErrBuild, "编译失败: " + err.Error() + "\n" + out
		}
		d.execLocal(fmt.Sprintf("echo '%s' > %s/agent.hash", hash, d.cfg.NodeAgentPath))
		d.emitLog("success", "编译完成")
	}

	if err := d.transferBinary(); err != nil {
		return ErrTransfer, "传输失败: " + err.Error()
	}
	d.emitLog("success", "二进制传输完成")
	return "", ""
}

func (d *Deployer) transferBinary() error {
	data, err := osReadFile(d.cfg.NodeAgentPath + "/agent")
	if err != nil {
		return fmt.Errorf("读取本地文件失败: %w", err)
	}

	d.exec(fmt.Sprintf("rm -f %s/agent", d.cfg.DeployDir), 5*time.Second)

	chunkSize := 1024 * 1024
	totalChunks := (len(data) + chunkSize - 1) / chunkSize

	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		encoded := base64Encode(data[start:end])
		cmd := fmt.Sprintf("echo '%s' | base64 -d >> %s/agent", encoded, d.cfg.DeployDir)
		if _, err := d.exec(cmd, 30*time.Second); err != nil {
			return fmt.Errorf("传输块 %d/%d 失败: %w", i+1, totalChunks, err)
		}
	}
	d.exec(fmt.Sprintf("chmod +x %s/agent", d.cfg.DeployDir), 5*time.Second)

	verifyOut, _ := d.exec(fmt.Sprintf("ls -la %s/agent && file %s/agent", d.cfg.DeployDir, d.cfg.DeployDir), 5*time.Second)
	if !strings.Contains(verifyOut, "ELF") && !strings.Contains(verifyOut, "executable") {
		return fmt.Errorf("传输后文件类型异常")
	}
	return nil
}

// ====== Phase 6: gRPC 预检 ======

func (d *Deployer) phaseGRPCPreCheck() (string, string) {
	addr := fmt.Sprintf("%s:%d", d.cfg.PanelGRPCAddr, d.cfg.GRPCPort)
	d.emitLog("info", "预检 gRPC 连通性 (%s)...", addr)

	script := fmt.Sprintf(`
HOST=$(echo "%s" | cut -d: -f1)
PORT=$(echo "%s" | cut -d: -f2)
[ -z "$PORT" ] && PORT=%d

if command -v nc &>/dev/null; then
    nc -zv -w 10 "$HOST" "$PORT" 2>&1 && echo "REACHABLE" || echo "UNREACHABLE"
elif timeout 10 bash -c 'echo >/dev/tcp/$HOST/$PORT' 2>/dev/null; then
    echo "REACHABLE"
else
    curl -sk --connect-timeout 10 "https://$HOST:$PORT" -o /dev/null 2>/dev/null
    rc=$?; if [ $rc -eq 35 ] || [ $rc -eq 56 ] || [ $rc -eq 92 ]; then echo "REACHABLE"; else echo "UNREACHABLE"; fi
fi
`, d.cfg.PanelGRPCAddr, d.cfg.PanelGRPCAddr, d.cfg.GRPCPort)

	out, err := d.exec(script, 30*time.Second)
	if err != nil || strings.Contains(out, "UNREACHABLE") {
		return ErrSSHConnect, fmt.Sprintf("gRPC 端口 %s 不可达", addr)
	}

	if d.cfg.GRPCTLS {
		tlsOut, _ := d.exec(fmt.Sprintf(
			"echo | timeout 8 openssl s_client -connect %s -servername %s -brief 2>&1 | head -10 || true",
			addr, d.cfg.PanelGRPCAddr), 15*time.Second)
		if strings.Contains(tlsOut, "certificate required") {
			return ErrStart, "面板启用了 mTLS 但 agent 无客户端证书"
		}
	}

	d.emitLog("success", "gRPC 预检通过 ✓")
	return "", ""
}

// ====== Phase 7: 启动服务 ======

func (d *Deployer) phaseStartService() (string, string) {
	d.emitLog("info", "启动 %s...", d.cfg.ContainerName)

	if !d.waitDockerReady(10) {
		return ErrDockerNotInstalled, "Docker 不可用"
	}

	script := fmt.Sprintf(`
set -e
cd %s
docker compose -f docker-compose.node.yml --env-file .env.node down --timeout 10 2>/dev/null || true
docker compose -f docker-compose.node.yml --env-file .env.node up -d --build 2>&1
`, d.cfg.DeployDir)

	out, err := d.exec(script, 120*time.Second)
	if err != nil {
		diagOut, _ := d.exec(fmt.Sprintf("cd %s && docker compose config 2>&1 | tail -10; echo '---'; docker ps -a | head -10", d.cfg.DeployDir), 10*time.Second)
		return ErrStart, fmt.Sprintf("启动失败: %v\n%s", err, out+diagOut)
	}

	d.emitLog("success", "容器已启动")

	time.Sleep(5 * time.Second)
	diag := d.diagnoseContainer()
	if diag != "" {
		d.emitLog("warn", diag)
	}

	return "", ""
}

func (d *Deployer) diagnoseContainer() string {
	out, _ := d.exec(fmt.Sprintf(`
STATUS=$(docker ps -a --filter name=%s --format '{{.Status}}' 2>/dev/null)
echo "STATUS=$STATUS"
docker logs --tail 30 %s 2>&1 || echo "LOG_UNAVAILABLE"
`, d.cfg.ContainerName, d.cfg.ContainerName), 10*time.Second)

	if strings.Contains(out, "STATUS=Exited") || strings.Contains(out, "STATUS=Restarting") {
		return fmt.Sprintf("容器异常: %s", out)
	}
	return ""
}

// ====== Phase 8: 验证 ======

func (d *Deployer) phaseVerify() (string, string) {
	d.emitLog("info", "验证节点注册 (最多 120s)...")

	for i := 0; i < 40; i++ {
		select {
		case <-d.ctx.Done():
			return ErrVerify, "验证超时"
		default:
		}

		if i < 10 {
			time.Sleep(2 * time.Second)
		} else {
			time.Sleep(3 * time.Second)
		}

		out, _ := d.exec(fmt.Sprintf("docker logs --tail 30 %s 2>&1 || echo 'UNAVAILABLE'", d.cfg.ContainerName), 5*time.Second)

		if strings.Contains(out, "注册成功") || strings.Contains(out, "已注册到面板") ||
			strings.Contains(out, "Xray 已启动") || strings.Contains(out, "Xray 进程已启动") {
			d.emitLog("success", "节点已注册 ✓")
			portOut, _ := d.exec(fmt.Sprintf(
				"(ss -tlnp 2>/dev/null || netstat -tlnp 2>/dev/null) | grep -E ':%d' || echo 'PORT_NOT_LISTENING'",
				d.cfg.ListenPort), 5*time.Second)
			d.emitLog("info", "端口监听:\n%s", portOut)
			return "", ""
		}

		if strings.Contains(out, "注册失败") || strings.Contains(out, "token 无效") || strings.Contains(out, "Unauthenticated") {
			return ErrVerify, "节点 Token 认证失败"
		}

		if i%5 == 0 {
			elapsed := i*2 + 2
			if i >= 10 {
				elapsed = 20 + (i-9)*3
			}
			d.emitLog("info", "等待中... (%ds/120s)", elapsed)
		}
	}

	d.emitLog("warn", "容器已启动但未注册到面板, 可能需手动检查")
	return "", ""
}

// ====== SSH 执行辅助 ======

func (d *Deployer) exec(cmd string, timeout time.Duration) (string, error) {
	if d.client == nil {
		return "", fmt.Errorf("SSH 未连接")
	}
	session, err := d.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(d.ctx, timeout)
	defer cancel()

	type result struct {
		output string
		err    error
	}
	ch := make(chan result, 1)

	go func() {
		out, err := session.CombinedOutput("export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin; " + cmd)
		ch <- result{string(out), err}
	}()

	select {
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		return "", fmt.Errorf("命令超时 (%v)", timeout)
	case r := <-ch:
		return r.output, r.err
	}
}

func (d *Deployer) execLocal(cmd string) (string, error) {
	return execLocal(cmd)
}

// ====== 文件操作辅助 ======

func (d *Deployer) writeRemoteFile(path, content string) error {
	cmd := fmt.Sprintf("cat > '%s' << 'DEPLOY_EOF'\n%s\nDEPLOY_EOF", path, content)
	_, err := d.exec(cmd, 10*time.Second)
	return err
}

// ====== 工具函数 ======

func classifySSHError(errStr string) string {
	switch {
	case strings.Contains(errStr, "connection refused"):
		return ErrSSHConnect
	case strings.Contains(errStr, "timeout"), strings.Contains(errStr, "i/o timeout"):
		return ErrSSHTimeout
	case strings.Contains(errStr, "authenticate"), strings.Contains(errStr, "unable to authenticate"),
		strings.Contains(errStr, "handshake failed"):
		return ErrSSHAuth
	case strings.Contains(errStr, "no such host"):
		return ErrSSHConnect
	default:
		return ErrSSHConnect
	}
}

func parseDiskAvailGB(s string) int {
	fields := strings.Fields(s)
	var gvs []int
	for _, f := range fields {
		f = strings.TrimSuffix(f, "G")
		f = strings.TrimSuffix(f, "Gi")
		var v int
		if _, err := fmt.Sscanf(f, "%d", &v); err == nil && strings.Contains(f, "G") {
			gvs = append(gvs, v)
		}
	}
	if len(gvs) >= 3 {
		return gvs[len(gvs)-3]
	}
	if len(gvs) == 1 {
		return gvs[0]
	}
	return -1
}

func parseMemAvailMB(s string) int {
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 7 && (fields[0] == "Mem:" || strings.HasPrefix(line, " ")) {
			var v int
			fmt.Sscanf(fields[len(fields)-1], "%d", &v)
			return v
		}
	}
	return -1
}

func osReadFile(path string) ([]byte, error) {
	return readFile(path)
}

func base64Encode(data []byte) string {
	return encodeBase64(data)
}
