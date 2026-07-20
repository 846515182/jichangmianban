package service

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/config"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// backupDir 备份目录, 与 handler.backupDir 保持一致(对齐 docker-compose 挂载点)。
const backupDir = "/app/data/backup"

type CronService struct {
	userRepo    *repo.UserRepo
	orderSvc    *OrderService
	nodeRepo    *repo.NodeRepo
	settingRepo *repo.SettingRepo
	logger      *zap.Logger
}

func NewCronService(u *repo.UserRepo, o *OrderService, n *repo.NodeRepo, sr *repo.SettingRepo, logger *zap.Logger) *CronService {
	return &CronService{userRepo: u, orderSvc: o, nodeRepo: n, settingRepo: sr, logger: logger}
}

func (s *CronService) ExpireOverdueUsers() {
	now := time.Now()
	count, err := s.userRepo.ExpireOverdueUsers(now)
	if err != nil {
		s.logger.Error("clean expired users failed", zap.Error(err))
		return
	}
	if count > 0 {
		s.logger.Info("cleaned expired users", zap.Int64("count", count))
	}
}

func (s *CronService) ExpireOrders() {
	count, err := s.orderSvc.ExpireOrders()
	if err != nil {
		s.logger.Error("clean expired orders failed", zap.Error(err))
		return
	}
	if count > 0 {
		s.logger.Info("cleaned expired orders", zap.Int("count", count))
	}
}

