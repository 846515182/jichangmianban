package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// OrderService 订单服务
type OrderService struct {
	orderRepo  *repo.OrderRepo
	planRepo   *repo.PlanRepo
	couponRepo *repo.CouponRepo
	userRepo   *repo.UserRepo
}

// NewOrderService 创建订单服务
func NewOrderService(o *repo.OrderRepo, p *repo.PlanRepo, c *repo.CouponRepo, u *repo.UserRepo) *OrderService {
	return &OrderService{orderRepo: o, planRepo: p, couponRepo: c, userRepo: u}
}

// CreateOrderInput 创建订单入参
type CreateOrderInput struct {
	UserID        string
	PlanID        string
	CouponCode    string
	PaymentMethod string // epay_alipay/epay_wechat/epay_qq
}

// CreateOrder 创建订单
// 生成订单号 NP+yyyyMMddHHmmss+6位随机, 设15分钟过期, 如有优惠券计算折扣
// 修复: 订单创建 + 优惠券计数在同一事务内, 避免"订单有折扣但计数未增加"
func (s *OrderService) CreateOrder(in *CreateOrderInput) (*model.Order, error) {
	if in.UserID == "" || in.PlanID == "" || in.PaymentMethod == "" {
		return nil, errors.New("缺少必填字段")
	}
	plan, err := s.planRepo.GetByID(in.PlanID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("套餐不存在")
		}
		return nil, err
	}
	if !plan.IsEnabled {
		return nil, errors.New("套餐已下架")
	}
	now := time.Now()
	amount := plan.PriceCents
	// 优惠券折扣计算
	var couponID, couponCode string
	if in.CouponCode != "" {
		coupon, err := s.couponRepo.GetByCode(in.CouponCode)
		if err != nil {
			return nil, errors.New("优惠券无效")
		}
		if !s.couponRepo.IsValid(coupon, now) {
			return nil, errors.New("优惠券已失效或已用完")
		}
		discount, err := calcCouponDiscount(coupon, amount)
		if err != nil {
			return nil, err
		}
		amount -= discount
		if amount < 0 {
			amount = 0
		}
		couponID = coupon.ID
		couponCode = coupon.Code
	}
	orderNo, err := generateOrderNo()
	if err != nil {
		return nil, err
	}
	order := &model.Order{
		OrderNo:       orderNo,
		UserID:        in.UserID,
		PlanID:        plan.ID,
		PlanName:      plan.Name,
		AmountCents:   amount,
		Status:        model.OrderStatusPending,
		PaymentMethod: in.PaymentMethod,
		CouponID:      couponID,
		CouponCode:    couponCode,
		ExpiredAt:     now.Add(15 * time.Minute),
	}
	// 事务: 订单创建 + 优惠券计数, 要么都成功要么都回滚
	db := app.Get().DB
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(order).Error; err != nil {
			return err
		}
		if couponID != "" {
			if err := s.couponRepo.IncrUsedSafeTx(tx, couponID, now); err != nil {
				return errors.New("优惠券已被抢用完, 请刷新重试")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

// GetOrder 获取订单详情(可选校验所属用户)
func (s *OrderService) GetOrder(orderID string) (*model.Order, error) {
	return s.orderRepo.GetByID(orderID)
}

// GetByOrderNo 按订单号查询 - 修复 SEC-P1-02: 支付回调金额校验需要
func (s *OrderService) GetByOrderNo(orderNo string) (*model.Order, error) {
	return s.orderRepo.GetByOrderNo(orderNo)
}

// ListUserOrders 用户订单列表
func (s *OrderService) ListUserOrders(userID string, page, size int) ([]model.Order, int64, error) {
	return s.orderRepo.ListByUserID(userID, page, size)
}

// ListAllOrders 全部订单列表
func (s *OrderService) ListAllOrders(page, size int, status, userID string) ([]model.Order, int64, error) {
	return s.orderRepo.ListAll(page, size, status, userID)
}

// CancelOrder 用户取消订单(仅 pending 可取消)
// 修复 F-12: 改为条件更新, WHERE status='pending' 防止竞态覆盖已支付订单
// 修复: 取消时回退优惠券使用次数
func (s *OrderService) CancelOrder(orderID, userID string) error {
	o, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return err
	}
	if userID != "" && o.UserID != userID {
		return errors.New("无权操作此订单")
	}
	if o.Status != model.OrderStatusPending {
		return errors.New("仅待支付订单可取消")
	}
	// 条件更新: 仅当 status 仍为 pending 时才改为 cancelled
	db := app.Get().DB
	err = db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Order{}).
			Where("id = ? AND status = ?", orderID, model.OrderStatusPending).
			Updates(map[string]interface{}{
				"status":     model.OrderStatusCancelled,
				"updated_at": time.Now(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("订单状态已变更, 无法取消")
		}
		// 回退优惠券使用次数
		if o.CouponID != "" {
			_ = s.couponRepo.DecrUsedSafeTx(tx, o.CouponID)
		}
		return nil
	})
	return err
}

