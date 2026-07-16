package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/app"
	"nexus-panel/internal/response"
)

// LoginLockGuard 登录前置风控中间件
// 在登录接口前调用，按 IP 维度检查是否已被风控锁定
func LoginLockGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		rdb := app.Get().RDB
		if rdb == nil {
			c.Next()
			return
		}
		ctx := c.Request.Context()
		ip := c.ClientIP()

		// 检查 IP 是否被锁定
		lockKey := fmt.Sprintf("loginlock:%s", ip)
		if ttl, err := rdb.TTL(ctx, lockKey).Result(); err == nil && ttl > 0 {
			response.FailWithHTTP(c, http.StatusTooManyRequests, response.CodeAccountLocked)
			c.Abort()
			return
		}
		c.Next()
	}
}

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
// 同时累计 IP 与账号维度，达到阈值(默认5次)则锁定 15 分钟
// 返回锁定后剩余次数(已锁定返回 0)
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

	// IP 维度
	ipKey := fmt.Sprintf("loginfail:%s", ip)
	ipCount, _ := rdb.Incr(ctx, ipKey).Result()
	if ipCount == 1 {
		rdb.Expire(ctx, ipKey, lockTTL)
	}
	if ipCount >= int64(maxFail) {
		rdb.Set(ctx, fmt.Sprintf("loginlock:%s", ip), "1", lockTTL)
		return 0, true
	}
	if maxFail-int(ipCount) < remaining {
		remaining = maxFail - int(ipCount)
	}
	return remaining, false
}

// RecordLoginSuccess 登录成功后清空失败计数
func RecordLoginSuccess(ctx context.Context, ip, account string) {
	rdb := app.Get().RDB
	if rdb == nil {
		return
	}
	rdb.Del(ctx, fmt.Sprintf("loginfail:%s", ip))
	rdb.Del(ctx, fmt.Sprintf("loginlock:%s", ip))
	if account != "" {
		rdb.Del(ctx, fmt.Sprintf("loginfail:acc:%s", account))
		rdb.Del(ctx, fmt.Sprintf("loginlock:acc:%s", account))
	}
}
