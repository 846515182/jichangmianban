package handler

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
)

// ============================================================
// 工具函数 (被 captcha.go / admin_invite_codes.go 等引用)
// ============================================================

// secureRandInt 加密随机整数 [0, n)
func secureRandInt(n int) (int, error) {
	if n <= 0 {
		return 0, errors.New("invalid range")
	}
	max := big.NewInt(int64(n))
	i, err := rand.Int(rand.Reader, max)
	if err != nil {
		return 0, err
	}
	return int(i.Int64()), nil
}

// getenv 读取环境变量, 带默认值
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// getDB 返回全局 DB 句柄
func getDB() *gorm.DB {
	if a := app.Get(); a != nil {
		return a.DB
	}
	return nil
}

// getRedis 返回全局 Redis 句柄
func getRedis() *redis.Client {
	if a := app.Get(); a != nil {
		return a.RDB
	}
	return nil
}

// randomToken 生成 n 位随机 token (被 captcha.go 引用)
func randomToken(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		idx, _ := secureRandInt(len(letters))
		b[i] = letters[idx]
	}
	return string(b)
}

// getFrontendBase 获取前端地址 (被 email_service.go 引用)
func getFrontendBase() string {
	return strings.TrimRight(getenv("FRONTEND_BASE", "https://panel.example.com"), "/")
}

// invalidateUserTokens 失效某用户所有 token
func invalidateUserTokens(uid string) {
	if uid == "" {
		return
	}
	_ = bumpTokenVersion(context.Background(), uid, "user")
}

// strongEnough 密码强度 (字母 + 数字)
func strongEnough(p string) bool {
	hasLetter, hasDigit := false, false
	for _, ch := range p {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z':
			hasLetter = true
		case ch >= '0' && ch <= '9':
			hasDigit = true
		}
	}
	return hasLetter && hasDigit
}
