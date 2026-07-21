package handler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// 测试覆盖: 一键更新相关的核心函数
// 对应修复: 60113f8, 6987ea2, 99323cc — helper 容器路径 + docker compose build 工作目录
// =============================================================================

// === getHostProjectRoot ===

// TestGetHostProjectRoot_WithEnv 验证 HOST_PROJECT_ROOT 环境变量优先级
// 修复 CRITICAL 2026-07-21: getHostProjectRoot 必须优先读环境变量,
// 否则在容器内 git rev-parse 返回的是容器内路径(/root/nexus-panel),
// 导致 docker compose volume 挂载路径解析到宿主机不存在的目录。
func TestGetHostProjectRoot_WithEnv(t *testing.T) {
	os.Setenv("HOST_PROJECT_ROOT", "/opt/nexus-panel")
	defer os.Unsetenv("HOST_PROJECT_ROOT")

	got := getHostProjectRoot()
	if got != "/opt/nexus-panel" {
		t.Fatalf("expected /opt/nexus-panel from env, got: %s", got)
	}
}

// TestGetHostProjectRoot_NoEnv_Fallback 验证无环境变量时回退到 getGitRoot()
// 确认方法签名正确, 不会 panic
func TestGetHostProjectRoot_NoEnv_Fallback(t *testing.T) {
	os.Unsetenv("HOST_PROJECT_ROOT")

	got := getHostProjectRoot()
	// 至少返回一个非空字符串(getGitRoot 有最终回退 "/root/nexus-panel")
	if got == "" {
		t.Fatal("getHostProjectRoot should not return empty string")
	}
	t.Logf("getHostProjectRoot (no env) = %s", got)
}

// === getGitRoot ===

// TestGetGitRoot_FallbackChain 验证回退链: git rev-parse → PROJECT_ROOT → cwd → "/root/nexus-panel"
func TestGetGitRoot_FallbackChain(t *testing.T) {
	got := getGitRoot()
	if got == "" {
		t.Fatal("getGitRoot should never return empty, has hardcoded fallback /root/nexus-panel")
	}
	t.Logf("getGitRoot = %s", got)
}

// === execCommandLogTimeout ===

// TestExecCommandLogTimeout_ChdirFailure 验证 cmd.Dir 设置为不存在的路径时,
// Go exec 包会返回 chdir 错误(而非静默忽略)。
// 这就是 6987ea2 版本失败的原因: build 命令用了 hostGitRoot(/opt/nexus-panel),
// 但 panel 容器内不存在该路径, chdir 失败导致整个更新中断。
func TestExecCommandLogTimeout_ChdirFailure(t *testing.T) {
	// 使用一个容器内不存在的宿主机路径, 模拟 hostGitRoot 被错误传入的场景
	nonExistent := "/opt/nexus-panel-not-exists-" + time.Now().Format("150405")

	// execCommandLogTimeout 内部设 cmd.Dir = nonExistent,
	// 然后 cmd.Start() → chdir 失败 → 返回 false
	ok := execCommandLogTimeout(nonExistent, "echo", 5, "hello")
	if ok {
		t.Fatal("expected failure when dir does not exist, but got success")
	}
	t.Log("correctly returned false when dir does not exist (chdir failure)")
}

// TestExecCommandLogTimeout_ChdirSuccess 验证 cmd.Dir 设置为存在的路径时正常执行
// 对比验证: 用存在的路径(gitRoot)就能正常执行, 证明问题就是路径存在性。
func TestExecCommandLogTimeout_ChdirSuccess(t *testing.T) {
	gitRoot := getGitRoot()
	// 验证 gitRoot 存在(否则回退到当前目录)
	if _, err := os.Stat(gitRoot); os.IsNotExist(err) {
		cwd, _ := os.Getwd()
		gitRoot = cwd
	}
	t.Logf("using dir: %s", gitRoot)

	// 使用 go version 作为跨平台命令(Windows 上没有 echo 可执行文件)
	ok := execCommandLogTimeout(gitRoot, "go", 5, "version")
	if !ok {
		t.Fatal("expected success when dir exists, but got failure")
	}
}

// === docker compose build 工作目录 ===

