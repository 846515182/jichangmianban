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

// 限流场景
const (
	RateScopeUser  = "user"
	RateScopeAdmin = "admin"
	RateScopeSub   = "sub"
)

// 限流违规阈值(60s 内违规次数超过该值则拉黑 IP)
const rateViolBanThreshold = 100

// RateLimit 基于 Redis 的固定窗口限流中间件
// scope 决定使用哪种速率配置
func RateLimit(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		rdb := app.Get().RDB
		if rdb == nil {
			// Redis 不可用时直接放行，避免阻断服务
			c.Next()
			return
		}
		ctx := c.Request.Context()
		ip := c.ClientIP()

		// 1. 检查 IP 是否已被拉黑
		banKey := fmt.Sprintf("ipban:%s", ip)
		if banned, _ := rdb.Exists(ctx, banKey).Result(); banned > 0 {
			response.FailWithHTTP(c, http.StatusTooManyRequests, response.CodeIPBlacklist)
			c.Abort()
			return
		}

		// 2. 确定限流主体(已认证用户按用户ID，否则按IP)
		identity := ip
		if uid, ok := c.Get(string(CtxUserID)); ok {
			if s, ok := uid.(string); ok && s != "" {
				identity = s
			}
		}

		// 3. 确定速率
		limit := rateForScope(scope)
		now := time.Now().Unix()
		rlKey := fmt.Sprintf("rl:%s:%s:%d", scope, identity, now)

		// INCR + EXPIRE 原子化处理
		pipe := rdb.Pipeline()
		incr := pipe.Incr(ctx, rlKey)
		pipe.Expire(ctx, rlKey, 2*time.Second)
		if _, err := pipe.Exec(ctx); err != nil {
			// Redis 出错时不阻断请求
			c.Next()
			return
		}
		count, _ := incr.Result()

		// 4. 超限处理
		if count > int64(limit) {
			// 累计违规次数
			violKey := fmt.Sprintf("rlviol:%s", ip)
			violCount, _ := rdb.Incr(ctx, violKey).Result()
			if violCount == 1 {
				rdb.Expire(ctx, violKey, 60*time.Second)
			}
			// 超过阈值则拉黑 IP 1 小时
			if violCount >= rateViolBanThreshold {
				rdb.Set(ctx, banKey, "1", app.Get().Cfg.IPBanTTL)
				response.FailWithHTTP(c, http.StatusTooManyRequests, response.CodeIPBlacklist)
				c.Abort()
				return
			}
			response.FailWithHTTP(c, http.StatusTooManyRequests, response.CodeRateLimit)
			c.Abort()
			return
		}

		c.Next()
	}
}

// rateForScope 根据场景返回每秒请求上限
func rateForScope(scope string) int {
	cfg := app.Get().Cfg
	switch scope {
	case RateScopeUser:
		return cfg.RateUser
	case RateScopeAdmin:
		return cfg.RateAdmin
	case RateScopeSub:
		return cfg.RateSub
	default:
		return cfg.RateUser
	}
}

// BanIP 显式拉黑 IP(供其它模块调用，如登录暴力破解)
func BanIP(ip string, ttl time.Duration) error {
	rdb := app.Get().RDB
	if rdb == nil {
		return nil
	}
	if ttl <= 0 {
		ttl = app.Get().Cfg.IPBanTTL
	}
	return rdb.Set(context.Background(), fmt.Sprintf("ipban:%s", ip), "1", ttl).Err()
}
