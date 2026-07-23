package repo

import (
	"context"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// PlanRepo 套餐仓储
type PlanRepo struct {
	db *gorm.DB
}

// NewPlanRepo 创建套餐仓储
func NewPlanRepo(db *gorm.DB) *PlanRepo {
	return &PlanRepo{db: db}
}

// GetByID 按 ID 查询(过滤软删除)
func (r *PlanRepo) GetByID(id string) (*model.Plan, error) {
	var p model.Plan
	if err := r.db.Where("id = ? AND is_deleted = false", id).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// List 分页查询(支持关键字搜索)
func (r *PlanRepo) List(page, size int, keyword string) ([]model.Plan, int64, error) {
	var list []model.Plan
	var total int64
	q := r.db.Model(&model.Plan{}).Where("is_deleted = false")
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("name LIKE ? OR description LIKE ?", like, like)
	}
	q.Count(&total)
	if err := q.Order("sort_order ASC, created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListEnabled 查询所有可购买的套餐(按 sort_order 排序, 排除试用套餐)
func (r *PlanRepo) ListEnabled() ([]model.Plan, error) {
	var list []model.Plan
	if err := r.db.Where("is_deleted = false AND is_enabled = true AND is_trial = false").
		Order("sort_order ASC, created_at DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// GetTrialPlan 查找试用套餐 (is_trial=true 且启用), 用于注册自动发放
func (r *PlanRepo) GetTrialPlan() (*model.Plan, error) {
	var p model.Plan
	if err := r.db.Where("is_deleted = false AND is_enabled = true AND is_trial = true").
		Order("sort_order ASC, created_at DESC").First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// CountNodesByPlanID 统计绑定该套餐的节点数量
func (r *PlanRepo) CountNodesByPlanID(planID string) (int64, error) {
	var count int64
	if err := r.db.Table("node_plan_bindings").Where("plan_id = ?", planID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// CountActiveUsersByPlanID 统计当前仍引用该套餐(未删除)的用户数
// 用于删除套餐前校验: 仍有用户在用时拒绝删除, 避免后续订单开通时查不到套餐(P1)
func (r *PlanRepo) CountActiveUsersByPlanID(planID string) (int64, error) {
	var count int64
	if err := r.db.Model(&model.User{}).
		Where("plan_id = ? AND is_deleted = false", planID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// CountPendingOrdersByPlanID 统计引用该套餐的待支付订单数(删除套餐前校验, P2)
// 防止删除套餐后, 已下单未支付的用户付款时无法开通套餐
func (r *PlanRepo) CountPendingOrdersByPlanID(planID string) (int64, error) {
	var count int64
	if err := r.db.Model(&model.Order{}).
		Where("plan_id = ? AND is_deleted = false AND status = ?", planID, model.OrderStatusPending).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// nodeIDsByPlanID 查询套餐绑定的所有节点 ID
func (r *PlanRepo) nodeIDsByPlanID(planID string) ([]string, error) {
	return r.NodeIDsByPlanID(planID)
}

// NodeIDsByPlanID 导出版: 查询套餐绑定的所有节点 ID。
// 供 service/handler 跨包获取受影响节点列表, 用于精准清理 Redis 缓存。
func (r *PlanRepo) NodeIDsByPlanID(planID string) ([]string, error) {
	var ids []string
	if err := r.db.Model(&model.NodePlanBinding{}).
		Where("plan_id = ?", planID).
		Pluck("node_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// Create 创建套餐
func (r *PlanRepo) Create(p *model.Plan) error {
	return r.db.Create(p).Error
}

// Update 更新套餐
// 兜底: 套餐变更(价格/流量/状态)会影响订阅展示与节点用户列表,
// 更新后清理该套餐绑定节点的 usershash + configver 缓存, 让 agent 下次心跳重拉。
func (r *PlanRepo) Update(p *model.Plan) error {
	if err := r.db.Save(p).Error; err != nil {
		return err
	}
	if ids, err := r.nodeIDsByPlanID(p.ID); err == nil && len(ids) > 0 {
		clearNodeUsersHashCache(context.Background(), ids...)
	}
	return nil
}

// SoftDelete 软删除套餐
// [P1-删除审计] 事务内同时物理删除 node_plan_bindings 绑定关系,
// 旧版只软删 plan, 绑定残留导致 CountNodesByPlanID 统计虚高,
// 且 node_plan_bindings.plan_id 外键 CASCADE 仅物理删 plan 时生效, 软删不触发
// 兜底: 删除前记录绑定节点, 事务成功后清理这些节点的 usershash + configver 缓存。
func (r *PlanRepo) SoftDelete(id string) error {
	nodeIDs, _ := r.nodeIDsByPlanID(id)
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 1. 软删套餐
		if err := tx.Model(&model.Plan{}).Where("id = ? AND is_deleted = false", id).
			Update("is_deleted", true).Error; err != nil {
			return err
		}
		// 2. 物理删除节点绑定关系(关系表无需审计, 直接删)
		//    避免 DeletePlan 后 CountNodesByPlanID 统计虚高
		if err := tx.Where("plan_id = ?", id).Delete(&model.NodePlanBinding{}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(nodeIDs) > 0 {
		clearNodeUsersHashCache(context.Background(), nodeIDs...)
	}
	return nil
}

// SyncUsersByPlanID 在事务内按 plan_id 同步 users 表的 traffic_limit
// 注意:
//   - 不修改 expired_at(避免缩短已付费用户有效期)
//   - 不修改 traffic_used(已用流量不清零)
//   - 节点可见性由 node_plan_bindings 表决定
//   - 使用 UpdateColumns 避免自动更新 updated_at(否则会触发节点配置版本号变更误判)
func (r *PlanRepo) SyncUsersByPlanID(tx *gorm.DB, planID string, trafficLimit int64) error {
	return tx.Model(&model.User{}).
		Where("plan_id = ? AND is_deleted = false", planID).
		UpdateColumn("traffic_limit", trafficLimit).Error
}

// DB 返回底层 *gorm.DB(事务用)
func (r *PlanRepo) DB() *gorm.DB {
	return r.db
}