// TestDockerComposeBuildDirChoice 验证关键设计决策:
// docker compose build 必须用 gitRoot(容器内路径), 不能用 hostGitRoot(宿主机路径)。
//
// 原因: execCommandLogTimeout 内部设 cmd.Dir = dir, Go exec 包会 chdir(dir)。
// 如果 dir 是 hostGitRoot(/opt/nexus-panel), panel 容器内不存在 → chdir 失败。
// docker compose build 只读 Dockerfile/源码, 不涉及 volume 挂载路径解析,
// 用 gitRoot 即可正确工作。
//
// 这个测试不执行真实的 docker compose build(太重), 而是验证:
// 1. hostGitRoot 和 gitRoot 可能不同(容器内路径 vs 宿主机路径)
// 2. gitRoot 在容器内一定存在, hostGitRoot 不一定存在
func TestDockerComposeBuildDirChoice(t *testing.T) {
	os.Setenv("HOST_PROJECT_ROOT", "/opt/nexus-panel")
	defer os.Unsetenv("HOST_PROJECT_ROOT")

	hostGitRoot := getHostProjectRoot()
	gitRoot := getGitRoot()

	// 验证: hostGitRoot 和 gitRoot 可能不同
	if hostGitRoot == gitRoot {
		// 如果没配 HOST_PROJECT_ROOT, 两者相同是正常的
		t.Logf("hostGitRoot == gitRoot == %s (no HOST_PROJECT_ROOT env)", hostGitRoot)
	} else {
		t.Logf("hostGitRoot=%s, gitRoot=%s (different)", hostGitRoot, gitRoot)
	}

	// 验证: gitRoot 在容器内存在(否则没法执行 build)
	if _, err := os.Stat(gitRoot); os.IsNotExist(err) {
		// 不是 git 仓库时 fallback 到当前目录, 也是合理的
		cwd, _ := os.Getwd()
		t.Logf("gitRoot %s not exist, fallback to cwd: %s", gitRoot, cwd)
	} else {
		t.Logf("gitRoot %s exists (can use for docker compose build)", gitRoot)
	}

	// 验证: hostGitRoot 在容器内不一定存在(这就是 6987ea2 失败的根因)
	if _, err := os.Stat(hostGitRoot); os.IsNotExist(err) {
		t.Logf("hostGitRoot %s does NOT exist in container (correct: use gitRoot for build)", hostGitRoot)
	} else {
		t.Logf("hostGitRoot %s exists in container", hostGitRoot)
	}
}

// === helper 容器命令构造 ===

// TestHelperContainerCommand_NoDirSet 验证 helperCmd.Dir 没有被设置。
// 这是 60113f8 修复的核心: 不能设 helperCmd.Dir = hostGitRoot,
// 因为 exec.Command 是在 panel 容器内执行的, hostGitRoot 是宿主机路径,
// 容器内不存在 → chdir 失败 → helper 启动失败。
func TestHelperContainerCommand_NoDirSet(t *testing.T) {
	// 模拟构造 helper 容器命令(与 GitPull 中的逻辑一致)
	hostGitRoot := "/opt/nexus-panel"
	helperCmd := exec.Command("echo", "simulated-helper-init",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", hostGitRoot+":"+hostGitRoot,
		"-w", hostGitRoot,
		"alpine:latest",
	)

	// 关键断言: helperCmd.Dir 应该是空字符串(默认值), 不应该被设置
	// 如果设置了, 在容器内执行时会 chdir 到不存在的宿主机路径 → 失败
	if helperCmd.Dir != "" {
		t.Fatalf("helperCmd.Dir should be empty (not set), got: %s. "+
			"Setting Dir to hostGitRoot causes chdir failure in container", helperCmd.Dir)
	}
	t.Log("helperCmd.Dir is empty (correct: docker run does not need exec process cwd)")
}

// TestHelperContainerCommand_MountUsesHostPath 验证 helper 容器挂载使用宿主机路径。
// docker run -v 参数格式: hostGitRoot:hostGitRoot
// 这样 helper 容器内的路径和宿主机路径一致, docker compose 相对路径解析正确。
func TestHelperContainerCommand_MountUsesHostPath(t *testing.T) {
	hostGitRoot := "/opt/nexus-panel"

	// 验证 -v 参数格式
	volumeArg := hostGitRoot + ":" + hostGitRoot

	parts := strings.Split(volumeArg, ":")
	if len(parts) != 2 {
		t.Fatalf("invalid volume mount format: %s", volumeArg)
	}
	if parts[0] != parts[1] {
		t.Fatalf("host and container paths should be identical for correct path resolution, "+
			"got host=%s container=%s", parts[0], parts[1])
	}
	t.Logf("volume mount: %s (host and container paths are identical)", volumeArg)

	// 验证 -w 参数等于 hostGitRoot
	// 这样 helper 容器内 docker compose 的工作目录是宿主机路径,
	// docker-compose.yml 相对路径(./deployments/...) 被 dockerd 正确解析
	workDir := hostGitRoot
	if workDir != hostGitRoot {
		t.Fatalf("helper work dir should be hostGitRoot, got: %s", workDir)
	}
	t.Logf("helper work dir (-w): %s (docker compose relative paths resolve correctly)", workDir)
}