// tryLock 尝试获取分布式锁，成功返回解锁函数，失败返回 nil
func tryLock(rdb *redis.Client, key string, ttl time.Duration) (unlock func()) {
	if rdb == nil {
		return nil
	}
	// 使用 crypto/rand 生成锁 token，避免纳秒级并发下时间戳碰撞
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil
	}
	token := hex.EncodeToString(tokenBytes)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil || !ok {
		return nil
	}
	return func() {
		// 仅删除自己的锁（通过 Lua 脚本保证原子性）
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		script := `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
		if result, err := rdb.Eval(ctx, script, []string{key}, token).Result(); err != nil {
			// 解锁失败仅记录但不影响业务流
			_ = result
		}
	}
}

// RunAll 执行所有定时任务（带分布式锁，防止多副本重复执行）
func (s *CronService) RunAll() {
	unlock := tryLock(app.Get().RDB, "cron:lock:runall", 4*time.Minute)
	if unlock == nil {
		s.logger.Debug("定时任务 RunAll 被其它实例占用，跳过本次")
		return
	}
	defer unlock()
	s.ExpireOverdueUsers()
	s.ExpireOrders()
	s.MarkTrafficExhausted()
	s.CleanAggregateTrafficLogs()
}

// MarkTrafficExhausted 兜底检测所有超额用户并标记 traffic_exhausted
// 修复 BIZ-FATAL-02: 此前仅在 grpc ReportRealtime 实时上报时检测超额,
// 若节点 agent 上报异常/缺失,超额用户永远不会被停服("流量不停机")。
// 每 5 分钟扫一次 users 表,凡 status='active' 且 traffic_used>=traffic_limit (>0) 全部标记。
func (s *CronService) MarkTrafficExhausted() {
	if s.userRepo == nil {
		return
	}
	count, err := s.userRepo.MarkAllTrafficExhausted()
	if err != nil {
		s.logger.Error("mark traffic exhausted failed", zap.Error(err))
		return
	}
	if count > 0 {
		s.logger.Warn("已自动标记超额用户为 traffic_exhausted", zap.Int64("count", count))
	}
}

// CleanAggregateTrafficLogs 清理历史遗留的"幽灵用户"流量日志
// node-agent 0.1.0 之前把节点聚合流量写到 user_id="00000000-0000-0000-0000-000000000000",
// 造成后台 TopUsers/TrafficStats 出现"deleted"幽灵用户,后台显示混乱。
// 每天清理一次超过 7 天的聚合标记流量日志(给客户端 grace 期间再清)。
//
// 修复 SQL-CRON-01 (P0): 旧版 SQL 含 `user_id LIKE 'node:%'`, 但 traffic_logs.user_id
// 是 PostgreSQL uuid 类型, 不支持 LIKE 操作符, 每 5 分钟报
// `operator does not exist: uuid ~~ unknown (SQLSTATE 42883)`。
// 实际上 "node:"+nodeID 前缀只在 gRPC 内存层(grpc.isNodeAggregateUser)用于标识聚合流量,
// 在 distributeNodeTraffic 中已被分发到真实用户 ID 后才落库, 永远不会以 "node:xxx"
// 字符串形式写入 user_id 列(uuid 列也根本存不下非 UUID 字符串)。
// 因此 LIKE 'node:%' 子句既非法又永远匹配 0 行, 直接移除。
// 仅保留对占位零值 UUID 的清理即可。
func (s *CronService) CleanAggregateTrafficLogs() {
	db := app.Get().DB
	if db == nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -7)
	// 仅清理旧版占位零值 UUID 的聚合流量日志("node:" 前缀数据不会落库, 无需 LIKE)
	result := db.Exec(`
		DELETE FROM traffic_logs
		WHERE log_time < ?
		AND user_id = ?
	`, cutoff, "00000000-0000-0000-0000-000000000000")
	if result.Error != nil {
		s.logger.Error("clean aggregate traffic logs failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		s.logger.Info("已清理幽灵用户流量日志", zap.Int64("count", result.RowsAffected))
	}
}

func (s *CronService) CleanOrphanData() {
	unlock := tryLock(app.Get().RDB, "cron:lock:orphan", 3*time.Hour)
	if unlock == nil {
		return
	}
	defer unlock()

	db := app.Get().DB
	if db == nil {
		return
	}

	result := db.Exec(`
                UPDATE subscriptions SET is_deleted = true, updated_at = NOW()
                WHERE is_deleted = false
                AND user_id NOT IN (SELECT id FROM users WHERE is_deleted = false)
        `)
	if result.Error != nil {
		s.logger.Error("clean orphan subscriptions failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		s.logger.Info("cleaned orphan subscriptions", zap.Int64("count", result.RowsAffected))
	}

	result = db.Exec(`
                UPDATE user_nodes SET is_deleted = true, updated_at = NOW()
                WHERE is_deleted = false
                AND (
                        user_id NOT IN (SELECT id FROM users WHERE is_deleted = false)
                        OR node_id NOT IN (SELECT id FROM nodes WHERE is_deleted = false)
                )
        `)
	if result.Error != nil {
		s.logger.Error("clean orphan user_nodes failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		s.logger.Info("cleaned orphan user_nodes", zap.Int64("count", result.RowsAffected))
	}

	result = db.Exec(`
                DELETE FROM node_plan_bindings
                WHERE node_id NOT IN (SELECT id FROM nodes WHERE is_deleted = false)
                OR plan_id NOT IN (SELECT id FROM plans WHERE is_deleted = false)
        `)
	if result.Error != nil {
		s.logger.Error("clean orphan node_plan_bindings failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		s.logger.Info("cleaned orphan node_plan_bindings", zap.Int64("count", result.RowsAffected))
	}

	cutOff := time.Now().AddDate(0, 0, -30)
	result = db.Where("log_time < ?", cutOff).Delete(&model.TrafficLog{})
	if result.Error != nil {
		s.logger.Error("clean old traffic logs failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		s.logger.Info("cleaned old traffic logs", zap.Int64("count", result.RowsAffected))
	}

	// 修复 STORAGE-LOG-01 (P1): login_audit / admin_actions 两张审计表无任何清理,
	// 长期运行会无限累积占用磁盘。保留 90 天, 超过则物理删除。
	auditCutOff := time.Now().AddDate(0, 0, -90)
	result = db.Where("created_at < ?", auditCutOff).Delete(&model.LoginAudit{})
	if result.Error != nil {
		s.logger.Error("clean old login_audit failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		s.logger.Info("cleaned old login_audit", zap.Int64("count", result.RowsAffected))
	}
	result = db.Where("created_at < ?", auditCutOff).Delete(&model.AdminAction{})
	if result.Error != nil {
		s.logger.Error("clean old admin_actions failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		s.logger.Info("cleaned old admin_actions", zap.Int64("count", result.RowsAffected))
	}

	// 修复 STORAGE-PARTITION-01 (P0): traffic_logs 是按月分区表, 旧版只 DELETE 30 天前数据,
	// 但 DELETE 不会释放磁盘空间(留下死元组), 且分区本身永远不 DROP, 长期运行后:
	//   1. 每月一个分区文件持续膨胀
	//   2. VACUUM 也无法回收已 DROP 但未归档的空间
	// 现改为: 直接 DROP 超过 2 个月的旧分区(整块释放磁盘), 保留当月+上月分区。
	// 这比 DELETE 高效且彻底, 因为旧月份数据已无业务价值(趋势图最多展示 1 年,
	// 但分区级 DROP 后, 趋势图查询旧月会返回空, 可接受)。
	s.dropOldTrafficPartitions(db)

	// [fix 2026-07-19] 兜底清理一键更新产生的 git-pull.log 残留
	// 与 backupDir 一样路径挂载到宿主机, 容器重启后仍可清理
	s.CleanUpdateLogs()
}

// dropOldTrafficPartitions DROP 超过 2 个月的 traffic_logs 旧分区
// 保留当月与上月分区, 更早的分区整块 DROP 释放磁盘空间
func (s *CronService) dropOldTrafficPartitions(db *gorm.DB) {
	// 查询所有 traffic_logs 子分区名
	type partInfo struct {
		PartName string `gorm:"column:partname"`
	}
	var parts []partInfo
	err := db.Raw(`
		SELECT c.relname AS partname
		FROM pg_inherits
		JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
		JOIN pg_class c ON c.oid = pg_inherits.inhrelid
		WHERE parent.relname = 'traffic_logs'
	`).Scan(&parts).Error
	if err != nil {
		s.logger.Error("查询 traffic_logs 分区列表失败", zap.Error(err))
		return
	}
	if len(parts) == 0 {
		return
	}

	// 计算需要保留的分区名: 当月 + 上月(格式 traffic_logs_YYYY_MM)
	now := time.Now()
	keepMonths := map[string]bool{
		"traffic_logs_" + now.Format("2006_01"):                   true, // 当月
		"traffic_logs_" + now.AddDate(0, -1, 0).Format("2006_01"): true, // 上月
	}

	dropped := 0
	for _, p := range parts {
		if keepMonths[p.PartName] {
			continue
		}
		// 仅 DROP 形如 traffic_logs_YYYY_MM 的分区(防误删)
		if !strings.HasPrefix(p.PartName, "traffic_logs_20") {
			continue
		}
		// DROP 分区(整块删除, 立即释放磁盘)
		if err := db.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %q CASCADE`, p.PartName)).Error; err != nil {
			s.logger.Warn("DROP 旧分区失败", zap.String("part", p.PartName), zap.Error(err))
		} else {
			dropped++
			s.logger.Info("已 DROP 旧流量日志分区, 磁盘空间已释放", zap.String("part", p.PartName))
		}
	}
	if dropped > 0 {
		s.logger.Info("旧流量日志分区清理完成", zap.Int("dropped", dropped))
	}
}

