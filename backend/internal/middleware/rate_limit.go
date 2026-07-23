package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/app"
	"nexus-panel/internal/response"
)

// RateLimitOptions 限流配置
type RateLimitOptions struct {
	// KeyPrefix Redis key 前缀(区分不同限流维度, 如 "sub:ip:" / "sub:tok:")
	KeyPrefix string
	// MaxRequests 窗口内最大请求数
	MaxRequests int
	// Window 窗口大小(如 1*time.Minute)
	Window time.Duration
	// KeyFunc 从请求中提取限流 key(如 IP / token / IP+token)
	KeyFunc func(c *gin.Context) string
	// 当 Redis 不可用时的策略: true=fail-open(放行), false=fail-closed(拒绝)
	// 对公开接口默认 fail-open(避免 Redis 故障导致所有用户无法拉取订阅)
	FailOpen bool
}

// RateLimit 基于 Redis 的固定窗口限流中间件
//
// P0-PublicSubscribe: 订阅拉取接口是公开的(无需 JWT), 无限流可被高频请求打爆 DB。
// 用 Redis INCR + EXPIRE 实现固定窗口限流:
//   - 首次请求 INCR 返回 1, 设置 EXPIRE
//   - 后续请求 INCR, 超过阈值返回 429
//   - 窗口过期后 key 自动删除, 重新计数
//
// 多维度限流时, 在路由上链式挂多个 RateLimit 中间件即可(Gin 自动链式调用 c.Next())。
// 适用场景: 公开接口(IP/token 维度)、登录接口(IP 维度)、注册接口(IP 维度)。
func RateLimit(opts RateLimitOptions) gin.HandlerFunc {
	return func(c *gin.Context) {
		rdb := app.Get().RDB
		// P0-Redis: 用 IsRedisAvailable() 替代 rdb == nil(rdb 恒非 nil, 但 Redis 可能不可达)
		if rdb == nil || !app.Get().IsRedisAvailable() {
			if opts.FailOpen {
				c.Next()
				return
			}
			response.Fail(c, response.CodeServerError)
			c.Abort()
			return
		}

		key := opts.KeyFunc(c)
		if key == "" {
			// 无法提取 key 时放行(如无 token 的请求)
			c.Next()
			return
		}

		redisKey := fmt.Sprintf("%s%s", opts.KeyPrefix, key)
		ctx := context.Background()

		// INCR 是原子的, 首次返回 1
		count, err := rdb.Incr(ctx, redisKey).Result()
		if err != nil {
			// Redis 出错时按策略降级
			if opts.FailOpen {
				c.Next()
				return
			}
			response.Fail(c, response.CodeServerError)
			c.Abort()
			return
		}
		// 首次请求设置 TTL(后续请求不刷新, 保证窗口固定)
		if count == 1 {
			_ = rdb.Expire(ctx, redisKey, opts.Window).Err()
		}
		// 超限
		if int(count) > opts.MaxRequests {
			// 设置 Retry-After 头, 让客户端知道多久后重试
			ttl, _ := rdb.TTL(ctx, redisKey).Result()
			if ttl > 0 {
				c.Header("Retry-After", fmt.Sprintf("%d", int(ttl.Seconds())))
			}
			c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", opts.MaxRequests))
			c.Header("X-RateLimit-Remaining", "0")
			response.Fail(c, response.CodeRateLimit)
			c.Abort()
			return
		}
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", opts.MaxRequests))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", opts.MaxRequests-int(count)))
		c.Next()
	}
}

// RateLimitByIP 按 IP 限流(快捷构造)
func RateLimitByIP(prefix string, max int, window time.Duration) gin.HandlerFunc {
	return RateLimit(RateLimitOptions{
		KeyPrefix:   prefix,
		MaxRequests: max,
		Window:      window,
		KeyFunc:     func(c *gin.Context) string { return c.ClientIP() },
		FailOpen:    true, // 公开接口 Redis 故障时放行, 避免全站不可用
	})
}

// RateLimitByParam 按查询参数限流(如 token)
func RateLimitByParam(paramName, prefix string, max int, window time.Duration) gin.HandlerFunc {
	return RateLimit(RateLimitOptions{
		KeyPrefix:   prefix,
		MaxRequests: max,
		Window:      window,
		KeyFunc:     func(c *gin.Context) string { return c.Query(paramName) },
		FailOpen:    true,
	})
}