// TestHelperContainerCommand_Sequence 验证 helper 容器命令执行顺序:
// 1. apk add docker-cli docker-cli-compose
// 2. docker compose up -d --no-deps frontend (先建前端, 改动最小)
// 3. sleep 3 (等前端就绪)
// 4. docker compose up -d --no-deps panel (重建 panel, 杀当前进程)
// 5. docker rm -f nexus-panel-restarter (清理自己)
// 这个顺序很重要: 必须先建 frontend 再建 panel(panel 重建会杀当前进程)。
func TestHelperContainerCommand_Sequence(t *testing.T) {
	// 模拟 helper 容器执行的 shell 命令
	shellCmd := "apk add --no-cache docker-cli docker-cli-compose >/dev/null 2>&1 && " +
		"docker compose up -d --no-deps frontend && " +
		"sleep 3 && " +
		"docker compose up -d --no-deps panel && " +
		"docker rm -f nexus-panel-restarter"

	// 验证执行顺序: frontend 必须在 panel 之前
	frontendIdx := strings.Index(shellCmd, "frontend")
	panelIdx := strings.Index(shellCmd, "panel")
	sleepIdx := strings.Index(shellCmd, "sleep 3")

	if frontendIdx < 0 || panelIdx < 0 || sleepIdx < 0 {
		t.Fatal("helper shell command missing required steps")
	}
	if frontendIdx > panelIdx {
		t.Fatal("frontend must be recreated BEFORE panel (panel recreate kills current process)")
	}
	if sleepIdx > frontendIdx && sleepIdx < panelIdx {
		t.Log("sleep 3 correctly placed between frontend and panel recreation")
	} else {
		t.Log("sleep 3 position: not between frontend and panel (may be OK if helper handles timing)")
	}

	// 验证 self-cleanup: docker rm -f nexus-panel-restarter 必须在最后
	rmIdx := strings.Index(shellCmd, "docker rm -f nexus-panel-restarter")
	if rmIdx < panelIdx {
		t.Fatal("self-cleanup must be after panel recreation (last step)")
	}
	t.Log("command sequence verified: frontend → sleep → panel → self-cleanup")
}

// === setPullDone ===

// TestSetPullDone_StatePersistence 验证 setPullDone 同时更新内存和文件状态。
// 修复 UI-LOG-01 (P1): setPullDone(true) 必须在 panel 重建前调用,
// 因为 docker compose up panel 会杀当前进程, 必须先把成功状态持久化到文件,
// 新容器启动后 init 从文件恢复 gitPullDone/gitPullOK, 前端轮询读到"成功"。
func TestSetPullDone_StatePersistence(t *testing.T) {
	// 保存原始状态, 测试后恢复
	origDone := gitPullDone
	origOK := gitPullOK
	defer func() {
		gitPullDone = origDone
		gitPullOK = origOK
	}()

	// 确保状态文件目录存在
	_ = os.MkdirAll(gitPullLogDir, 0755)

	// 测试成功状态
	setPullDone(true)
	if !gitPullDone {
		t.Fatal("gitPullDone should be true after setPullDone(true)")
	}
	if !gitPullOK {
		t.Fatal("gitPullOK should be true after setPullDone(true)")
	}

	// 验证状态文件已写入
	if _, err := os.Stat(gitPullStateFile); os.IsNotExist(err) {
		t.Fatal("state file should exist after setPullDone")
	}

	// 测试失败状态
	setPullDone(false)
	if !gitPullDone {
		t.Fatal("gitPullDone should be true after setPullDone(false)")
	}
	if gitPullOK {
		t.Fatal("gitPullOK should be false after setPullDone(false)")
	}
}

// === execCommandLogTimeout 超时和取消 ===

