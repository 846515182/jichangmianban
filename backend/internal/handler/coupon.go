package handler

import (
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nexus-panel/internal/middleware"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
)

// CouponHandler 优惠券处理器
type CouponHandler struct {
	couponRepo *repo.CouponRepo
	orderRepo  *repo.OrderRepo
}

// NewCouponHandler 创建优惠券处理器
func NewCouponHandler(cr *repo.CouponRepo, or *repo.OrderRepo) *CouponHandler {
	return &CouponHandler{couponRepo: cr, orderRepo: or}
}

// createCouponRequest 创建优惠券请求
// 修复 P2: code 字段在 model.Coupon 为 varchar(32), 加 max=32 长度校验,
// type 加 oneof 白名单(虽然 handler 内也校验了, 提前在 binding 拒绝更清晰)
type createCouponRequest struct {
	Code           string     `json:"code" binding:"required,max=32"`
	Type           string     `json:"type" binding:"required,oneof=percent fixed"`
	Value          int64      `json:"value" binding:"required"`
	MinAmountCents int64      `json:"min_amount_cents"`
	MaxUses        int        `json:"max_uses"`
	ExpireAt       *time.Time `json:"expire_at"`
	IsEnabled      bool       `json:"is_enabled"`
}

// AdminCouponList [41] GET /api/v1/admin/coupons
func (h *CouponHandler) AdminCouponList(c *gin.Context) {
	page, size := parsePage(c)
	keyword := c.Query("keyword")
	list, total, err := h.couponRepo.List(page, size, keyword)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// AdminCouponCreate [42] POST /api/v1/admin/coupons
func (h *CouponHandler) AdminCouponCreate(c *gin.Context) {
	var req createCouponRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if req.Type != model.CouponTypePercent && req.Type != model.CouponTypeFixed {
		response.FailMsg(c, response.CodeParamError, "优惠券类型无效")
		return
	}
	if req.Type == model.CouponTypePercent && (req.Value < 1 || req.Value > 100) {
		response.FailMsg(c, response.CodeParamError, "百分比折扣需在 1-100 之间")
		return
	}
	coupon := &model.Coupon{
		Code:           req.Code,
		Type:           req.Type,
		Value:          req.Value,
		MinAmountCents: req.MinAmountCents,
		MaxUses:        req.MaxUses,
		ExpireAt:       req.ExpireAt,
		IsEnabled:      req.IsEnabled,
	}
	if err := h.couponRepo.Create(coupon); err != nil {
		response.FailMsg(c, response.CodeServerError, err.Error())
		return
	}
	response.OK(c, coupon)
}

// updateCouponRequest 编辑优惠券入参(指针字段支持部分更新, 未传则保持原值)
// 修复 P0-4: 新增 clear_expire_at 字段, 显式标记清空过期时间
// 原因: *time.Time 无法区分 "null 清空" 和 "字段缺失不改", 二者都为 nil,
// 导致前端发 expire_at: null 时被当作 "未传" 而跳过赋值, 清空功能失效
type updateCouponRequest struct {
	Code           *string    `json:"code"`
	Type           *string    `json:"type"`
	Value          *int64     `json:"value"`
	MinAmountCents *int64     `json:"min_amount_cents"`
	MaxUses        *int       `json:"max_uses"`
	ExpireAt       *time.Time `json:"expire_at"`
	ClearExpireAt  *bool      `json:"clear_expire_at"` // ✅ 显式清空过期时间(优先级高于 expire_at)
	IsEnabled      *bool      `json:"is_enabled"`
}

// AdminCouponUpdate PUT /api/v1/admin/coupons/:id
// 编辑优惠券(部分更新, 指针字段未传则保持原值)
// 安全限制: type 字段不允许跨类型修改, 避免 value 单位混乱(percent 是 1-100 整数, fixed 是分)
func (h *CouponHandler) AdminCouponUpdate(c *gin.Context) {
	id := c.Param("id")
	coupon, err := h.couponRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}
	var req updateCouponRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	// type 字段不允许跨类型修改, 防止 value 单位混乱(percent 是百分比整数, fixed 是分)
	if req.Type != nil && *req.Type != coupon.Type {
		response.FailMsg(c, response.CodeParamError, "不允许修改优惠券类型, 请重新创建")
		return
	}
	if req.Code != nil && *req.Code != "" {
		coupon.Code = *req.Code
	}
	if req.Value != nil {
		if coupon.Type == model.CouponTypePercent && (*req.Value < 1 || *req.Value > 100) {
			response.FailMsg(c, response.CodeParamError, "百分比折扣需在 1-100 之间")
			return
		}
		if coupon.Type == model.CouponTypeFixed && *req.Value < 0 {
			response.FailMsg(c, response.CodeParamError, "优惠金额不能为负")
			return
		}
		coupon.Value = *req.Value
	}
	if req.MinAmountCents != nil {
		coupon.MinAmountCents = *req.MinAmountCents
	}
	if req.MaxUses != nil {
		coupon.MaxUses = *req.MaxUses
	}
	// 修复 P0-4: 优先处理 clear_expire_at(显式清空), 否则按 expire_at 更新
	// 原代码: if req.ExpireAt != nil { coupon.ExpireAt = req.ExpireAt }
	// 问题: 前端发 expire_at: null 时 req.ExpireAt 为 nil, 无法区分 "清空" vs "未传"
	if req.ClearExpireAt != nil && *req.ClearExpireAt {
		coupon.ExpireAt = nil
	} else if req.ExpireAt != nil {
		coupon.ExpireAt = req.ExpireAt
	}
	if req.IsEnabled != nil {
		coupon.IsEnabled = *req.IsEnabled
	}
	if err := h.couponRepo.Update(coupon); err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OK(c, coupon)
}

