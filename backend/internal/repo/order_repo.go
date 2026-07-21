package repo

import (
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// OrderRepo 订单仓储
type OrderRepo struct {
	db *gorm.DB
}

// NewOrderRepo 创建订单仓储
func NewOrderRepo(db *gorm.DB) *OrderRepo {
	return &OrderRepo{db: db}
}

// GetByID 按 ID 查询(过滤软删除)
func (r *OrderRepo) GetByID(id string) (*model.Order, error) {
	var o model.Order
	if err := r.db.Where("id = ? AND is_deleted = false", id).First(&o).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

// GetByOrderNo 按订单号查询
func (r *OrderRepo) GetByOrderNo(orderNo string) (*model.Order, error) {
	var o model.Order
	if err := r.db.Where("order_no = ? AND is_deleted = false", orderNo).First(&o).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

// ListByUserID 分页查询用户订单
func (r *OrderRepo) ListByUserID(userID string, page, size int) ([]model.Order, int64, error) {
	var list []model.Order
	var total int64
	q := r.db.Model(&model.Order{}).Where("user_id = ? AND is_deleted = false", userID)
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAll 分页查询全部订单(支持按状态/用户ID/keyword过滤)
// 修复 P1: 旧版不支持 keyword, 前端只能在前端过滤当前 20 条, 第 21 条之后永远搜不到。
// 现支持 keyword 模糊匹配 order_no 或 username(LEFT JOIN users)。
// 修复 P0: 旧版直接返回 []model.Order 无 username, 前端表格"用户"列永远空白。
// 现改用 OrderListItem DTO, LEFT JOIN users 取 username 一起返回。
// 修复 P1-repo-软删除过滤: JOIN users 加 AND u.is_deleted = false, 避免查出软删用户的订单
// 注意: gorm 不便直接返回 DTO + Count, 这里用 raw SQL 兼容 PostgreSQL。
func (r *OrderRepo) ListAll(page, size int, status, userID, keyword string) ([]OrderListItem, int64, error) {
	q := r.db.Table("orders AS o").
		Select("o.id, o.order_no, o.user_id, o.plan_id, o.plan_name, o.amount_cents, o.status, "+
			"o.payment_method, o.trade_no, o.coupon_id, o.coupon_code, o.paid_at, o.expired_at, "+
			"o.is_deleted, o.created_at, o.updated_at, u.username AS user_username").
		Joins("LEFT JOIN users AS u ON u.id = o.user_id AND u.is_deleted = false").
		Where("o.is_deleted = false")
	if status != "" {
		q = q.Where("o.status = ?", status)
	}
	if userID != "" {
		q = q.Where("o.user_id = ?", userID)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		// 同时匹配订单号和用户名, 任一命中即返回
		q = q.Where("o.order_no ILIKE ? OR u.username ILIKE ?", like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []OrderListItem
	if err := q.Order("o.created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// OrderListItem 订单列表项 DTO(含 user_username, 用于管理后台展示)
// 修复 P0: model.Order 无 Username 字段, 直接返回会让前端表格"用户"列永远空白。
type OrderListItem struct {
	ID            string     `gorm:"column:id" json:"id"`
	OrderNo       string     `gorm:"column:order_no" json:"order_no"`
	UserID        string     `gorm:"column:user_id" json:"user_id"`
	UserUsername  string     `gorm:"column:user_username" json:"user_username"`
	PlanID        string     `gorm:"column:plan_id" json:"plan_id"`
	PlanName      string     `gorm:"column:plan_name" json:"plan_name"`
	AmountCents   int64      `gorm:"column:amount_cents" json:"amount_cents"`
	Status        string     `gorm:"column:status" json:"status"`
	PaymentMethod string     `gorm:"column:payment_method" json:"payment_method"`
	TradeNo       string     `gorm:"column:trade_no" json:"trade_no"`
	CouponID      *string    `gorm:"column:coupon_id" json:"coupon_id,omitempty"`
	CouponCode    string     `gorm:"column:coupon_code" json:"coupon_code,omitempty"`
	PaidAt        *time.Time `gorm:"column:paid_at" json:"paid_at,omitempty"`
	ExpiredAt     time.Time  `gorm:"column:expired_at" json:"expired_at"`
	IsDeleted     bool       `gorm:"column:is_deleted" json:"-"`
	CreatedAt     time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

// Create 创建订单
func (r *OrderRepo) Create(o *model.Order) error {
	return r.db.Create(o).Error
}

// Update 更新订单
func (r *OrderRepo) Update(o *model.Order) error {
	return r.db.Save(o).Error
}

// UpdateStatus 更新订单状态
func (r *OrderRepo) UpdateStatus(id, status string) error {
	return r.db.Model(&model.Order{}).Where("id = ? AND is_deleted = false", id).
		Update("status", status).Error
}

// SoftDelete 软删除
func (r *OrderRepo) SoftDelete(id string) error {
	return r.db.Model(&model.Order{}).Where("id = ? AND is_deleted = false", id).
		Update("is_deleted", true).Error
}

// DB 返回底层 *gorm.DB(事务用)
func (r *OrderRepo) DB() *gorm.DB {
	return r.db
}

// ListExpired 列出已过期但仍为 pending 的订单
func (r *OrderRepo) ListExpired(now time.Time) ([]model.Order, error) {
	var list []model.Order
	if err := r.db.Where("is_deleted = false AND status = ? AND expired_at < ?", model.OrderStatusPending, now).
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListPendingSince 列出 created_at >= since 且仍为 pending 的订单(掉单对账用)
// 修复 PAY-RECON-01 (P0): 回调因网络/服务重启丢失时, 通过主动查询 EPay 订单状态
// 兜底完成支付, 防止"用户已付款但订单永远 pending"。仅扫近 N 分钟订单, 避免全表扫描。
func (r *OrderRepo) ListPendingSince(since time.Time) ([]model.Order, error) {
	var list []model.Order
	if err := r.db.Where("is_deleted = false AND status = ? AND created_at >= ?",
		model.OrderStatusPending, since).
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// CountActiveByUser 统计用户的未完结(pending/paid)订单数
// 用于删除用户前校验: 存在未完结订单时拒绝删除, 避免产生孤儿订单(P1)
func (r *OrderRepo) CountActiveByUser(userID string) (int64, error) {
	var n int64
	if err := r.db.Model(&model.Order{}).
		Where("user_id = ? AND is_deleted = false AND status IN ?",
			userID, []string{model.OrderStatusPending, model.OrderStatusPaid}).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// CountPaidByUserExcluding 统计用户除指定订单外的已支付(未退款/未取消)订单数
// 用于退款时判断是否还有其它有效订阅, 避免退款一笔误杀用户全部访问权(P0 蝴蝶效应)
func (r *OrderRepo) CountPaidByUserExcluding(userID, excludeOrderID string) (int64, error) {
	var n int64
	if err := r.db.Model(&model.Order{}).
		Where("user_id = ? AND id != ? AND is_deleted = false AND status = ?",
			userID, excludeOrderID, model.OrderStatusPaid).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// CountActiveByCoupon 统计引用该优惠券的未完结订单数(pending/paid)
// 用于删除优惠券前校验: 软删除后这些订单退款/取消时 DecrUsedSafeTx 会失败,
// 造成优惠券 used_count 永久虚高, 影响后续用户使用
func (r *OrderRepo) CountActiveByCoupon(couponID string) (int64, error) {
	var n int64
	if err := r.db.Model(&model.Order{}).
		Where("coupon_id = ? AND is_deleted = false AND status IN ?",
			couponID, []string{model.OrderStatusPending, model.OrderStatusPaid}).
		Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// ListExpiredSince 列出已过期(status=expired)且 expired_at >= since 的订单(掉单对账用)
// 用于对账 cron 扫描"已过期但用户可能已付款"的订单, 兜底开通, 避免资金损失
func (r *OrderRepo) ListExpiredSince(since time.Time) ([]model.Order, error) {
	var list []model.Order
	if err := r.db.Where("is_deleted = false AND status = ? AND expired_at >= ?",
		model.OrderStatusExpired, since).
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// OrderStatsResult 订单全局统计结果
// 用于管理后台订单页头部统计展示, 避免前端只基于当前页数据计算导致的偏差
type OrderStatsResult struct {
	PendingCount     int64 `json:"pending_count"`
	PaidCount        int64 `json:"paid_count"`
	CancelledCount   int64 `json:"cancelled_count"`
	ExpiredCount     int64 `json:"expired_count"`
	RefundedCount    int64 `json:"refunded_count"`
	TotalIncomeCents int64 `json:"total_income_cents"` // 已支付订单总金额(分)
}

// Stats 全局订单统计: 按状态分组计数 + 已支付订单总金额
// 一次 GROUP BY 拿各状态计数, 一次 SUM 拿总金额, 共 2 次查询(均已走 status 索引)
func (r *OrderRepo) Stats() (*OrderStatsResult, error) {
	var stats OrderStatsResult
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	if err := r.db.Model(&model.Order{}).
		Select("status, COUNT(*) as count").
		Where("is_deleted = false").
		Group("status").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		switch row.Status {
		case model.OrderStatusPending:
			stats.PendingCount = row.Count
		case model.OrderStatusPaid:
			stats.PaidCount = row.Count
		case model.OrderStatusCancelled:
			stats.CancelledCount = row.Count
		case model.OrderStatusExpired:
			stats.ExpiredCount = row.Count
		case model.OrderStatusRefunded:
			stats.RefundedCount = row.Count
		}
	}
	// 已支付订单总金额(分), COALESCE 防止无数据时返回 NULL
	if err := r.db.Model(&model.Order{}).
		Where("is_deleted = false AND status = ?", model.OrderStatusPaid).
		Select("COALESCE(SUM(amount_cents), 0)").
		Scan(&stats.TotalIncomeCents).Error; err != nil {
		return nil, err
	}
	return &stats, nil
}