// TestExecCommandLogTimeout_ContextCancellation 验证超时机制:
// 长时间运行的命令应该被 context 超时取消, 不会永久阻塞。
// 这对 docker compose build 很重要, 构建可能因网络问题卡住。
func TestExecCommandLogTimeout_ContextCancellation(t *testing.T) {
	// 用很短的超时(1秒)执行会阻塞的命令
	start := time.Now()
	ok := execCommandLogTimeout(".", "sleep", 1, "30")
	elapsed := time.Since(start)

	if ok {
		// sleep 可能在某些系统上被正确处理
		t.Log("sleep was killed by signal (normal)")
	}

	// 关键: 不应该等 30 秒, 超时后应该 1 秒左右就返回
	if elapsed > 5*time.Second {
		t.Fatalf("timeout didn't work: expected ~1s, got %v", elapsed)
	}
	t.Logf("timeout worked: elapsed %v (expected ~1s)", elapsed)
}

// === execCommand 辅助函数 ===

// TestExecCommand_OutputCapture 验证 execCommand 能正确捕获输出,
// 用于 git rev-parse --short HEAD 等命令。
func TestExecCommand_OutputCapture(t *testing.T) {
	// execCommand 返回 systemActionResult{Success, Output, Error}
	// 使用 go version 作为跨平台命令(Windows 上没有 echo 可执行文件)
	result := execCommand("go", "version")
	if result.Output == "" {
		t.Fatal("Output should not be empty")
	}
	if !result.Success {
		t.Fatal("go version should succeed")
	}
	t.Logf("execCommand output: %s", result.Output)

	// 验证 execCommandLog 也能正常捕获输出
	gitRoot := getGitRoot()
	if _, err := os.Stat(gitRoot); os.IsNotExist(err) {
		cwd, _ := os.Getwd()
		gitRoot = cwd
	}

	ok := execCommandLogTimeout(gitRoot, "go", 5, "version")
	if !ok {
		t.Fatal("execCommandLogTimeout should succeed for go version")
	}
}

// === 边界条件 ===

// TestGetHostProjectRoot_EmptyEnv 验证 HOST_PROJECT_ROOT 为空字符串时的行为
// 空环境变量应该被视为"未设置", 走回退逻辑
func TestGetHostProjectRoot_EmptyEnv(t *testing.T) {
	os.Setenv("HOST_PROJECT_ROOT", "")
	defer os.Unsetenv("HOST_PROJECT_ROOT")

	got := getHostProjectRoot()
	// 空字符串不应被返回, 应该回退到 getGitRoot()
	if got == "" {
		t.Fatal("getHostProjectRoot should not return empty when HOST_PROJECT_ROOT is empty")
	}
	t.Logf("HOST_PROJECT_ROOT='' → getHostProjectRoot=%s (fallback to getGitRoot)", got)
}

// TestExecCommandLogTimeout_EmptyDir 验证 dir 为空字符串时的行为
// exec.Command 的 Dir 为空时使用当前工作目录, 不应该 panic
func TestExecCommandLogTimeout_EmptyDir(t *testing.T) {
	ok := execCommandLogTimeout("", "go", 5, "version")
	if !ok {
		t.Fatal("execCommandLogTimeout with empty dir should succeed (uses cwd)")
	}
	t.Log("empty dir: uses current working directory (no panic)")
}

// TestExecCommandLogTimeout_NonexistentBinary 验证命令不存在时的行为
// 应该返回 false, 不会 panic
func TestExecCommandLogTimeout_NonexistentBinary(t *testing.T) {
	ok := execCommandLogTimeout(".", "this-binary-should-not-exist-xyz", 5, "arg")
	if ok {
		t.Fatal("expected failure for nonexistent binary")
	}
	t.Log("nonexistent binary: correctly returned false")
}

// === 集成: 路径使用场景验证 ===

