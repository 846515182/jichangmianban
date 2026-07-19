package service

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// PlanService 套餐服务
type PlanService struct {
	repo *repo.PlanRepo
}

// NewPlanService 创建套餐服务
func NewPlanService(r *repo.PlanRepo) *PlanService {
	return &PlanService{repo: r}
}

// CreatePlanInput 创建套餐入参
type CreatePlanInput struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	Features           string `json:"features"`
	Limitations        string `json:"limitations"`
	TrafficLimit       int64  `json:"traffic_limit"`
	DurationDays       int    `json:"duration_days"`
	PriceCents         int64  `json:"price_cents"`
	OriginalPriceCents int64  `json:"original_price_cents"`
	DeviceLimit        int    `json:"device_limit"`
	SortOrder          int    `json:"sort_order"`
	IsEnabled          bool   `json:"is_enabled"`
}

// UpdatePlanInput 更新套餐入参(指针字段可部分更新)
type UpdatePlanInput struct {
	Name               *string `json:"name"`
	Description        *string `json:"description"`
	Features           *string `json:"features"`
	Limitations        *string `json:"limitations"`
	TrafficLimit       *int64  `json:"traffic_limit"`
	DurationDays       *int    `json:"duration_days"`
	PriceCents         *int64  `json:"price_cents"`
	OriginalPriceCents *int64  `json:"original_price_cents"`
	DeviceLimit        *int    `json:"device_limit"`
	SortOrder          *int    `json:"sort_order"`
	IsEnabled          *bool   `json:"is_enabled"`
}

// CreatePlan 创建套餐
func (s *PlanService) CreatePlan(in *CreatePlanInput) (*model.Plan, error) {
	if in.Name == "" {
		return nil, errors.New("套餐名称不能为空")
	}
	if in.PriceCents < 0 {
		return nil, errors.New("价格不能为负")
	}
	p := &model.Plan{
		Name:               in.Name,
		Description:        in.Description,
		Features:           in.Features,
		Limitations:        in.Limitations,
		TrafficLimit:       in.TrafficLimit,
		DurationDays:       in.DurationDays,
		PriceCents:         in.PriceCents,
		OriginalPriceCents: in.OriginalPriceCents,
		DeviceLimit:        in.DeviceLimit,
		SortOrder:          in.SortOrder,
		IsEnabled:          in.IsEnabled,
	}
	if err := s.repo.Create(p); err != nil {
		return nil, err
	}
	return p, nil
}

// UpdatePlan 更新套餐，并在事务内同步已购该套餐的用户的 traffic_limit
// 注意:
//   - 不修改用户的 expired_at(避免缩短已付费用户有效期)
//   - 不修改 traffic_used(不清零已用流量)
//   - 节点可见性由 node_plan_bindings 表决定
func (s *PlanService) UpdatePlan(id string, in *UpdatePlanInput) (*model.Plan, error) {
	p, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		p.Name = *in.Name
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	if in.Features != nil {
		p.Features = *in.Features
	}
	if in.Limitations != nil {
		p.Limitations = *in.Limitations
	}
	if in.TrafficLimit != nil {
		p.TrafficLimit = *in.TrafficLimit
	}
	if in.DurationDays != nil {
		p.DurationDays = *in.DurationDays
	}
	if in.PriceCents != nil {
		p.PriceCents = *in.PriceCents
	}
	if in.OriginalPriceCents != nil {
		p.OriginalPriceCents = *in.OriginalPriceCents
	}
	if in.DeviceLimit != nil {
		p.DeviceLimit = *in.DeviceLimit
	}
	if in.SortOrder != nil {
		p.SortOrder = *in.SortOrder
	}
	if in.IsEnabled != nil {
		p.IsEnabled = *in.IsEnabled
	}

	// 事务: 更新套餐 + 同步已购该套餐的用户(仅同步 traffic_limit)
	err = s.repo.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(p).Error; err != nil {
			return err
		}
		// 同步 users 表的 traffic_limit(不修改 expired_at/traffic_used)
		if err := s.repo.SyncUsersByPlanID(tx, p.ID, p.TrafficLimit); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// DeletePlan 软删除套餐
// 安全修复(P1): 仍有用户引用该套餐时拒绝删除, 避免后续订单开通时查不到套餐
func (s *PlanService) DeletePlan(id string) error {
	if _, err := s.repo.GetByID(id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return err
	}
	if count, err := s.repo.CountActiveUsersByPlanID(id); err == nil && count > 0 {
		return fmt.Errorf("该套餐仍有 %d 个用户在用，请先迁移用户或禁用套餐而非删除", count)
	}
	if pending, err := s.repo.CountPendingOrdersByPlanID(id); err == nil && pending > 0 {
		return fmt.Errorf("该套餐仍有 %d 笔待支付订单，请先处理订单后再删除", pending)
	}
	return s.repo.SoftDelete(id)
}

// ListPlans 管理端列表(含禁用)
func (s *PlanService) ListPlans(page, size int, keyword string) ([]model.Plan, int64, error) {
	return s.repo.List(page, size, keyword)
}

// GetPlan 获取套餐详情
func (s *PlanService) GetPlan(id string) (*model.Plan, error) {
	return s.repo.GetByID(id)
}

// ListEnabledPlans 用户端列表(只返回启用)
func (s *PlanService) ListEnabledPlans() ([]model.Plan, error) {
	return s.repo.ListEnabled()
}

// CountNodesByPlanID 统计绑定该套餐的节点数量
func (s *PlanService) CountNodesByPlanID(planID string) (int64, error) {
	return s.repo.CountNodesByPlanID(planID)
}

// CalcExpiredAt 根据套餐计算过期时间(返回 nil 表示不限)
func CalcExpiredAt(p *model.Plan, base time.Time) *time.Time {
	if p.DurationDays <= 0 {
		return nil
	}
	t := base.AddDate(0, 0, p.DurationDays)
	return &t
}