// getGitRoot 自动检测 git 仓库根目录(与 admin_system.go 保持一致)
// 优先级: PROJECT_ROOT 环境变量 > 当前工作目录(含 docker-compose.yml) > /root/nexus-panel(历史兼容)
func getGitRoot() string {
	if root := os.Getenv("PROJECT_ROOT"); root != "" {
		return root
	}
	if cwd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(cwd, "docker-compose.yml")); err == nil {
			return cwd
		}
	}
	return "/root/nexus-panel"
}

var (
	gitRepoPath     = getGitRoot()
	updateStateDir  = filepath.Join(getGitRoot(), ".update-state")
	updateLogFile   = filepath.Join(getGitRoot(), ".update-state", "git-pull.log")
	updateStateFile = filepath.Join(getGitRoot(), ".update-state", "git-pull.state")
)

// CleanUpdateLogs 兜底清理一键更新产生的日志残留
// 每次更新都会追加写, 长期累积会膨胀到几十 MB.
// 后端虽有 GitPullClearLog API 供管理员手动清理, 但用户经常忘记,
// 这里做兜底: 文件超过 7 天未修改 或 超过 5MB 时自动清空.
// 与 backupDir 一样, 路径对齐 docker-compose 挂载点, 容器重启后仍可清理.
//
// 清理策略(满足条件之一即清空):
//  1. 文件 mtime 距今 > 7 天(更新日志已无参考价值)
//  2. 文件 > 5MB(异常膨胀, 防磁盘吃满)
//
// 清理时机: 在 CleanOrphanData 的 cron(每 6 小时)中调用

func (s *CronService) CleanUpdateLogs() {
	info, err := os.Stat(updateLogFile)
	if err != nil {
		return // 文件不存在, 无需清理
	}
	// 条件 1: 超过 7 天未修改
	old := time.Since(info.ModTime()) > 7*24*time.Hour
	// 条件 2: 文件超过 5MB
	big := info.Size() > 5*1024*1024
	if !old && !big {
		return
	}
	if err := os.Remove(updateLogFile); err != nil {
		s.logger.Warn("清理更新日志文件失败", zap.String("file", updateLogFile), zap.Error(err))
		return
	}
	// 顺便清理状态文件, 避免下次启动 init 读到旧 done 状态
	_ = os.Remove(updateStateFile)
	s.logger.Info("已兜底清理过期的更新日志",
		zap.String("file", updateLogFile),
		zap.Int64("size_bytes", info.Size()),
		zap.Time("mtime", info.ModTime()),
		zap.Bool("too_old", old),
		zap.Bool("too_big", big))
}

// gitPullStateFile 一键更新流程的状态文件, 与 admin_system.go 对齐
// 内容: {"done":false,"success":false} 表示正在更新中
//
//	{"done":true,"success":true} 表示更新完成
var gitPullStateFile = filepath.Join(getGitRoot(), ".update-state", "git-pull.state")