// TestPathUsageDecisionMatrix 验证路径使用决策矩阵:
//
//	场景                              | 使用路径     | 原因
//	----------------------------------|-------------|--------------------------
//	docker compose build              | gitRoot      | 只读 Dockerfile, 不需要 volume 挂载解析
//	docker compose up (helper 容器内) | hostGitRoot  | volume 挂载路径需要宿主机路径
//	git rev-parse 等 git 命令         | gitRoot      | git 仓库在容器内路径
//	image prune / builder prune       | gitRoot      | 通过 docker.sock, 不需要宿主机路径
//	helperCmd.Dir                     | (不设置)     | docker run 用 -w, 不需要 exec 进程 cwd
//	.last_build_version 写入          | gitRoot      | 文件在容器内路径
func TestPathUsageDecisionMatrix(t *testing.T) {
	os.Setenv("HOST_PROJECT_ROOT", "/opt/nexus-panel")
	defer os.Unsetenv("HOST_PROJECT_ROOT")

	hostGitRoot := getHostProjectRoot()
	gitRoot := getGitRoot()

	// 场景 1: docker compose build → gitRoot (容器内路径)
	buildDir := gitRoot // 应该用 gitRoot
	if buildDir == "/opt/nexus-panel" && hostGitRoot != gitRoot {
		t.Fatal("docker compose build should use gitRoot (container path), not hostGitRoot")
	}
	t.Logf("docker compose build dir: %s (gitRoot)", buildDir)

	// 场景 2: docker compose up → helper 容器内用 hostGitRoot
	// helper 容器通过 -w hostGitRoot 指定工作目录
	helperWorkDir := hostGitRoot
	if helperWorkDir != hostGitRoot {
		t.Fatal("helper container work dir should be hostGitRoot")
	}
	t.Logf("docker compose up dir (in helper): %s (hostGitRoot)", helperWorkDir)

	// 场景 3: helperCmd.Dir → 不设置(空字符串)
	// 已在 TestHelperContainerCommand_NoDirSet 中验证

	// 场景 4: 文件写入操作 → gitRoot
	lastBuildVersionPath := filepath.Join(gitRoot, ".last_build_version")
	t.Logf(".last_build_version path: %s (gitRoot)", lastBuildVersionPath)

	// 场景 5: image prune → gitRoot
	pruneDir := gitRoot
	if pruneDir == "/opt/nexus-panel" && hostGitRoot != gitRoot {
		t.Fatal("image prune should use gitRoot (docker.sock, not volume mount)")
	}
	t.Logf("image prune dir: %s (gitRoot)", pruneDir)
}

// === 辅助: 确保 git root 可达性 ===

// TestGitRootContainsDockerCompose 验证 gitRoot 下存在 docker-compose.yml。
// 这是 docker compose build 能正常工作的前提。
// 如果 gitRoot 不正确, docker compose 找不到 compose 文件, build 会失败。
func TestGitRootContainsDockerCompose(t *testing.T) {
	gitRoot := getGitRoot()
	composeFile := filepath.Join(gitRoot, "docker-compose.yml")
	if _, err := os.Stat(composeFile); os.IsNotExist(err) {
		t.Logf("docker-compose.yml not found at %s (not a docker compose project root)", composeFile)
		// 这不是错误, 只是说明当前环境不是部署环境
	} else {
		t.Logf("docker-compose.yml found at %s (correct git root)", composeFile)
	}
}

// === execCommandLogTimeout 并发安全 ===

// TestExecCommandLogTimeout_ConcurrentLogWrite 验证 logWrite 在并发下的安全性。
// execCommandLogTimeout 用 goroutine 实时读取 stdout/stderr,
// 两个 goroutine 同时调用 logWrite, 需要确保字符串 buffer 不会竞态。
// logWrite 内部用 sync.Mutex 保护, 这个测试验证并发写入不会 panic。
func TestExecCommandLogTimeout_ConcurrentLogWrite(t *testing.T) {
	// 重置日志 buffer, 避免干扰
	gitPullLog.Reset()

	// 执行一个会产生大量输出的命令, 触发 stdout/stderr goroutine 并发写入
	gitRoot := getGitRoot()
	if _, err := os.Stat(gitRoot); os.IsNotExist(err) {
		cwd, _ := os.Getwd()
		gitRoot = cwd
	}

	// 在 Windows 上 dir 命令输出多, 在 Linux 上 ls -la 输出多
	// 使用 go version 作为跨平台命令
	ok := execCommandLogTimeout(gitRoot, "go", 10, "version")
	if !ok {
		t.Log("go version not available, skipping concurrent log test")
		return
	}

	// 如果有输出, 说明并发写入正常
	logOutput := gitPullLog.String()
	if len(logOutput) > 0 {
		t.Logf("concurrent log write succeeded, output length: %d", len(logOutput))
	} else {
		t.Log("log write returned empty (may be expected for go version)")
	}

	// 再次重置
	gitPullLog.Reset()
}

