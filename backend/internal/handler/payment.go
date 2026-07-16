package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// PaymentHandler 支付回调处理器
type PaymentHandler struct {
	paymentSvc *service.PaymentService
}

// NewPaymentHandler 创建支付处理器
func NewPaymentHandler(p *service.PaymentService) *PaymentHandler {
	return &PaymentHandler{paymentSvc: p}
}

// Notify [39] GET /api/v1/payment/notify
// EPay 异步回调, 验签 + 处理
func (h *PaymentHandler) Notify(c *gin.Context) {
	params := collectEPayParams(c)
	if len(params) == 0 {
		c.String(http.StatusOK, "fail")
		return
	}
	res, err := h.paymentSvc.HandleNotify(params)
	if err != nil {
		c.String(http.StatusOK, "fail")
		return
	}
	c.String(http.StatusOK, res)
}

// Return [40] GET /api/v1/payment/return
// EPay 同步跳转回前台
func (h *PaymentHandler) Return(c *gin.Context) {
	params := collectEPayParams(c)
	orderNo := params["out_trade_no"]
	tradeStatus := params["trade_status"]
	// 仅做展示, 实际状态以 notify 为准
	c.JSON(http.StatusOK, response.Result{
		Code: response.CodeSuccess,
		Data: gin.H{
			"order_no":     orderNo,
			"trade_status": tradeStatus,
		},
		Msg: response.Msg(response.CodeSuccess),
	})
}

// collectEPayParams 收集 query 与 form 参数(EPay 回调可能为 GET 或 POST form)
func collectEPayParams(c *gin.Context) map[string]string {
	params := map[string]string{}
	// query
	for k, v := range c.Request.URL.Query() {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	// form(POST application/x-www-form-urlencoded)
	if c.Request.Method == http.MethodPost {
		_ = c.Request.ParseForm()
		for k, v := range c.Request.PostForm {
			if len(v) > 0 {
				params[k] = v[0]
			}
		}
	}
	return params
}
