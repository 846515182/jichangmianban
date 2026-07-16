package handler

import (
	"crypto/rand"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// 4 位 base32 字符, 排除易混的 0/O/1/l
const captchaChars = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// captchaEntry 验证码条目（带过期时间）
type captchaEntry struct {
	code      string
	expiresAt time.Time
}

var (
	captchaStore = make(map[string]captchaEntry)
	captchaMu    sync.RWMutex
)

// init 启动定时清理协程
func init() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cleanupExpiredCaptcha()
		}
	}()
}

// cleanupExpiredCaptcha 清理过期验证码
func cleanupExpiredCaptcha() {
	captchaMu.Lock()
	defer captchaMu.Unlock()
	now := time.Now()
	for id, entry := range captchaStore {
		if now.After(entry.expiresAt) {
			delete(captchaStore, id)
		}
	}
}

// GetCaptcha 拉图形验证码
// 生产环境不返回验证码明文，仅返回 captcha_id
func GetCaptcha(c *gin.Context) {
	const length = 4
	buf := make([]byte, length)
	for i := 0; i < length; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(captchaChars))))
		buf[i] = captchaChars[n.Int64()]
	}
	code := string(buf)
	id := randomToken(16)

	// 线程安全写入
	captchaMu.Lock()
	captchaStore[id] = captchaEntry{
		code:      code,
		expiresAt: time.Now().Add(10 * time.Minute),
	}
	captchaMu.Unlock()

	// 同步到 Redis (10 分钟过期)
	rdb := getRedis()
	if rdb != nil {
		rdb.Set(c.Request.Context(), "captcha:"+id, code, 10*time.Minute)
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"data": gin.H{
			"captcha_id": id,
			// 生产环境不返回验证码明文
			// "debug": code,
		},
	})
}

// VerifyCaptcha 校验图形验证码
func VerifyCaptcha(c *gin.Context, captchaID, captchaCode string) bool {
	if captchaID == "" || captchaCode == "" {
		return false
	}
	// 1) 内存校验（线程安全）
	captchaMu.Lock()
	entry, ok := captchaStore[captchaID]
	if ok {
		delete(captchaStore, captchaID)
	}
	captchaMu.Unlock()

	if ok {
		// 检查是否过期
		if time.Now().After(entry.expiresAt) {
			return false
		}
		if entry.code == captchaCode {
			return true
		}
	}

	// 2) Redis 兜底
	rdb := getRedis()
	if rdb != nil {
		saved, err := rdb.Get(c.Request.Context(), "captcha:"+captchaID).Result()
		if err == nil && saved == captchaCode {
			rdb.Del(c.Request.Context(), "captcha:"+captchaID)
			return true
		}
	}
	return false
}