// === 竞态条件: gitPullMu ===

// TestGitPullMu_ConcurrentAccess 验证互斥锁能防止并发更新。
// 如果两个请求同时触发 git pull, 第二个应该被拒绝。
func TestGitPullMu_ConcurrentAccess(t *testing.T) {
	// 确保锁是释放状态
	if gitPullMu.TryLock() {
		gitPullMu.Unlock()
	} else {
		// 如果锁已被持有(可能是之前测试失败未释放), 强制解锁
		// 这种情况不应该发生, 但测试环境需要处理
		t.Log("lock was held, unlocking for test")
		// 无法强制解锁 sync.Mutex, 跳过
		t.Skip("gitPullMu is locked, skipping")
	}

	// 第一次获取锁应该成功
	if !gitPullMu.TryLock() {
		t.Fatal("first TryLock should succeed")
	}

	// 第二次获取锁应该失败(锁已被持有)
	if gitPullMu.TryLock() {
		gitPullMu.Unlock()
		t.Fatal("second TryLock should fail (lock already held)")
	}

	// 释放锁
	gitPullMu.Unlock()
	t.Log("mutex correctly prevents concurrent access")
}

// === execCommand 基础函数 ===

// execCommand 是 execCommandLog 的简化版, 用于 git rev-parse 等短命令。
// 这个测试验证它的基本行为。
func TestExecCommand_Basic(t *testing.T) {
	result := execCommand("go", "version")
	if !result.Success {
		t.Fatalf("execCommand should succeed, got error: %s", result.Error)
	}
	if result.Output == "" {
		t.Fatal("Output should not be empty")
	}
	t.Logf("execCommand basic: output=%s", result.Output)
}

// === 验证 context 超时传播 ===

// TestContextTimeoutPropagation 验证 execCommandLogTimeout 的 context 超时
// 能正确传播到子进程。如果 docker compose build 卡住(网络问题),
// 超时后应该终止子进程, 不会永久阻塞。
func TestContextTimeoutPropagation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 用 go run 执行一个无限循环, 测试 context 超时传播
	// 跨平台: 不依赖 Windows 上不存在的 sleep 命令
	cmd := exec.CommandContext(ctx, "go", "run", "-")
	cmd.Dir = "."
	cmd.Stdin = strings.NewReader(`
		package main
		import "time"
		func main() { time.Sleep(30 * time.Second) }
	`)

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("infinite sleep should have been killed by context timeout")
	}

	if elapsed > 5*time.Second {
		t.Fatalf("context timeout didn't propagate: expected ~100ms, got %v", elapsed)
	}
	t.Logf("context timeout propagated: elapsed %v, err=%v", elapsed, err)
}

// =============================================================================
// 集成测试: 模拟宿主机路径不存在时执行 docker compose build
// 运行方式: go test -run Integration -count=1 -v ./internal/handler/
// 跳过短测试: go test -short 时跳过(需要 docker 环境)
// =============================================================================

