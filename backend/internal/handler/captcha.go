package handler

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
	mrand "math/rand"
	"net/http"
	"os"
	"strings"
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
// 返回 captcha_id + 内联 SVG 图像(base64), 前端 <img src="data:image/svg+xml;base64,..."> 直接展示。
// 不再依赖任何外部图形库, SVG 由本函数纯字符串生成。
func GetCaptcha(c *gin.Context) {
	const length = 4
	buf := make([]byte, length)
	for i := 0; i < length; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(captchaChars))))
		buf[i] = captchaChars[n.Int64()]
	}
	code := string(buf)
	id, err := randomToken(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 50001, "msg": "验证码生成失败"})
		return
	}

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

	// 生成 SVG 验证码图像 (120x40), 字符带随机旋转/位置/颜色 + 干扰线
	svg := buildCaptchaSVG(code, 120, 40)
	svgB64 := base64.StdEncoding.EncodeToString([]byte(svg))

	data := gin.H{
		"captcha_id":  id,
		"captcha_img": "data:image/svg+xml;base64," + svgB64,
		"expires_in":  600,
	}
	// EMAIL_DEBUG=1 时附带明文, 便于自动化测试/本地调试
	if os.Getenv("EMAIL_DEBUG") == "1" {
		data["debug"] = code
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"data": data,
	})
}

// buildCaptchaSVG 生成验证码 SVG 图像 (纯字符串拼接, 无外部依赖)
// 每个字符随机: x 偏移、旋转角度、字号、颜色; 再叠加 4 条干扰线段 + 20 个噪点。
func buildCaptchaSVG(code string, w, h int) string {
	// 颜色调色板 (深色系, 在浅色背景下清晰)
	palette := []string{
		"#0f4c75", "#1b6ca8", "#3a0ca3", "#4361ee",
		"#7209b7", "#b5179e", "#f72585", "#2a9d8f",
	}
	var b []byte
	add := func(s string) { b = append(b, s...) }

	add(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`,
		w, h, w, h,
	))
	// 背景
	add(fmt.Sprintf(`<rect width="%d" height="%d" fill="#f1f5f9"/>`, w, h))

	// 干扰线 (3 条, [fix 2026-07-18] 由 4 降到 3 减少识别难度)
	for i := 0; i < 3; i++ {
		x1 := mrand.Intn(w)
		y1 := mrand.Intn(h)
		x2 := mrand.Intn(w)
		y2 := mrand.Intn(h)
		col := palette[mrand.Intn(len(palette))]
		add(fmt.Sprintf(
			`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1" opacity="0.4"/>`,
			x1, y1, x2, y2, col,
		))
	}

	// 噪点 (12 个, [fix 2026-07-18] 由 20 降到 12)
	for i := 0; i < 12; i++ {
		x := mrand.Intn(w)
		y := mrand.Intn(h)
		col := palette[mrand.Intn(len(palette))]
		add(fmt.Sprintf(`<circle cx="%d" cy="%d" r="1" fill="%s" opacity="0.3"/>`, x, y, col))
	}

	// 字符 (每个字符占 w/len(code) 区间, 居中带随机扰动)
	// [fix 2026-07-18] 旋转角度由 ±30° 降到 ±20°, 字号加大到 24..29, 降低看错率
	cellW := w / len(code)
	for i, ch := range code {
		x := i*cellW + cellW/2 + (mrand.Intn(6) - 3)
		y := h/2 + (mrand.Intn(6) - 3)
		rot := mrand.Intn(40) - 20 // -20..+20 度
		size := 24 + mrand.Intn(6)  // 24..29
		col := palette[mrand.Intn(len(palette))]
		add(fmt.Sprintf(
			`<text x="%d" y="%d" font-family="Arial,Helvetica,sans-serif" font-size="%d" font-weight="bold" fill="%s" text-anchor="middle" dominant-baseline="middle" transform="rotate(%d %d %d)">%s</text>`,
			x, y, size, col, rot, x, y, string(ch),
		))
	}
	add(`</svg>`)
	return string(b)
}

// VerifyCaptcha 校验图形验证码
// [安全修复 P1] 验证码一次性校验: 无论成功失败, 校验后立即从内存和 Redis 删除,
// 防止对同一 captcha_id 暴力枚举。验证码不存在(已过期或已删除)视为校验失败。
func VerifyCaptcha(c *gin.Context, captchaID, captchaCode string) bool {
	if captchaID == "" || captchaCode == "" {
		return false
	}
	// 统一转大写比较（字符集不含小写字母，前端可能输入小写）
	captchaCode = strings.ToUpper(strings.TrimSpace(captchaCode))

	// 1) 内存校验（线程安全）— 一次性: 取出后立即删除, 防止暴力枚举
	captchaMu.Lock()
	entry, ok := captchaStore[captchaID]
	if ok {
		delete(captchaStore, captchaID)
	}
	captchaMu.Unlock()

	if ok && !time.Now().After(entry.expiresAt) {
		if subtle.ConstantTimeCompare([]byte(entry.code), []byte(captchaCode)) == 1 {
			return true
		}
	}

	// 2) Redis 兜底 — 一次性: Get 后立即 Del, 无论是否匹配
	rdb := getRedis()
	if rdb != nil {
		ctx := c.Request.Context()
		key := "captcha:" + captchaID
		saved, err := rdb.Get(ctx, key).Result()
		// 无论命中与否, 立即删除, 防止再次尝试
		rdb.Del(ctx, key)
		if err == nil && strings.EqualFold(saved, captchaCode) {
			return true
		}
	}
	return false
}