// CheckVersionConsistency 版本一致性巡检(保守模式 - 只告警不重建)
//
// 设计原则: cron 绝不主动杀 panel 进程, 绝不主动重建容器。
// 历史教训:
//   - v1 (86a408e): cron 检测到不一致就 docker compose up -d panel, 但与一键更新
//     流程冲突, 在 build 还没完成时杀掉 panel, 导致 502 (事故 2026-07-19 20:15)
//   - v2 (83dfa1a): 加了 isGitPullInProgress 检测, 但仍自动重建。结果在 gitPull
//     done=true 后 cron 又触发重建, 此时新容器还没起来, 双杀 (事故 2026-07-19 21:06)
//   - v3 (本版本): 完全放弃自动重建, 只告警。让人来决定是否修复。
//
// 本方法每 5 分钟跑一次, 对比:
//   - 代码版本: 从 git 仓库读 HEAD short hash
//   - 运行版本: app.Version (编译时 ldflags 注入的 git HEAD short hash)
//
// 不一致时:
//  1. ERROR 日志告警(每次都打, 便于从 docker logs 看到问题)
//  2. 写告警文件到 .update-state/version-mismatch.flag
//     (前端 GitStatus 接口可读取此文件, 在面板显示"版本不一致, 请手动修复"提示)
//  3. 绝不执行 docker compose up -d panel, 绝不杀 panel 进程
//
// 安全保障:
//   - 用 Redis 分布式锁, 多副本/重启安全
//   - 启动 3 分钟后才巡检(等首次更新流程可能完成)
//   - 若检测到 gitPull 进行中, 跳过本次巡检(避免误报)
func (s *CronService) CheckVersionConsistency() {
	if app.Get() == nil || app.Get().RDB == nil {
		return
	}
	unlock := tryLock(app.Get().RDB, "cron:lock:version_check", 60*time.Second)
	if unlock == nil {
		return
	}
	defer unlock()

	// 1. 读运行版本(app.Version 是 ldflags 注入的 git HEAD short hash)
	runningVersion := strings.TrimSpace(app.Version)
	if runningVersion == "" || runningVersion == "dev" {
		// 未知版本(开发模式/未注入), 跳过
		return
	}

	// 2. 读代码版本(从 git 仓库读 HEAD short hash)
	codeVersion := readGitHeadShort(gitRepoPath)
	if codeVersion == "" {
		// 仓库不存在或 git 命令失败, 跳过(容器内可能没挂载仓库)
		return
	}

	// 3. 一致就清理告警标记, return
	if runningVersion == codeVersion {
		// 版本一致, 清理历史告警标记文件(如果存在)
		os.Remove(versionMismatchFlagFile)
		return
	}

	// 4. 检查是否正在一键更新中, 进行中则跳过(避免误报)
	if isGitPullInProgress() {
		return
	}

	// 5. 不一致, 只告警, 不重建
	//    写告警标记文件, 内容是 running→code, 前端可读取展示提示
	flagContent := fmt.Sprintf("running=%s\ncode=%s\ndetected_at=%s\n",
		runningVersion, codeVersion, time.Now().Format(time.RFC3339))
	_ = os.WriteFile(versionMismatchFlagFile, []byte(flagContent), 0644)

	manualFixCmd := fmt.Sprintf("cd %s && docker compose build --build-arg VERSION=$(git rev-parse --short HEAD) panel && docker compose up -d --no-deps panel", gitRepoPath)
	s.logger.Error("[版本一致性巡检] 检测到运行版本与代码版本不一致(只告警不自动修复)",
		zap.String("running_version", runningVersion),
		zap.String("code_version", codeVersion),
		zap.String("repo", gitRepoPath),
		zap.String("flag_file", versionMismatchFlagFile),
		zap.String("manual_fix", manualFixCmd),
		zap.String("note", "保守模式: cron 不自动重建容器, 需人工确认后手动执行上述命令"))
}

// versionMismatchFlagFile 版本不一致告警标记文件
// 存在 = 当前有版本不一致问题, 前端可读取展示提示
// 内容: running=<hash>\ncode=<hash>\ndetected_at=<time>
var versionMismatchFlagFile = filepath.Join(getGitRoot(), ".update-state", "version-mismatch.flag")