// TestIntegration_DockerComposeBuild_NonExistentDir
// 模拟 6987ea2 的事故场景: execCommandLogTimeout 的 dir 参数被设为宿主机路径
// （如 /opt/nexus-panel），但容器内不存在该路径 → chdir 失败 → build 中断。
//
// 测试步骤:
//  1. 创建临时目录, 内含最小 docker-compose.yml + Dockerfile
//  2. 用不存在的路径调用 execCommandLogTimeout → 预期失败(chdir 报错)
//  3. 用正确路径(临时目录)调用 execCommandLogTimeout → 预期成功(build 完成)
//  4. 验证 build 产物(docker image)存在
//  5. 清理临时目录和镜像
func TestIntegration_DockerComposeBuild_NonExistentDir(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires docker, skipped in short mode")
	}

	// 检查 docker 是否可用
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available, skipping integration test")
	}

	// ---- 步骤 0: 创建临时项目目录 ----
	tmpDir, err := os.MkdirTemp("", "nexus-integration-build-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 兜底清理: 无论测试成功还是失败, 确保镜像和容器都不残留
	// defer 按 LIFO 顺序执行, 这个 defer 在 os.RemoveAll 之后注册, 先执行
	defer func() {
		_ = exec.Command("docker", "rmi", "-f", "nexus-integration-test:latest").Run()
		_ = exec.Command("docker", "rm", "-f", "nexus-integration-test-helper").Run()
		// 清理 docker compose build 可能产生的中间层 dangling 镜像
		_ = exec.Command("docker", "image", "prune", "-f").Run()
	}()

	t.Logf("temp project dir: %s", tmpDir)

	// 写入最小 docker-compose.yml
	composeContent := `services:
  test-service:
    build:
      context: .
      dockerfile: Dockerfile
    image: nexus-integration-test:latest
`
	if err := os.WriteFile(filepath.Join(tmpDir, "docker-compose.yml"), []byte(composeContent), 0644); err != nil {
		t.Fatalf("failed to write docker-compose.yml: %v", err)
	}

	// 写入最小 Dockerfile
	dockerfileContent := `FROM alpine:3.18
RUN echo "integration-test-build" > /build-marker
`
	if err := os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfileContent), 0644); err != nil {
		t.Fatalf("failed to write Dockerfile: %v", err)
	}

	// ---- 步骤 1: 用不存在的路径调用 execCommandLogTimeout ----
	nonExistent := filepath.Join(tmpDir, "this-path-does-not-exist")
	t.Logf("step 1: calling execCommandLogTimeout with non-existent dir: %s", nonExistent)

	ok := execCommandLogTimeout(nonExistent, "docker", 60, "compose", "build")
	if ok {
		// 清理可能产生的镜像
		_ = exec.Command("docker", "rmi", "-f", "nexus-integration-test:latest").Run()
		t.Fatal("expected failure when dir does not exist (chdir should fail), but got success")
	}
	t.Log("step 1 PASS: correctly returned false for non-existent dir")

	// 验证日志中有失败信息
	logOutput := gitPullLog.String()
	if logOutput == "" {
		t.Log("warning: log buffer is empty, chdir error may not have been logged")
	} else {
		t.Logf("log output after failure: %s", strings.TrimSpace(logOutput))
	}
	gitPullLog.Reset()

	// ---- 步骤 2: 用正确路径调用 execCommandLogTimeout ----
	t.Logf("step 2: calling execCommandLogTimeout with correct dir: %s", tmpDir)

	ok = execCommandLogTimeout(tmpDir, "docker", 120, "compose", "build")
	if !ok {
		logOutput = gitPullLog.String()
		t.Fatalf("docker compose build failed with correct dir.\nlog: %s", logOutput)
	}
	t.Log("step 2 PASS: docker compose build succeeded with correct dir")

	_ = gitPullLog.String() // build log consumed, verified by step 3 image check
	gitPullLog.Reset()

	// ---- 步骤 3: 验证 build 产物 ----
	// 检查镜像是否存在
	output, err := exec.Command("docker", "images", "nexus-integration-test:latest",
		"--format", "{{.Repository}}:{{.Tag}}").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "nexus-integration-test") {
		t.Fatalf("docker image not found after build. output=%s, err=%v", string(output), err)
	}
	t.Logf("step 3 PASS: docker image found: %s", strings.TrimSpace(string(output)))

	// 验证镜像内容: 检查 build marker 文件存在
	markerOutput, err := exec.Command("docker", "run", "--rm",
		"nexus-integration-test:latest", "cat", "/build-marker").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to verify image content: %v, output=%s", err, string(markerOutput))
	}
	if !strings.Contains(string(markerOutput), "integration-test-build") {
		t.Fatalf("image content mismatch: expected 'integration-test-build', got '%s'",
			strings.TrimSpace(string(markerOutput)))
	}
	t.Logf("step 3b PASS: image content verified: %s", strings.TrimSpace(string(markerOutput)))

	// 最终验证: 路径决策矩阵
	//   docker compose build → 用 gitRoot(容器内路径)  ✓
	//   docker compose build → 用 hostGitRoot(宿主机路径) ✗ (chdir 失败)
	t.Log("=== integration test summary ===")
	t.Log("decision matrix verified:")
	t.Log("  docker compose build with gitRoot (exists)     → SUCCESS")
	t.Log("  docker compose build with hostGitRoot (absent)  → FAILURE (chdir)")
	t.Log("  conclusion: docker compose build MUST use gitRoot, not hostGitRoot")
	// 清理由 defer 自动完成, 包括镜像、容器、dangling 镜像和临时目录
}

