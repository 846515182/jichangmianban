package middleware

import (
	"context"
	"fmt"
	"time"

	"nexus-panel/internal/app"
)

// CheckAccountLocked 检查指定账号是否被锁定(双维度: 账号 + IP)
// 返回是否锁定及剩余锁定时间
//
// P0-LoginIP: 同时检查 IP 维度锁定, 防止分布式撞库的 IP 继续尝试其他账号。
func CheckAccountLocked(ctx context.Context, account string) (bool, time.Duration) {
	return CheckLoginLocked(ctx, "", account)
}

// CheckLoginLocked 检查登录是否被锁定(双维度: IP + 账号)
// ip 和 account 任一被锁定即返回 true。
// P0-LoginIP: 恢复 IP 维度检查
// P0-Redis: 用 IsRedisAvailable() 替代 rdb == nil(rdb 恒非 nil, 但 Redis 可能不可达)
func CheckLoginLocked(ctx context.Context, ip, account string) (bool, time.Duration) {
	rdb := app.Get().RDB
	if rdb == nil || !app.Get().IsRedisAvailable() {
		// Redis 不可用时 fail-open: 不锁定(避免 Redis 故障导致所有用户无法登录)
		// 但会丢失锁定状态(Redis 恢复后之前积累的失败计数会清零)
		return false, 0
	}
	// 账号维度
	if account != "" {
		accKey := fmt.Sprintf("loginlock:acc:%s", account)
		if ttl, err := rdb.TTL(ctx, accKey).Result(); err == nil && ttl > 0 {
			return true, ttl
		}
	}
	// P0-LoginIP: IP 维度
	if ip != "" {
		ipKey := fmt.Sprintf("loginlock:ip:%s", ip)
		if ttl, err := rdb.TTL(ctx, ipKey).Result(); err == nil && ttl > 0 {
			return true, ttl
		}
	}
	return false, 0
}

// RecordLoginFail 记录一次登录失败
// 双维度计数: 账号维度 + IP 维度, 任一达阈值即锁定。
//
// P0-LoginIP: 恢复 IP 维度计数(之前被显式移除, 导致分布式撞库可绕过:
// 攻击者每 IP 试 4 次换 IP 再试, 账号维度 5 次阈值永远触发不了)。
// 现在双维度独立计数:
//   - 账号维度: 同一账号失败 maxFail 次 → 锁定该账号 15 分钟(防针对单账号爆破)
//   - IP 维度: 同一 IP 失败 maxFail*3 次 → 锁定该 IP 15 分钟(防分布式撞库)
//     (IP 阈值放宽到 3 倍: 允许同一 NAT 出口多用户正常登录失败)
//
// 返回锁定后剩余次数(已锁定返回 0)。
func RecordLoginFail(ctx context.Context, ip, account string) (remaining int, locked bool) {
	rdb := app.Get().RDB
	if rdb == nil || !app.Get().IsRedisAvailable() {
		return 0, false
	}
	cfg := app.Get().Cfg
	maxFail := cfg.LoginMaxFail
	lockTTL := cfg.LoginLockWindow

	// 账号维度
	if account != "" {
		accKey := fmt.Sprintf("loginfail:acc:%s", account)
		accCount, _ := rdb.Incr(ctx, accKey).Result()
		if accCount == 1 {
			rdb.Expire(ctx, accKey, lockTTL)
		}
		if accCount >= int64(maxFail) {
			rdb.Set(ctx, fmt.Sprintf("loginlock:acc:%s", account), "1", lockTTL)
			return 0, true
		}
		remaining = maxFail - int(accCount)
	}

	// P0-LoginIP: 恢复 IP 维度计数(防分布式撞库)
	// IP 阈值放宽到 maxFail*3, 允许同一 NAT 出口多用户正常失败
	if ip != "" {
		ipKey := fmt.Sprintf("loginfail:ip:%s", ip)
		ipCount, _ := rdb.Incr(ctx, ipKey).Result()
		if ipCount == 1 {
			rdb.Expire(ctx, ipKey, lockTTL)
		}
		ipThreshold := int64(maxFail * 3)
		if ipThreshold < 10 {
			ipThreshold = 10 // 最低 10 次, 避免 NAT 环境误锁
		}
		if ipCount >= ipThreshold {
			rdb.Set(ctx, fmt.Sprintf("loginlock:ip:%s", ip), "1", lockTTL)
			// IP 维度锁定时也返回 locked=true
			return 0, true
		}
	}
	return remaining, false
}

// RecordLoginSuccess 登录成功后清空失败计数(双维度: 账号 + IP)
// P0-LoginIP: 恢复 IP 维度清理, 但不清除 IP 锁定(已锁定的 IP 不会因某用户登录成功而解锁)
func RecordLoginSuccess(ctx context.Context, ip, account string) {
	rdb := app.Get().RDB
	if rdb == nil || !app.Get().IsRedisAvailable() {
		return
	}
	if account != "" {
		rdb.Del(ctx, fmt.Sprintf("loginfail:acc:%s", account))
		rdb.Del(ctx, fmt.Sprintf("loginlock:acc:%s", account))
	}
	// P0-LoginIP: 清除账号维度的 IP 失败计数(不清除 IP 锁定, 避免撞库者碰巧命中一次就解锁)
	if ip != "" {
		rdb.Del(ctx, fmt.Sprintf("loginfail:ip:%s", ip))
	}
}