// ExpireOrders 定时清理过期订单(将过期未支付的标记为 expired, 并回退优惠券)
func (s *OrderService) ExpireOrders() (int, error) {
	now := time.Now()
	db := app.Get().DB
	// 先查出要过期的订单(含优惠券信息)
	var toExpire []model.Order
	if err := db.Where("status = ? AND expired_at < ?", model.OrderStatusPending, now).
		Find(&toExpire).Error; err != nil {
		return 0, err
	}
	if len(toExpire) == 0 {
		return 0, nil
	}
	count := 0
	for _, o := range toExpire {
		err := db.Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&model.Order{}).
				Where("id = ? AND status = ?", o.ID, model.OrderStatusPending).
				Update("status", model.OrderStatusExpired)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return nil // 已被并发处理
			}
			if o.CouponID != "" {
				_ = s.couponRepo.DecrUsedSafeTx(tx, o.CouponID)
			}
			return nil
		})
		if err == nil {
			count++
		}
	}
	return count, nil
}

// PaySuccess 支付成功回调处理
// 更新订单状态, 给用户开通套餐(设 traffic_limit, expired_at, plan_id), 延长到期时间
// 修复 F-11: 将 setUserPlan 移入事务, 使用 tx 而非全局 db
// 原实现中 setUserPlan 调用 s.userRepo.Update(), 走的是 r.db(全局), 不在事务内,
// 若事务回滚, 订单状态回滚但用户套餐已生效, 造成"退款不退套餐"
func (s *OrderService) PaySuccess(orderNo, tradeNo string) error {
	o, err := s.orderRepo.GetByOrderNo(orderNo)
	if err != nil {
		return err
	}
	if o.Status == model.OrderStatusPaid {
		return nil
	}
	if o.Status != model.OrderStatusPending {
		return errors.New("订单状态不允许支付")
	}
	now := time.Now()

	db := app.Get().DB
	return db.Transaction(func(tx *gorm.DB) error {
		var locked model.Order
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("id = ?", o.ID).First(&locked).Error; err != nil {
			return err
		}
		if locked.Status == model.OrderStatusPaid {
			return nil
		}
		if locked.Status != model.OrderStatusPending {
			return errors.New("订单状态不允许支付")
		}
		locked.Status = model.OrderStatusPaid
		locked.TradeNo = tradeNo
		locked.PaidAt = &now
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		plan, err := s.planRepo.GetByID(locked.PlanID)
		if err != nil {
			return fmt.Errorf("订单已支付但套餐不存在: %w", err)
		}
		// 关键: 传入 tx, 使 setUserPlan 在同一事务内执行
		return s.setUserPlan(tx, locked.UserID, plan, now)
	})
}

// SetUserPlan 设置用户套餐(对外暴露给注册/手动开通场景)
func (s *OrderService) SetUserPlan(userID string, planID string) error {
	plan, err := s.planRepo.GetByID(planID)
	if err != nil {
		return err
	}
	// 非事务场景传 nil, setUserPlan 内部回退到 userRepo.Update
	return s.setUserPlan(nil, userID, plan, time.Now())
}