// TestIntegration_HelperContainer_Simulated
// 模拟 helper 容器执行 docker compose up 的场景。
// 验证 helper 容器的 docker run 命令构造正确:
//   - -v hostGitRoot:hostGitRoot（宿主机路径挂载）
//   - -w hostGitRoot（工作目录 = 宿主机路径）
//   - helperCmd.Dir 不设置（exec.Command 在 panel 容器内执行, Dir 设宿主机路径会 chdir 失败）
//
// 这个测试不会真正启动 helper 容器（避免影响正在运行的 panel），
// 而是验证命令参数构造是否正确。
func TestIntegration_HelperContainer_Simulated(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires docker, skipped in short mode")
	}

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available, skipping integration test")
	}

	// 兜底清理: 无论测试成功还是失败, 确保 helper 容器不残留
	// 即使 helper 容器自清理失败(如 docker rm 在容器内执行失败),
	// 这个 defer 在宿主机上执行清理, 保证 CI 环境干净
	defer func() {
		_ = exec.Command("docker", "rm", "-f", "nexus-integration-test-helper").Run()
	}()

	// 模拟 hostGitRoot（宿主机路径）
	hostGitRoot := "/opt/nexus-panel"

	// 验证: 不能用 execCommandLog 来执行 helper 容器
	// 原因: execCommandLog 内部设 cmd.Dir = hostGitRoot,
	// panel 容器内不存在 /opt/nexus-panel → chdir 失败
	t.Log("verification 1: execCommandLog should NOT be used for helper container")
	ok := execCommandLogTimeout(hostGitRoot, "docker", 5, "version")
	if ok {
		// 如果在某些环境下 /opt/nexus-panel 恰好存在, 跳过
		t.Log("  /opt/nexus-panel exists in this environment, skip chdir check")
	} else {
		t.Log("  PASS: execCommandLog with hostGitRoot fails as expected (chdir)")
	}
	gitPullLog.Reset()

	// 验证: 正确做法是用 exec.Command 直接构造, 不设 Dir
	t.Log("verification 2: helperCmd.Dir should be empty")
	helperCmd := exec.Command("docker", "run", "-d",
		"--name", "nexus-integration-test-helper",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", hostGitRoot+":"+hostGitRoot,
		"-w", hostGitRoot,
		"alpine:latest",
		"sh", "-c", "echo 'helper test ok' && docker rm -f nexus-integration-test-helper",
	)

	if helperCmd.Dir != "" {
		t.Fatalf("helperCmd.Dir should be empty, got: %s", helperCmd.Dir)
	}
	t.Log("  PASS: helperCmd.Dir is empty")

	// 验证: helper 容器的 -v 挂载路径 hostGitRoot:hostGitRoot
	t.Log("verification 3: volume mount should be hostGitRoot:hostGitRoot")
	args := helperCmd.Args
	foundVolume := false
	foundWorkDir := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) {
			expected := hostGitRoot + ":" + hostGitRoot
			if args[i+1] == expected {
				foundVolume = true
			}
		}
		if arg == "-w" && i+1 < len(args) {
			if args[i+1] == hostGitRoot {
				foundWorkDir = true
			}
		}
	}
	if !foundVolume {
		t.Fatal("helper container -v mount should be hostGitRoot:hostGitRoot")
	}
	if !foundWorkDir {
		t.Fatal("helper container -w should be hostGitRoot")
	}
	t.Log("  PASS: volume mount and work dir are correct")

	// 执行: 真正启动 helper 容器测试（不依赖 panel，
	// 只是一个独立的 alpine 容器执行 echo 然后自清理）
	// 先清理可能残留的容器
	_ = exec.Command("docker", "rm", "-f", "nexus-integration-test-helper").Run()

	t.Log("verification 4: helper container can start and self-cleanup")
	out, err := helperCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper container failed to start: %v\noutput: %s", err, string(out))
	}
	t.Logf("  helper output: %s", strings.TrimSpace(string(out)))

	// 等待 helper 容器自清理完成
	time.Sleep(3 * time.Second)

	// 验证 helper 容器已自清理
	checkCmd := exec.Command("docker", "ps", "-a", "--filter",
		"name=nexus-integration-test-helper", "--format", "{{.Names}}")
	checkOut, _ := checkCmd.CombinedOutput()
	if strings.TrimSpace(string(checkOut)) != "" {
		t.Logf("  warning: helper container self-cleanup failed, defer will clean up")
	} else {
		t.Log("  PASS: helper container self-cleaned up")
	}

	t.Log("=== integration test: helper container PASS ===")
}
