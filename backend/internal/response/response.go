package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 统一错误码定义
const (
	CodeSuccess            = 0
	CodeParamError         = 40001
	CodeAccountPwdError    = 40101
	CodeAccountDisabled    = 40102
	CodeTokenInvalid       = 40103
	CodeAccountLocked      = 40104
	CodeNoPermission       = 40301
	CodeSubSigExpired      = 40302
	CodeNotFound           = 40401
	CodeDuplicate          = 40901
	CodeRateLimit          = 42901
	CodeIPBlacklist        = 42902
	CodeServerError        = 50001
	CodeDBError            = 50002
	CodeConfigGenFailed    = 50004
)

// 错误码对应的默认消息
var codeMsg = map[int]string{
	CodeSuccess:          "成功",
	CodeParamError:       "参数错误",
	CodeAccountPwdError:  "账号或密码错误",
	CodeAccountDisabled:  "账号已禁用",
	CodeTokenInvalid:     "Token 失效",
	CodeAccountLocked:    "账号已锁定",
	CodeNoPermission:     "无权限",
	CodeSubSigExpired:    "订阅签名已过期",
	CodeNotFound:         "资源不存在",
	CodeDuplicate:        "资源重复",
	CodeRateLimit:        "请求过于频繁",
	CodeIPBlacklist:      "IP 已被拉黑",
	CodeServerError:      "服务异常",
	CodeDBError:          "数据库错误",
	CodeConfigGenFailed:  "配置生成失败",
}

// Msg 获取错误码对应的默认消息
func Msg(code int) string {
	if m, ok := codeMsg[code]; ok {
		return m
	}
	return "未知错误"
}

// Result 统一返回结构
type Result struct {
	Code int         `json:"code"`
	Data interface{} `json:"data,omitempty"`
	Msg  string      `json:"msg"`
}

// OK 返回成功响应
func OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Result{
		Code: CodeSuccess,
		Data: data,
		Msg:  Msg(CodeSuccess),
	})
}

// OKMsg 返回带自定义消息的成功响应
func OKMsg(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, Result{
		Code: CodeSuccess,
		Msg:  msg,
	})
}

// httpStatusForCode 根据错误码返回对应的 HTTP 状态码
func httpStatusForCode(code int) int {
	switch {
	case code >= 50000:
		return http.StatusInternalServerError
	case code >= 42900:
		return http.StatusTooManyRequests
	case code >= 40900:
		return http.StatusConflict
	case code >= 40400:
		return http.StatusNotFound
	case code >= 40300:
		return http.StatusForbidden
	case code >= 40100:
		return http.StatusUnauthorized
	case code >= 40000:
		return http.StatusBadRequest
	default:
		return http.StatusOK
	}
}

// Fail 返回失败响应（根据错误码自动映射 HTTP 状态码）
func Fail(c *gin.Context, code int) {
	c.JSON(httpStatusForCode(code), Result{
		Code: code,
		Msg:  Msg(code),
	})
}

// FailMsg 返回带自定义消息的失败响应
func FailMsg(c *gin.Context, code int, msg string) {
	c.JSON(httpStatusForCode(code), Result{
		Code: code,
		Msg:  msg,
	})
}

// FailWithHTTP 返回指定 HTTP 状态码的失败响应(用于限流等需特殊状态码的场景)
func FailWithHTTP(c *gin.Context, httpCode, code int) {
	c.JSON(httpCode, Result{
		Code: code,
		Msg:  Msg(code),
	})
}
