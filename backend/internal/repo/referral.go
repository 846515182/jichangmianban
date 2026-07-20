package repo

import (
	"gorm.io/gorm"

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

// GetByInviteeID 查询被邀请人的邀请关系(每人只能被邀请一次)
func (r *ReferralRepo) GetByInviteeID(inviteeID string) (*model.Referral, error) {
	var ref model.Referral
	if err := r.db.Where("invitee_id = ?", inviteeID).First(&ref).Error; err != nil {
		return nil, err
	}
	return &ref, nil
}

// ListByInviterID 分页查询邀请人发出的邀请列表
func (r *ReferralRepo) ListByInviterID(inviterID string, page, size int) ([]model.Referral, int64, error) {
	var list []model.Referral
	var total int64
	q := r.db.Model(&model.Referral{}).Where("inviter_id = ?", inviterID)
	q.Count(&total)
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
func (r *ReferralRepo) CompleteTx(tx *gorm.DB, refID string, orderID string, rewardCents int64) error {
	now := gorm.Expr("NOW()")
	return tx.Model(&model.Referral{}).Where("id = ?", refID).Updates(map[string]interface{}{
		"status":       model.ReferralStatusCompleted,
		"order_id":     orderID,
		"reward_cents": rewardCents,
		"reward_at":    now,
	}).Error
}

// Stats 统计邀请人数据: 邀请总数 / 已完成数 / 累计返利(分)
func (r *ReferralRepo) Stats(inviterID string) (total int64, completed int64, totalReward int64, err error) {
	var count int64
	r.db.Model(&model.Referral{}).Where("inviter_id = ?", inviterID).Count(&count)
	total = count

	var done int64
	r.db.Model(&model.Referral{}).Where("inviter_id = ? AND status = ?", inviterID, model.ReferralStatusCompleted).Count(&done)
	completed = done

	var sum struct {
		Total int64
	}
	r.db.Model(&model.Referral{}).Select("COALESCE(SUM(reward_cents), 0) AS total").
		Where("inviter_id = ? AND status = ?", inviterID, model.ReferralStatusCompleted).Scan(&sum)
	totalReward = sum.Total

	return total, completed, totalReward, nil
}

// ListRewards 分页查询返利记录
func (r *ReferralRepo) ListRewards(userID string, page, size int) ([]model.ReferralReward, int64, error) {
	var list []model.ReferralReward
	var total int64
	q := r.db.Model(&model.ReferralReward{}).Where("user_id = ?", userID)
	q.Count(&total)
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
