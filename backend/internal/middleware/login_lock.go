package middleware

import (
	"context"
	"fmt"
	"time"

	"nexus-panel/internal/app"
)

// CheckAccountLocked 检查指定账号是否被锁定
// 返回是否锁定及剩余锁定时间
func CheckAccountLocked(ctx context.Context, account string) (bool, time.Duration) {
	rdb := app.Get().RDB
	if rdb == nil {
		return false, 0
	}
	lockKey := fmt.Sprintf("loginlock:acc:%s", account)
	ttl, err := rdb.TTL(ctx, lockKey).Result()
	if err != nil || ttl <= 0 {
		return false, 0
	}
	return true, ttl
}

// RecordLoginFail 记录一次登录失败
// 累计账号维度，达到阈值(默认5次)则锁定 15 分钟
// 返回锁定后剩余次数(已锁定返回 0)
//
// 注意: ip 参数保留以维持调用方签名兼容, 但不再做 IP 维度计数。
func RecordLoginFail(ctx context.Context, ip, account string) (remaining int, locked bool) {
	rdb := app.Get().RDB
	if rdb == nil {
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
	return remaining, false
}

// RecordLoginSuccess 登录成功后清空失败计数
//
// 注意: ip 参数保留以维持调用方签名兼容, 但不再做 IP 维度清理。
func RecordLoginSuccess(ctx context.Context, ip, account string) {
	rdb := app.Get().RDB
	if rdb == nil {
		return
	}
	if account != "" {
		rdb.Del(ctx, fmt.Sprintf("loginfail:acc:%s", account))
		rdb.Del(ctx, fmt.Sprintf("loginlock:acc:%s", account))
	}
}
