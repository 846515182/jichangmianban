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

// ListAll 分页查询全部订单(支持按状态/用户ID过滤)
func (r *OrderRepo) ListAll(page, size int, status, userID string) ([]model.Order, int64, error) {
	var list []model.Order
	var total int64
	q := r.db.Model(&model.Order{}).Where("is_deleted = false")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
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
