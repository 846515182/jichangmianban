package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// ReferralService 邀请返利服务
type ReferralService struct {
	referralRepo *repo.ReferralRepo
	userRepo     *repo.UserRepo
	settingRepo  *repo.SettingRepo
}

// NewReferralService 创建邀请返利服务
func NewReferralService(r *repo.ReferralRepo, u *repo.UserRepo, s *repo.SettingRepo) *ReferralService {
	return &ReferralService{referralRepo: r, userRepo: u, settingRepo: s}
}

// 邀请返利配置 key
const (
	SettingKeyReferralEnabled   = "referral_enabled"
	SettingKeyReferralRate      = "referral_rate"
	SettingKeyReferralMaxReward = "referral_max_reward_cents"
)

// 默认配置
const (
	defaultReferralRate      = 0.10 // 默认返利比例 10%
	defaultReferralMaxReward = 0    // 0 = 不设上限
)

// GetReferralConfig 获取返利配置
func (s *ReferralService) GetReferralConfig() (enabled bool, rate float64, maxReward int64) {
	enabled = false
	if err := s.settingRepo.Get(SettingKeyReferralEnabled, &enabled); err != nil {
		enabled = false
	}
	rate = defaultReferralRate
	if err := s.settingRepo.Get(SettingKeyReferralRate, &rate); err != nil {
		rate = defaultReferralRate
	}
	maxReward = defaultReferralMaxReward
	if err := s.settingRepo.Get(SettingKeyReferralMaxReward, &maxReward); err != nil {
		maxReward = defaultReferralMaxReward
	}
	return enabled, rate, maxReward
}

// SetReferralConfig 设置返利配置(管理端)
func (s *ReferralService) SetReferralConfig(enabled bool, rate float64, maxReward int64) error {
	if rate < 0 || rate > 1 {
		return errors.New("返利比例必须在 0-1 之间")
	}
	if err := s.settingRepo.Set(SettingKeyReferralEnabled, enabled); err != nil {
		return err
	}
	if err := s.settingRepo.Set(SettingKeyReferralRate, rate); err != nil {
		return err
	}
	if err := s.settingRepo.Set(SettingKeyReferralMaxReward, maxReward); err != nil {
		return err
	}
	return nil
}

// GetOrCreateInviteCode 获取或生成邀请码
// 若用户已有邀请码则直接返回, 否则生成一个新的
func (s *ReferralService) GetOrCreateInviteCode(userID string) (string, error) {
	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return "", err
	}
	if user.InviteCode != "" {
		return user.InviteCode, nil
	}
	// 生成并写入邀请码(带冲突重试)
	for i := 0; i < 5; i++ {
		code := generateInviteCode(8)
		if err := s.userRepo.UpdateInviteCode(userID, code); err != nil {
			// 唯一索引冲突则重试
			if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
				continue
			}
			return "", err
		}
		return code, nil
	}
	return "", errors.New("生成邀请码失败, 请稍后重试")
}

// generateInviteCode 生成指定长度的邀请码(大写字母+数字, 去掉易混淆字符)
func generateInviteCode(length int) string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	result := make([]byte, length)
	for i := range result {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		result[i] = chars[n.Int64()]
	}
	return string(result)
}

// BindInviter 绑定邀请关系(注册时调用)
// 注意: 每人只能被邀请一次, 重复调用返回错误
// 修复 P1-BindInviter: 三步(查重/创建)包入事务, 事务内查重避免并发重复绑定
func (s *ReferralService) BindInviter(inviteeID, inviteCode string) error {
	enabled, _, _ := s.GetReferralConfig()
	if !enabled {
		return errors.New("邀请返利功能未开启")
	}
	if inviteCode == "" {
		return nil // 没有邀请码, 不绑定
	}
	// 查找邀请人
	inviter, err := s.userRepo.GetByInviteCode(strings.ToUpper(strings.TrimSpace(inviteCode)))
	if err != nil {
		return errors.New("邀请码无效")
	}
	if inviter.ID == inviteeID {
		return errors.New("不能邀请自己")
	}
	ref := &model.Referral{
		InviterID: inviter.ID,
		InviteeID: inviteeID,
		Status:    model.ReferralStatusPending,
	}
	// 事务内查重 + 创建, 避免并发场景下两个请求同时通过查重后双写
	// 优先用 repo 持有的 DB(单元测试 SQLite 内存库场景兼容), 回退到 app.Get().DB
	db := s.referralRepo.DB()
	if db == nil {
		db = s.GetDB()
	}
	if db == nil {
		return errors.New("数据库不可用")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		existing, err := s.referralRepo.GetByInviteeIDTx(tx, inviteeID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if existing != nil {
			return errors.New("只能被邀请一次")
		}
		return s.referralRepo.CreateTx(tx, ref)
	})
}