// AdminMarkPaid 管理员手动标记订单已支付(线下转账/对公付款等场景)
// 流程: 校验订单存在 + 状态为 pending -> 设置 paid + trade_no + paid_at -> 开套餐(同 PaySuccess)
// 事务保证: 订单状态变更与开通套餐要么都成功, 要么都回滚
func (s *OrderService) AdminMarkPaid(orderID, tradeNo string) error {
	o, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return err
	}
	if o.Status == model.OrderStatusPaid {
		return errors.New("订单已是已支付状态")
	}
	if o.Status != model.OrderStatusPending {
		return errors.New("仅待支付订单可标记为已支付")
	}
	now := time.Now()
	db := app.Get().DB
	return db.Transaction(func(tx *gorm.DB) error {
		var locked model.Order
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("id = ?", o.ID).First(&locked).Error; err != nil {
			return err
		}
		if locked.Status == model.OrderStatusPaid {
			return errors.New("订单已被其他操作处理")
		}
		if locked.Status != model.OrderStatusPending {
			return errors.New("订单状态不允许标记为已支付")
		}
		locked.Status = model.OrderStatusPaid
		locked.TradeNo = tradeNo
		locked.PaidAt = &now
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		plan, err := s.planRepo.GetByID(locked.PlanID)
		if err != nil {
			return fmt.Errorf("订单已标记已支付但套餐不存在: %w", err)
		}
		return s.setUserPlan(tx, locked.UserID, plan, now)
	})
}

// AdminRefund 管理员对已支付订单执行退款
// 流程: 校验订单存在 + 状态为 paid -> 设置 refunded -> 回退优惠券 -> 不动用户套餐
// 修复: 退款时回退优惠券使用次数
func (s *OrderService) AdminRefund(orderID, reason string) error {
	o, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return err
	}
	if o.Status == model.OrderStatusRefunded {
		return errors.New("订单已是已退款状态")
	}
	if o.Status != model.OrderStatusPaid {
		return errors.New("仅已支付订单可退款")
	}
	db := app.Get().DB
	return db.Transaction(func(tx *gorm.DB) error {
		var locked model.Order
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("id = ?", o.ID).First(&locked).Error; err != nil {
			return err
		}
		if locked.Status != model.OrderStatusPaid {
			return errors.New("订单状态已变更, 无法退款")
		}
		// 将 reason 写入 trade_no 后缀(便于审计); 截断长度保护列宽
		if reason != "" {
			suffix := " [refund:" + reason + "]"
			if len(suffix) > 120 {
				suffix = suffix[:120]
			}
			locked.TradeNo = locked.TradeNo + suffix
		}
		locked.Status = model.OrderStatusRefunded
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		// 回退优惠券使用次数
		if locked.CouponID != "" {
			_ = s.couponRepo.DecrUsedSafeTx(tx, locked.CouponID)
		}
		return nil
	})
}

// AdminCancelOrder 管理员强制取消订单
// 与用户取消不同: 管理员可对任何非 refunded 订单执行取消
// 修复: 取消时回退优惠券使用次数
func (s *OrderService) AdminCancelOrder(orderID, reason string) error {
	o, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return err
	}
	if o.Status == model.OrderStatusRefunded {
		return errors.New("已退款订单不可取消")
	}
	if o.Status == model.OrderStatusCancelled {
		return errors.New("订单已是已取消状态")
	}
	wasPending := o.Status == model.OrderStatusPending
	db := app.Get().DB
	return db.Transaction(func(tx *gorm.DB) error {
		var locked model.Order
		if err := tx.Set("gorm:query_option", "FOR UPDATE").
			Where("id = ?", o.ID).First(&locked).Error; err != nil {
			return err
		}
		if locked.Status == model.OrderStatusRefunded {
			return errors.New("已退款订单不可取消")
		}
		if locked.Status == model.OrderStatusCancelled {
			return errors.New("订单已是已取消状态")
		}
		if reason != "" {
			suffix := " [cancel:" + reason + "]"
			if len(suffix) > 120 {
				suffix = suffix[:120]
			}
			locked.TradeNo = locked.TradeNo + suffix
		}
		// 仅 pending 订单回退优惠券(paid 订单优惠券已消费, 退款流程才退)
		wasPendingNow := locked.Status == model.OrderStatusPending
		locked.Status = model.OrderStatusCancelled
		if err := tx.Save(&locked).Error; err != nil {
			return err
		}
		if wasPending && wasPendingNow && locked.CouponID != "" {
			_ = s.couponRepo.DecrUsedSafeTx(tx, locked.CouponID)
		}
		return nil
	})
}

