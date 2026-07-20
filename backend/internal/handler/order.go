package handler

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nexus-panel/internal/middleware"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// OrderHandler 订单处理器
type OrderHandler struct {
	orderSvc   *service.OrderService
	paymentSvc *service.PaymentService
}

// NewOrderHandler 创建订单处理器
func NewOrderHandler(o *service.OrderService, p *service.PaymentService) *OrderHandler {
	return &OrderHandler{orderSvc: o, paymentSvc: p}
}

// createOrderRequest 创建订单请求
type createOrderRequest struct {
	PlanID        string `json:"plan_id" binding:"required"`
	PaymentMethod string `json:"payment_method" binding:"required"`
	CouponCode    string `json:"coupon_code"`
}

// CreateOrder [33] POST /api/v1/user/orders
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	uid := middleware.GetUserID(c)
	var req createOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	order, err := h.orderSvc.CreateOrder(&service.CreateOrderInput{
		UserID:        uid,
		PlanID:        req.PlanID,
		CouponCode:    req.CouponCode,
		PaymentMethod: req.PaymentMethod,
	})
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, order)
}

// ListUserOrders [34] GET /api/v1/user/orders
func (h *OrderHandler) ListUserOrders(c *gin.Context) {
	uid := middleware.GetUserID(c)
	page, size := parsePage(c)
	list, total, err := h.orderSvc.ListUserOrders(uid, page, size)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// GetOrder [35] GET /api/v1/user/orders/:id
func (h *OrderHandler) GetOrder(c *gin.Context) {
	uid := middleware.GetUserID(c)
	id := c.Param("id")
	order, err := h.orderSvc.GetOrder(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}
	// 校验所属用户
	if order.UserID != uid {
		response.Fail(c, response.CodeNotFound)
		return
	}
	response.OK(c, order)
}

// CancelOrder [36] POST /api/v1/user/orders/:id/cancel
func (h *OrderHandler) CancelOrder(c *gin.Context) {
	uid := middleware.GetUserID(c)
	id := c.Param("id")
	if err := h.orderSvc.CancelOrder(id, uid); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OKMsg(c, "已取消")
}

// PayOrder [37] POST /api/v1/user/orders/:id/pay
// 获取支付链接
func (h *OrderHandler) PayOrder(c *gin.Context) {
	uid := middleware.GetUserID(c)
	id := c.Param("id")
	order, err := h.orderSvc.GetOrder(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}
	if order.UserID != uid {
		response.Fail(c, response.CodeNotFound)
		return
	}
	if order.Status != "pending" {
		response.FailMsg(c, response.CodeServerError, "订单状态不允许支付")
		return
	}
	// 拒绝已过期订单生成支付链接(即使状态仍为 pending 但已过 ExpiredAt, P2)
	if !order.ExpiredAt.IsZero() && order.ExpiredAt.Before(time.Now()) {
		response.FailMsg(c, response.CodeServerError, "订单已过期，请重新下单")
		return
	}
	base := getRequestBaseURL(c)
	res, err := h.paymentSvc.CreatePayment(order, base)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, res)
}

// AdminOrderList [38] GET /api/v1/admin/orders
func (h *OrderHandler) AdminOrderList(c *gin.Context) {
	page, size := parsePage(c)
	status := c.Query("status")
	userID := c.Query("user_id")
	list, total, err := h.orderSvc.ListAllOrders(page, size, status, userID)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// AdminOrderStats [GET] /api/v1/admin/orders/stats
// 返回全局订单统计(各状态计数 + 已支付总金额)
// 修复前端 bug: 原前端只基于当前页数据计算统计, 导致数字不准
func (h *OrderHandler) AdminOrderStats(c *gin.Context) {
	stats, err := h.orderSvc.GetOrderStats()
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, stats)
}

// adminMarkPaidRequest 管理员标记已支付请求
type adminMarkPaidRequest struct {
	TradeNo string `json:"trade_no"` // 外部流水号(可选, 便于对账)
}

// AdminMarkPaid [POST] /api/v1/admin/orders/:id/mark-paid
// 管理员手动标记订单已支付(线下转账/对公付款等场景)
func (h *OrderHandler) AdminMarkPaid(c *gin.Context) {
	id := c.Param("id")
	var req adminMarkPaidRequest
	_ = c.ShouldBindJSON(&req) // body 可为空
	if err := h.orderSvc.AdminMarkPaid(id, req.TradeNo); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OKMsg(c, "已标记为已支付")
}

// adminOrderActionRequest 通用操作请求(reason)
type adminOrderActionRequest struct {
	Reason string `json:"reason"`
}

// AdminRefund [POST] /api/v1/admin/orders/:id/refund
// 管理员对已支付订单执行退款
// 修复 PAY-REFUND-01 (P1): 本地退款成功后, best-effort 同步 EPay 侧退款。
// 第三方退款失败不影响本地退款结果(用户套餐已重置), 仅记录日志便于人工对账。
func (h *OrderHandler) AdminRefund(c *gin.Context) {
	id := c.Param("id")
	var req adminOrderActionRequest
	_ = c.ShouldBindJSON(&req)
	// 先获取订单(含 trade_no / amount)用于第三方退款同步
	o, err := h.orderSvc.GetOrder(id)
	if err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	if err := h.orderSvc.AdminRefund(id, req.Reason); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	// 本地退款已成功, 异步 best-effort 同步 EPay 退款, 不阻塞 API 响应
	if h.paymentSvc != nil && o.TradeNo != "" && o.AmountCents > 0 {
		orderNo := o.OrderNo
		tradeNo := o.TradeNo
		money := fmt.Sprintf("%.2f", float64(o.AmountCents)/100.0)
		go func() {
			if err := h.paymentSvc.RequestRefund(orderNo, tradeNo, money); err != nil {
				log.Printf("[payment] EPay 退款同步失败 order_no=%s trade_no=%s: %v", orderNo, tradeNo, err)
			}
		}()
	}
	response.OKMsg(c, "已退款")
}

// AdminCancelOrder [POST] /api/v1/admin/orders/:id/cancel
// 管理员强制取消订单
func (h *OrderHandler) AdminCancelOrder(c *gin.Context) {
	id := c.Param("id")
	var req adminOrderActionRequest
	_ = c.ShouldBindJSON(&req)
	if err := h.orderSvc.AdminCancelOrder(id, req.Reason); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OKMsg(c, "已取消")
}