// ListInvitations 分页查询我发出的邀请
func (s *ReferralService) ListInvitations(inviterID string, page, size int) ([]model.Referral, int64, error) {
	return s.referralRepo.ListByInviterID(inviterID, page, size)
}

// GetStats 获取邀请统计
func (s *ReferralService) GetStats(inviterID string) (total int64, completed int64, totalReward int64, err error) {
	return s.referralRepo.Stats(inviterID)
}

// ListRewards 分页查询返利记录
func (s *ReferralService) ListRewards(userID string, page, size int) ([]model.ReferralReward, int64, error) {
	return s.referralRepo.ListRewards(userID, page, size)
}

// ListAllInvitations 分页查询全部邀请关系(管理端 P1-admin_referral 用)
func (s *ReferralService) ListAllInvitations(page, size int) ([]model.Referral, int64, error) {
	return s.referralRepo.ListAll(page, size)
}

// ListAllRewards 分页查询全部返利记录(管理端对账 P1-admin_referral 用)
func (s *ReferralService) ListAllRewards(page, size int) ([]model.ReferralReward, int64, error) {
	return s.referralRepo.ListAllRewards(page, size)
}

// HandleOrderPaid 订单支付成功后处理返利
// 在 PaySuccess 事务内调用, 保证原子性
// 只对用户的首笔支付订单发放返利
// 修复 P0-F2: 在事务内对 referrals 行加 FOR UPDATE 锁, 加锁后重新检查 status,
// 防止并发场景下两个 PaySuccess 同时进入并重复发放返利。
func (s *ReferralService) HandleOrderPaid(tx *gorm.DB, order *model.Order) error {
	if order == nil || order.ID == "" {
		return nil
	}
	enabled, rate, maxReward := s.GetReferralConfig()
	if !enabled {
		return nil
	}
	// P0-F2: 事务内锁定 referral 行, 防止并发重复发放
	ref, err := s.referralRepo.GetByInviteeIDForUpdateTx(tx, order.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // 没有邀请关系, 跳过
		}
		return err
	}
	// 加锁后重新检查 status
	if ref.Status != model.ReferralStatusPending {
		return nil // 已处理, 幂等
	}
	// 计算返利金额
	rewardCents := int64(float64(order.AmountCents) * rate)
	if maxReward > 0 && rewardCents > maxReward {
		rewardCents = maxReward
	}
	if rewardCents <= 0 {
		return nil // 0元不发放
	}
	// 在事务内完成: 标记邀请完成 + 创建返利记录
	// P0-F2: CompleteTx 内部 UPDATE 加 AND status='pending' 兜底, 双保险
	now := time.Now()
	if err := s.referralRepo.CompleteTx(tx, ref.ID, order.ID, rewardCents); err != nil {
		return fmt.Errorf("标记邀请完成失败: %w", err)
	}
	rew := &model.ReferralReward{
		UserID:           ref.InviterID,
		OrderID:          order.ID,
		InviteeID:        order.UserID,
		AmountCents:      rewardCents,
		OrderAmountCents: order.AmountCents,
		RewardRate:       rate,
		Description:      fmt.Sprintf("邀请好友首单返利(%.0f%%)", rate*100),
		CreatedAt:        now,
	}
	if err := s.referralRepo.CreateRewardTx(tx, rew); err != nil {
		return fmt.Errorf("创建返利记录失败: %w", err)
	}
	return nil
}

// GetInviterIDByUser 获取用户的邀请人ID(用于订单记录 inviter_id)
func (s *ReferralService) GetInviterIDByUser(userID string) *string {
	ref, err := s.referralRepo.GetByInviteeID(userID)
	if err != nil || ref == nil {
		return nil
	}
	return &ref.InviterID
}

// GetDB 获取底层 DB(用于外部事务)
func (s *ReferralService) GetDB() *gorm.DB {
	return app.Get().DB
}