// setUserPlan 内部开通套餐逻辑:
// - 无论是新购、续费还是升降级，均重置流量为套餐额度(行业主流做法)
// - 续费同套餐: 流量重置为套餐额度 + 延长到期时间(不叠加历史剩余)
// - 升级/降级/新购: 重置流量 + 设置新配额
// - expired_at 在未过期基础上叠加 duration_days; 已过期则从 now 起算
// 修复 F-11: 新增 tx 参数, tx 非空时用 tx.Save 而非 userRepo.Update,
// 保证与订单状态变更在同一事务内, 避免"退款不退套餐"
func (s *OrderService) setUserPlan(tx *gorm.DB, userID string, plan *model.Plan, now time.Time) error {
	u, err := s.userRepo.GetByID(userID)
	if err != nil {
		return err
	}

	planID := plan.ID
	u.PlanID = &planID

	// 统一重置流量为套餐额度(续费不叠加历史剩余)
	u.TrafficUsed = 0
	u.UploadBytes = 0
	u.DownloadBytes = 0
	u.TrafficLimit = plan.TrafficLimit

	// 计算到期时间
	if plan.DurationDays > 0 {
		base := now
		if u.ExpiredAt != nil && u.ExpiredAt.After(now) {
			base = *u.ExpiredAt
		}
		t := base.AddDate(0, 0, plan.DurationDays)
		u.ExpiredAt = &t
	} else {
		u.ExpiredAt = nil
	}

	if u.Status != "disabled" {
		u.Status = "active"
	}
	// 事务内用 tx.Save, 非事务用 userRepo.Update
	if tx != nil {
		return tx.Save(u).Error
	}
	return s.userRepo.Update(u)
}

// ApplyCoupon 校验优惠券并计算折扣金额(不持久化)
func (s *OrderService) ApplyCoupon(orderID, couponCode string) (discount int64, amount int64, err error) {
	o, err := s.orderRepo.GetByID(orderID)
	if err != nil {
		return 0, 0, err
	}
	if o.Status != model.OrderStatusPending {
		return 0, 0, errors.New("订单状态不允许应用优惠券")
	}
	coupon, err := s.couponRepo.GetByCode(couponCode)
	if err != nil {
		return 0, 0, errors.New("优惠券无效")
	}
	if !s.couponRepo.IsValid(coupon, time.Now()) {
		return 0, 0, errors.New("优惠券已失效或已用完")
	}
	discount, err = calcCouponDiscount(coupon, o.AmountCents)
	if err != nil {
		return 0, 0, err
	}
	amount = o.AmountCents - discount
	if amount < 0 {
		amount = 0
	}
	return discount, amount, nil
}

// calcCouponDiscount 计算优惠券折扣金额
func calcCouponDiscount(c *model.Coupon, amount int64) (int64, error) {
	if amount < c.MinAmountCents {
		return 0, fmt.Errorf("订单金额未达最低消费 %d 分", c.MinAmountCents)
	}
	switch c.Type {
	case model.CouponTypePercent:
		if c.Value < 1 || c.Value > 90 {
			return 0, errors.New("优惠券折扣比例无效")
		}
		d := amount * c.Value / 100
		return d, nil
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

// generateOrderNo 生成订单号: NP + yyyyMMddHHmmss + 6位随机数字
func generateOrderNo() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	suffix := ""
	for _, x := range b {
		suffix += string(rune('0' + int(x%10)))
	}
	return "NP" + time.Now().Format("20060102150405") + suffix, nil
}