// AdminCouponDelete [43] DELETE /api/v1/admin/coupons/:id
// 安全检查(P2): 引用该优惠券的未完结订单存在时拒绝删除
// 软删除后这些订单退款/取消时 DecrUsedSafeTx 会失败, 造成 used_count 永久虚高
func (h *CouponHandler) AdminCouponDelete(c *gin.Context) {
	id := c.Param("id")
	coupon, err := h.couponRepo.GetByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}
	if count, err := h.orderRepo.CountActiveByCoupon(coupon.ID); err == nil && count > 0 {
		response.FailMsg(c, response.CodeServerError, fmt.Sprintf("该优惠券仍有 %d 笔未完结订单引用，请先处理订单或禁用优惠券而非删除", count))
		return
	}
	if err := h.couponRepo.SoftDelete(id); err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OKMsg(c, "已删除")
}

// AdminCouponToggleStatus PATCH /api/v1/admin/coupons/:id/status
func (h *CouponHandler) AdminCouponToggleStatus(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if req.Status != "active" && req.Status != "disabled" {
		response.FailMsg(c, response.CodeParamError, "status must be active or disabled")
		return
	}
	enabled := req.Status == "active"
	if err := h.couponRepo.ToggleStatus(id, enabled); err != nil {
		response.Fail(c, response.CodeServerError)
		return
	}
	response.OKMsg(c, "已更新")
}

// validateCouponRequest 验证优惠券请求

type validateCouponRequest struct {
	Code        string `json:"code" binding:"required"`
	OrderID     string `json:"order_id"`
	AmountCents int64  `json:"amount_cents"` // 套餐价格(分), 未下单时用于计算折扣
}

// UserCouponValidate [44] POST /api/v1/user/coupon/validate
// 验证优惠券码, 返回折扣金额
func (h *CouponHandler) UserCouponValidate(c *gin.Context) {
	uid := middleware.GetUserID(c)
	var req validateCouponRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	coupon, err := h.couponRepo.GetByCode(req.Code)
	if err != nil {
		response.FailMsg(c, response.CodeNotFound, "优惠券无效")
		return
	}
	if !h.couponRepo.IsValid(coupon, time.Now()) {
		response.FailMsg(c, response.CodeServerError, "优惠券已失效或已用完")
		return
	}
	// 若提供 order_id, 则计算该订单实际折扣
	if req.OrderID != "" {
		order, err := h.orderRepo.GetByID(req.OrderID)
		if err != nil {
			response.Fail(c, response.CodeNotFound)
			return
		}
		if order.UserID != uid {
			response.Fail(c, response.CodeNotFound)
			return
		}
		discount, err := calcUserCouponDiscount(coupon, order.AmountCents)
		if err != nil {
			response.FailMsg(c, response.CodeServerError, err.Error())
			return
		}
		amount := order.AmountCents - discount
		if amount < 0 {
			amount = 0
		}
		response.OK(c, gin.H{
			"valid":          true,
			"discount_cents": discount,
			"amount_cents":   amount,
			"type":           coupon.Type,
			"value":          coupon.Value,
		})
		return
	}
	// 未提供 order_id 但提供 amount_cents: 基于套餐价格计算实际折扣(下单前预览)
	if req.AmountCents > 0 {
		discount, err := calcUserCouponDiscount(coupon, req.AmountCents)
		if err != nil {
			response.FailMsg(c, response.CodeServerError, err.Error())
			return
		}
		amount := req.AmountCents - discount
		if amount < 0 {
			amount = 0
		}
		response.OK(c, gin.H{
			"valid":          true,
			"discount_cents": discount,
			"amount_cents":   amount,
			"type":           coupon.Type,
			"value":          coupon.Value,
		})
		return
	}
	// 未提供 order_id 与 amount_cents 时只返回优惠券基础信息
	response.OK(c, gin.H{
		"valid":            true,
		"type":             coupon.Type,
		"value":            coupon.Value,
		"min_amount_cents": coupon.MinAmountCents,
	})
}

// calcUserCouponDiscount 计算优惠券折扣金额(与 service.calcCouponDiscount 一致, 复用前端校验)
func calcUserCouponDiscount(c *model.Coupon, amount int64) (int64, error) {
	if amount < c.MinAmountCents {
		return 0, errors.New("订单金额未达最低消费")
	}
	switch c.Type {
	case model.CouponTypePercent:
		if c.Value < 1 || c.Value > 100 {
			return 0, errors.New("优惠券折扣比例无效")
		}
		return amount * c.Value / 100, nil
	case model.CouponTypeFixed:
		if c.Value < 0 {
			return 0, errors.New("优惠券金额无效")
		}
		if c.Value > amount {
			return amount, nil
		}
		return c.Value, nil
	default:
		return 0, errors.New("优惠券类型无效")
	}
}
