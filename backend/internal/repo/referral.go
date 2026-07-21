package repo

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"nexus-panel/internal/model"
)

// ReferralRepo 邀请返利仓储
type ReferralRepo struct {
	db *gorm.DB
}

// NewReferralRepo 创建邀请返利仓储
func NewReferralRepo(db *gorm.DB) *ReferralRepo {
	return &ReferralRepo{db: db}
}

// DB 返回底层 *gorm.DB, 供 service 层在 repo 之上做事务包裹(P1-BindInviter 用)
// 优先使用此方法而非 app.Get().DB, 以保证单元测试用 SQLite 内存库时也能正常工作
func (r *ReferralRepo) DB() *gorm.DB {
	return r.db
}

// GetByInviteeID 查询被邀请人的邀请关系(每人只能被邀请一次)
func (r *ReferralRepo) GetByInviteeID(inviteeID string) (*model.Referral, error) {
	var ref model.Referral
	if err := r.db.Where("invitee_id = ?", inviteeID).First(&ref).Error; err != nil {
		return nil, err
	}
	return &ref, nil
}

// GetByInviteeIDTx 在事务内查询被邀请人的邀请关系(P1-BindInviter 事务化用)
func (r *ReferralRepo) GetByInviteeIDTx(tx *gorm.DB, inviteeID string) (*model.Referral, error) {
	var ref model.Referral
	if err := tx.Where("invitee_id = ?", inviteeID).First(&ref).Error; err != nil {
		return nil, err
	}
	return &ref, nil
}

// GetByInviteeIDForUpdateTx 在事务内查询并加 FOR UPDATE 锁(P0-F2 防并发重复发放)
func (r *ReferralRepo) GetByInviteeIDForUpdateTx(tx *gorm.DB, inviteeID string) (*model.Referral, error) {
	var ref model.Referral
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("invitee_id = ?", inviteeID).First(&ref).Error; err != nil {
		return nil, err
	}
	return &ref, nil
}

// ListByInviterID 分页查询邀请人发出的邀请列表
func (r *ReferralRepo) ListByInviterID(inviterID string, page, size int) ([]model.Referral, int64, error) {
	var list []model.Referral
	var total int64
	q := r.db.Model(&model.Referral{}).Where("inviter_id = ?", inviterID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Create 创建邀请关系
func (r *ReferralRepo) Create(ref *model.Referral) error {
	return r.db.Create(ref).Error
}

// CreateTx 在事务内创建邀请关系
func (r *ReferralRepo) CreateTx(tx *gorm.DB, ref *model.Referral) error {
	return tx.Create(ref).Error
}

// Complete 标记邀请关系为已完成(返利已发放)
func (r *ReferralRepo) Complete(refID string, orderID string, rewardCents int64) error {
	now := gorm.Expr("NOW()")
	return r.db.Model(&model.Referral{}).Where("id = ?", refID).Updates(map[string]interface{}{
		"status":       model.ReferralStatusCompleted,
		"order_id":     orderID,
		"reward_cents": rewardCents,
		"reward_at":    now,
	}).Error
}

// CompleteTx 在事务内标记完成
// 修复 P0-F2: UPDATE 条件加 AND status = 'pending', 通过 RowsAffected 判定是否本请求发放(幂等)
// 防止并发场景下重复发放返利(两个 PaySuccess 同时进入 HandleOrderPaid)
func (r *ReferralRepo) CompleteTx(tx *gorm.DB, refID string, orderID string, rewardCents int64) error {
	now := gorm.Expr("NOW()")
	result := tx.Model(&model.Referral{}).
		Where("id = ? AND status = ?", refID, model.ReferralStatusPending).
		Updates(map[string]interface{}{
			"status":       model.ReferralStatusCompleted,
			"order_id":     orderID,
			"reward_cents": rewardCents,
			"reward_at":    now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// 已被其他请求处理, 幂等返回 nil
		return nil
	}
	return nil
}

// Stats 统计邀请人数据: 邀请总数 / 已完成数 / 累计返利(分)
// 修复 P1-repo-referral: 旧版三次独立查询且吞掉 error, 现合并为单 SQL + 检查 error
func (r *ReferralRepo) Stats(inviterID string) (total int64, completed int64, totalReward int64, err error) {
	type statsResult struct {
		Total int64 `gorm:"column:total"`
		Done  int64 `gorm:"column:done"`
		Sum   int64 `gorm:"column:reward_sum"`
	}
	var sr statsResult
	err = r.db.Model(&model.Referral{}).
		Select("COUNT(*) as total, COUNT(*) FILTER (WHERE status = 'completed') as done, COALESCE(SUM(reward_cents) FILTER (WHERE status = 'completed'), 0) as reward_sum").
		Where("inviter_id = ?", inviterID).
		Scan(&sr).Error
	if err != nil {
		return 0, 0, 0, err
	}
	return sr.Total, sr.Done, sr.Sum, nil
}

// ListRewards 分页查询返利记录
func (r *ReferralRepo) ListRewards(userID string, page, size int) ([]model.ReferralReward, int64, error) {
	var list []model.ReferralReward
	var total int64
	q := r.db.Model(&model.ReferralReward{}).Where("user_id = ?", userID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// CreateReward 创建返利记录
func (r *ReferralRepo) CreateReward(rew *model.ReferralReward) error {
	return r.db.Create(rew).Error
}

// CreateRewardTx 在事务内创建返利记录
func (r *ReferralRepo) CreateRewardTx(tx *gorm.DB, rew *model.ReferralReward) error {
	return tx.Create(rew).Error
}

// ListAll 分页查询全部邀请关系(管理端 P1-admin_referral 用)
func (r *ReferralRepo) ListAll(page, size int) ([]model.Referral, int64, error) {
	var list []model.Referral
	var total int64
	q := r.db.Model(&model.Referral{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListAllRewards 分页查询全部返利记录(管理端对账 P1-admin_referral 用)
func (r *ReferralRepo) ListAllRewards(page, size int) ([]model.ReferralReward, int64, error) {
	var list []model.ReferralReward
	var total int64
	q := r.db.Model(&model.ReferralReward{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