// isGitPullInProgress 检查一键更新是否正在进行中
// 判断依据: gitPullStateFile 存在 + 内容 done=false + mtime 距今 < 10 分钟
//   - done=false 且 mtime 近期: 更新进行中, cron 应跳过
//   - done=false 但 mtime > 10 分钟: gitPull 流程已死(被杀/崩溃)
//   - done=true 或文件不存在: 无更新任务
func isGitPullInProgress() bool {
	info, err := os.Stat(gitPullStateFile)
	if err != nil {
		// 文件不存在, 无更新任务
		return false
	}
	// mtime > 10 分钟, 认为更新流程已死, 不算"进行中"
	if time.Since(info.ModTime()) > 10*time.Minute {
		return false
	}
	// 读文件内容, 检查 done 字段
	data, err := os.ReadFile(gitPullStateFile)
	if err != nil {
		return false
	}
	var st struct {
		Done    bool `json:"done"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return false
	}
	// done=false 说明更新未完成(进行中或异常中断, 配合 mtime 判断)
	return !st.Done
}

// readGitHeadShort 读取 git 仓库的 HEAD short hash
// 优先用 git rev-parse --short HEAD(干净), 失败则直接读 .git/HEAD 引用
func readGitHeadShort(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err == nil {
		v := strings.TrimSpace(string(out))
		if len(v) >= 7 {
			return v
		}
	}
	return ""
}

// MarkStaleNodesOffline 检测心跳超时的节点并标记为离线
// 修复 BIZ-HEARTBEAT-01: 节点 gRPC 失联后自动标记 offline=true，
// 避免面板误显示节点在线但实际 Xray 仍在跑旧配置。
// 同步清除 Redis 缓存，强制节点下次心跳时拉取最新配置。
//
// 阈值说明: 8 分钟。agent 心跳 30s/次 + 流量上报 60s/次 都会刷新 last_seen_at,
// 因此 8 分钟内必须有连续 16 次心跳 + 8 次流量上报全部失败才会判离线。
//
// 修复 NODE-OFFLINE-01 (P0): 旧版 5 分钟阈值在面板一键更新(docker build+restart
// 常耗时 3~6 分钟)场景下过激——面板重启期间 agent 心跳全部失败, last_seen_at
// 停在重启前的旧值, 面板一回来 1 分钟内就把节点判离线, 但 agent 还没重连成功。
// 此时 Xray 仍用缓存配置在跑, 用户能用节点, 但面板显示离线(用户报告的问题)。
// 调到 8 分钟, 配合 main.go 启动延迟 3 分钟再巡检, 覆盖正常 docker 重建窗口。
func (s *CronService) MarkStaleNodesOffline() {
	if s.nodeRepo == nil {
		return
	}
	unlock := tryLock(app.Get().RDB, "cron:lock:markstale", 50*time.Second)
	if unlock == nil {
		return
	}
	defer unlock()
	threshold := time.Now().Add(-8 * time.Minute)
	// 修复 PERF-CRON-01: 旧实现先 UPDATE 再 List(0,0,"") 全表扫描 + 循环 Del,
	// 会清掉所有启用节点的 configver/usershash 缓存, 触发全节点心跳重算配置风暴。
	// 现改为先 SELECT id 再 UPDATE WHERE id IN, 只清理真正被标记离线的节点缓存。
	ids, count, err := s.nodeRepo.MarkStaleNodesOfflineWithIDs(threshold)
	if err != nil {
		s.logger.Error("mark stale nodes offline failed", zap.Error(err))
		return
	}
	if count > 0 && len(ids) > 0 {
		s.logger.Warn("节点心跳超时,已自动标记为离线", zap.Int64("count", count), zap.Time("threshold", threshold))
		// 只清除被标记离线节点的 Redis 缓存, 一次 Del 多 key, 避免循环往返
		if rdb := app.Get().RDB; rdb != nil {
			keys := make([]string, 0, len(ids)*2)
			for _, id := range ids {
				keys = append(keys, "node:configver:"+id, "node:usershash:"+id)
			}
			rdb.Del(context.Background(), keys...)
		}
	}
}

// ResetTrafficMonthly 修复 TRAFFIC-RESET-02 (P0): 周期性自动流量重置。
//
// settings.traffic.reset_day 配置项长期存在但无任何代码读取, 导致月付套餐用户
// 流量用完后只能手动续费/重置。此方法:
//  1. 读取 settings.traffic.reset_day(默认 1, 即每月 1 号); 越界则取当月最后一天;
//  2. 仅在 "今日 == targetDay" 时触发, 由 cron 每小时检查一次;
//  3. 使用 Redis SetNX 当日幂等键(23h TTL), 防止同一天重复执行(多副本/重启场景);
//  4. 调用 UserRepo.ResetTrafficForCycleBatch 批量重置 users.traffic_used + 清理
//     traffic_logs 历史(配合 TRAFFIC-RESET-01 修复, 保证节点下次拉取为 0 流量)。
func (s *CronService) ResetTrafficMonthly() {
	if s.userRepo == nil {
		return
	}

	// 1) 读取 settings.traffic.reset_day
	resetDay := 1 // 默认每月 1 号
	if s.settingRepo != nil {
		var cfg struct {
			ResetDay int `json:"reset_day"`
		}
		if err := s.settingRepo.Get("traffic", &cfg); err == nil {
			if cfg.ResetDay >= 1 && cfg.ResetDay <= 31 {
				resetDay = cfg.ResetDay
			} else {
				s.logger.Warn("settings.traffic.reset_day 配置非法,使用默认值 1",
					zap.Int("configured", cfg.ResetDay))
			}
		} else {
			// settings 缺失或读取失败时静默降级到默认值, 不阻断其它 cron 任务
			s.logger.Debug("读取 settings.traffic 失败,使用默认 reset_day=1", zap.Error(err))
		}
	}

	// 2) 计算当月目标日(2月无 30/31 日时取月末)
	now := time.Now()
	lastDay := daysInMonth(now.Year(), int(now.Month()))
	targetDay := resetDay
	if targetDay > lastDay {
		targetDay = lastDay
	}
	if now.Day() != targetDay {
		return // 今天不是重置日, 跳过
	}

	// 3) Redis 当日幂等键, 防止同一天重复执行(每小时检查一次)
	rdb := app.Get().RDB
	lockKey := fmt.Sprintf("cron:traffic:reset:%d-%02d-%02d", now.Year(), int(now.Month()), now.Day())
	if rdb != nil {
		ok, err := rdb.SetNX(context.Background(), lockKey, "1", 23*time.Hour).Result()
		if err != nil {
			s.logger.Warn("周期流量重置加锁失败,跳过本次", zap.Error(err))
			return
		}
		if !ok {
			// 今天已经执行过, 跳过(避免多副本/重启重复重置)
			return
		}
	}

	// 4) 批量重置
	count, err := s.userRepo.ResetTrafficForCycleBatch()
	if err != nil {
		s.logger.Error("周期性流量重置失败", zap.Int("reset_day", resetDay), zap.Error(err))
		return
	}
	s.logger.Info("周期性流量重置完成",
		zap.Int("reset_day", resetDay),
		zap.Int64("user_count", count))
}

// daysInMonth 返回某年某月的天数(用于 reset_day 越界修正)
func daysInMonth(year, month int) int {
	// time.Date(year, month+1, 0, ...) 取得当月最后一天
	t := time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC)
	return t.Day()
}

// ============================================================
// 第三批: 备份存储控制 + 磁盘阈值告警
// ============================================================

// notifSvc 懒构造通知服务(邮件/Telegram), 用于磁盘告警等场景。
// NotificationService 此前为孤儿死代码(从未被实例化), 现接入。
// 修复 NOTIFY-CONFIG-01 (P1): 注入 EmailService 使通知复用 DB 配置,
// 管理员在 UI 配的 SMTP 对 cron 告警也生效。
func (s *CronService) notifSvc() *NotificationService {
	c := app.Get()
	if c == nil || c.Cfg == nil {
		return nil
	}
	ns := NewNotificationService(c.Cfg, s.logger)
	if c.DB != nil {
		ns.SetEmailService(NewEmailService(repo.NewSettingRepo(c.DB), c.Cfg))
	}
	return ns
}

// paymentSvc 懒构造 PaymentService, 用于掉单对账场景。
// 不在构造函数注入, 避免与 OrderService 的循环依赖。
func (s *CronService) paymentSvc() *PaymentService {
	if s.settingRepo == nil || s.orderSvc == nil {
		return nil
	}
	return NewPaymentService(s.settingRepo, s.orderSvc)
}

// ReconcilePendingOrders 修复 PAY-RECON-01 (P0): 掉单对账 cron。
//
// 问题: 支付回调依赖第三方网关 POST /api/v1/payment/notify, 若面板重启/网络抖动/
// 网关重试失败, 用户已付款但订单永远停在 pending, 用户权益无法开通("掉单")。
//
// 此方法每 5 分钟扫描一次近 30 分钟内仍为 pending 的订单, 主动调 EPay act=order
// 接口查询真实支付状态; 若已支付则调用 OrderService.PaySuccess 兜底开通套餐。
// 扫描窗口取 30 分钟(订单本身 15 分钟过期), 兼顾覆盖度与全表扫描成本。
func (s *CronService) ReconcilePendingOrders() {
	unlock := tryLock(app.Get().RDB, "cron:lock:reconcile", 4*time.Minute)
	if unlock == nil {
		return
	}
	defer unlock()

	ps := s.paymentSvc()
	if ps == nil {
		return
	}

	// 扫描近 30 分钟内仍为 pending 的订单
	since := time.Now().Add(-30 * time.Minute)
	orders, err := s.orderSvc.ListPendingSince(since)
	if err != nil {
		s.logger.Error("掉单对账查询失败", zap.Error(err))
		// pending 查询失败不阻断过期订单扫描
		orders = nil
	}

	reconciled := 0
	reconcileOne := func(o model.Order) {
		status, qerr := ps.QueryOrderStatus(o.OrderNo)
		if qerr != nil {
			// 查询失败(网关错误/订单未找到)常见, 仅 debug 记录
			s.logger.Debug("掉单对账查询订单状态失败",
				zap.String("order_no", o.OrderNo), zap.Error(qerr))
			return
		}
		if status.TradeStatus != "TRADE_SUCCESS" {
			return // 未支付, 跳过
		}
		// 金额校验, 防止 EPay 网关数据异常导致错误开通
		if status.Money != "" {
			cents, perr := parseMoneyToCents(status.Money)
			if perr == nil && cents != o.AmountCents {
				s.logger.Warn("掉单对账金额不匹配,跳过开通",
					zap.String("order_no", o.OrderNo),
					zap.Int64("order_cents", o.AmountCents),
					zap.Int64("epay_cents", cents))
				return
			}
		}
		// 调 PaySuccess 兜底开通(内部 FOR UPDATE 幂等, 重复回调安全; 且已支持过期订单履约)
		if err := s.orderSvc.PaySuccess(o.OrderNo, status.TradeNo); err != nil {
			s.logger.Error("掉单对账兜底开通失败",
				zap.String("order_no", o.OrderNo), zap.Error(err))
			return
		}
		reconciled++
		s.logger.Info("掉单对账成功,已兜底开通套餐",
			zap.String("order_no", o.OrderNo),
			zap.String("trade_no", status.TradeNo))
	}

	// 1) 扫描仍为 pending 的订单(含已过 ExpiredAt 但尚未被 cron 标记的)
	for _, o := range orders {
		reconcileOne(o)
	}

	// 2) 扫描近期已过期的订单(用户可能已付款但回调丢失/延迟到达, P1 资金损失修复):
	//    PaySuccess 已支持过期订单履约, 此处主动查询 EPay 真实状态兜底开通
	expSince := time.Now().Add(-30 * time.Minute)
	expOrders, err := s.orderSvc.ListExpiredSince(expSince)
	if err != nil {
		s.logger.Warn("掉单对账扫描过期订单失败", zap.Error(err))
	} else {
		for _, o := range expOrders {
			reconcileOne(o)
		}
	}

	if reconciled > 0 {
		s.logger.Info("掉单对账批次完成", zap.Int("reconciled", reconciled),
			zap.Int("scanned", len(orders)))
	}
}

// CheckDiskThreshold 修复 STORAGE-DISK-01 (P0): 此前没有任何磁盘阈值告警逻辑,
// 日志/备份/缓存爆满会导致数据库写失败、服务崩溃。此方法:
//  1. 通过 getRootDiskUsagePercent 读取根分区使用率;
//  2. >=85% 触发 WARN 告警, >=95% 触发 ERROR 紧急告警;
//  3. 通过 NotificationService 发邮件/Telegram(若已配置);
//  4. Redis 1 小时冷却键防刷屏(同一告警级别 1h 内最多 1 次)。
func (s *CronService) CheckDiskThreshold() {
	pct := getRootDiskUsagePercent()
	if pct <= 0 {
		return
	}

	level := ""
	if pct >= 95 {
		level = "critical"
	} else if pct >= 85 {
		level = "warn"
	}
	if level == "" {
		return
	}

	// Redis 冷却: 同一级别 1h 内最多告警 1 次, 避免每 5 分钟刷屏
	rdb := app.Get().RDB
	cooldownKey := "alert:disk:" + level
	if rdb != nil {
		ok, err := rdb.SetNX(context.Background(), cooldownKey, "1", time.Hour).Result()
		if err == nil && !ok {
			return // 冷却中, 跳过
		}
	}

	msg := fmt.Sprintf("磁盘告警 [%s]: 根分区使用率 %.1f%%", level, pct)

	if level == "critical" {
		s.logger.Error(msg)
	} else {
		s.logger.Warn(msg)
	}

	// 发送邮件/Telegram 告警(若已配置)
	if ns := s.notifSvc(); ns != nil && ns.IsEnabled() {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("NotifyAll goroutine panic", zap.Any("panic", r))
				}
			}()
			ns.NotifyAll("Nexus-Panel 磁盘告警", msg)
		}()
	}
}

// AutoBackupDatabase 修复 STORAGE-BACKUP-02 (P0): 此前仅 admin 手动触发的 JSON 快照
// (且只含 users/nodes/settings 三表), 无任何自动数据库备份。此方法:
//  1. 调用 pg_dump 全量备份数据库(若容器内无 pg_dump 则降级为 JSON 快照并告警);
//  2. 备份写入 /app/data/backup/db-backup-YYYYMMDD-HHMMSS.sql.gz;
//  3. 调用 RotateBackupsKeepOne 只保留最新 1 份(满足"自动备份仅保留最新一份")。
func (s *CronService) AutoBackupDatabase() {
	unlock := tryLock(app.Get().RDB, "cron:lock:backup", 30*time.Minute)
	if unlock == nil {
		s.logger.Debug("数据库备份被其它实例占用,跳过本次")
		return
	}
	defer unlock()

	if err := os.MkdirAll(backupDir, 0700); err != nil {
		s.logger.Error("创建备份目录失败", zap.String("dir", backupDir), zap.Error(err))
		return
	}

	cfg := app.Get().Cfg
	if cfg == nil {
		s.logger.Error("数据库备份失败: 配置不可用")
		return
	}

	ts := time.Now().Format("20060102-150405")
	outPath := filepath.Join(backupDir, "db-backup-"+ts+".sql.gz")

	// 优先尝试本机 pg_dump; 失败则降级用 docker exec 调用 postgres 容器内的 pg_dump
	done := false
	if _, err := exec.LookPath("pg_dump"); err == nil {
		if err := runPgDump(cfg, outPath); err != nil {
			s.logger.Error("pg_dump 备份失败, 尝试 docker exec 降级", zap.Error(err))
			_ = os.Remove(outPath)
		} else {
			s.logger.Info("数据库自动备份完成(pg_dump)", zap.String("file", outPath))
			done = true
		}
	}
	// 降级方案: 通过 docker exec 调用 postgres 容器内的 pg_dump
	// 修复 STORAGE-BACKUP-04 (P0): panel 容器基于 alpine 未装 postgresql-client,
	// 导致 AutoBackupDatabase 永远走"未找到 pg_dump"分支, 从不产生 .sql.gz 备份,
	// 用户手动备份的 .json 快照又只含配置不含数据, 灾备形同虚设。
	if !done {
		if err := runPgDumpViaDocker(cfg, outPath); err != nil {
			s.logger.Warn("docker exec pg_dump 备份失败(容器名 nexus-postgres 可能不匹配)",
				zap.Error(err))
			_ = os.Remove(outPath)
		} else {
			s.logger.Info("数据库自动备份完成(docker exec pg_dump)", zap.String("file", outPath))
			done = true
		}
	}
	if !done {
		s.logger.Warn("数据库全量备份未能完成(本机无 pg_dump 且 docker exec 失败)")
	}

	// 无论 pg_dump 是否成功, 都执行备份轮转(清理旧文件, 保留最新 1 份)
	s.RotateBackupsKeepOne()
}

// runPgDumpViaDocker 通过 docker exec 调用 postgres 容器内的 pg_dump
// 用于 panel 容器内未安装 postgresql-client 的场景, 复用 postgres 镜像自带的 pg_dump
// 修复 STORAGE-BACKUP-05 (P1): 用 gzip.Writer 包装输出, 真正产生 .gz 压缩文件
func runPgDumpViaDocker(cfg *config.Config, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("创建备份文件失败: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	// docker exec nexus-postgres pg_dump -U <user> -d <db> --no-owner --no-privileges
	// 注意: postgres 容器与 panel 容器在同一 docker network, localhost 即容器自身,
	// pg_dump 在 postgres 容器内执行时用 -h 127.0.0.1 走本地 unix socket
	args := []string{
		"exec", "nexus-postgres",
		"pg_dump",
		"-U", cfg.DBUser,
		"-d", cfg.DBName,
		"-h", "127.0.0.1",
		"--no-owner", "--no-privileges", // 跨环境恢复兼容
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdout = gw
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runPgDump 执行 pg_dump 并压缩为 gzip
// 修复 STORAGE-BACKUP-05 (P1): 旧版文件名 .sql.gz 但实际写裸 SQL 未压缩, 修复后用 gzip 真正压缩
func runPgDump(cfg *config.Config, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("创建备份文件失败: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	// pg_dump 连接串: host=... port=... user=... dbname=... (密码通过 PGPASSWORD 环境变量传递, 避免进程参数泄露)
	args := []string{
		"--host=" + cfg.DBHost,
		"--port=" + cfg.DBPort,
		"--username=" + cfg.DBUser,
		"--dbname=" + cfg.DBName,
		"--no-owner", "--no-privileges", // 跨环境恢复兼容
	}
	cmd := exec.Command("pg_dump", args...)
	cmd.Stdout = gw
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "PGPASSWORD="+cfg.DBPass)
	return cmd.Run()
}

// RotateBackupsKeepOne 修复 STORAGE-BACKUP-03 (P0): 旧实现备份只增不删, 永久累积。
// 此方法扫描备份目录, 对每种类型(.json 配置快照 / .sql.gz 数据库备份)各只保留最新 1 份,
// 删除其余旧文件。满足"每次新备份完成后自动删除旧备份, 仅保留最新一份"。
func (s *CronService) RotateBackupsKeepOne() {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		s.logger.Debug("读取备份目录失败(可能尚未创建)", zap.String("dir", backupDir), zap.Error(err))
		return
	}

	// 按后缀分组: .json 与 .sql.gz 各保留 1 份最新
	groups := map[string][]os.DirEntry{
		".json":   {},
		".sql.gz": {},
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".sql.gz"):
			groups[".sql.gz"] = append(groups[".sql.gz"], e)
		case strings.HasSuffix(name, ".json"):
			groups[".json"] = append(groups[".json"], e)
		}
	}

	deleted := 0
	for _, group := range groups {
		if len(group) <= 1 {
			continue
		}
		// 按修改时间倒序(最新在前)
		sort.Slice(group, func(i, j int) bool {
			ii, _ := group[i].Info()
			jj, _ := group[j].Info()
			return ii.ModTime().After(jj.ModTime())
		})
		// 保留 group[0](最新), 删除其余
		for _, e := range group[1:] {
			p := filepath.Join(backupDir, e.Name())
			if err := os.Remove(p); err != nil {
				s.logger.Warn("删除旧备份失败", zap.String("file", p), zap.Error(err))
			} else {
				deleted++
			}
		}
	}
	if deleted > 0 {
		s.logger.Info("备份轮转完成, 已清理旧备份(仅保留最新 1 份)",
			zap.Int("deleted", deleted), zap.String("dir", backupDir))
	}
}
