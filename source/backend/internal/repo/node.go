package repo

import (
	"database/sql"
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// NodeRepo 节点仓储
type NodeRepo struct {
	db *gorm.DB
}

// NewNodeRepo 创建节点仓储
func NewNodeRepo(db *gorm.DB) *NodeRepo {
	return &NodeRepo{db: db}
}

// GetByID 按 ID 查询(过滤软删除)
func (r *NodeRepo) GetByID(id string) (*model.Node, error) {
	var n model.Node
	if err := r.db.Where("id = ? AND is_deleted = false", id).First(&n).Error; err != nil {
		return nil, err
	}
	return &n, nil
}

// GetByToken 按节点 token 查询(过滤软删除)
func (r *NodeRepo) GetByToken(token string) (*model.Node, error) {
	var n model.Node
	if err := r.db.Where("node_token = ? AND is_deleted = false", token).First(&n).Error; err != nil {
		return nil, err
	}
	return &n, nil
}

// List 分页查询
func (r *NodeRepo) List(page, size int, keyword string) ([]model.Node, int64, error) {
	var list []model.Node
	var total int64
	q := r.db.Model(&model.Node{}).Where("is_deleted = false")
	if keyword != "" {
		like := "%" + keyword + "%"
		q = q.Where("name LIKE ? OR server_address LIKE ?", like, like)
	}
	q.Count(&total)
	if err := q.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListEnabled 查询所有启用的节点(订阅生成用)
func (r *NodeRepo) ListEnabled() ([]model.Node, error) {
	var list []model.Node
	if err := r.db.Where("is_deleted = false AND is_enabled = true").Order("created_at ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListByUser 查询用户可访问的启用节点
// 规则:
//   1. node_plan_bindings 命中: 用户 plan_id 在节点绑定的套餐列表中
//   2. user_nodes 显式授权: 个别精细授权(指定节点无视套餐强制可见)
func (r *NodeRepo) ListByUser(userID string) ([]model.Node, error) {
	var list []model.Node
	err := r.db.Raw(`
		SELECT n.* FROM nodes n
		JOIN users u ON u.id = @uid
		WHERE n.is_deleted = false AND n.is_enabled = true
		  AND (
		    -- 命中套餐绑定
		    (u.plan_id IS NOT NULL AND EXISTS (
		      SELECT 1 FROM node_plan_bindings b WHERE b.node_id = n.id AND b.plan_id = u.plan_id
		    ))
		  )
		UNION
		SELECT n.* FROM nodes n
		WHERE n.is_deleted = false AND n.is_enabled = true
		  AND n.id IN (
		    SELECT un.node_id FROM user_nodes un
		    WHERE un.user_id = @uid AND un.is_deleted = false
		  )
		ORDER BY created_at ASC
	`, sql.Named("uid", userID)).Scan(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// GetPlanIDsByNode 查询节点绑定的套餐 ID 列表
func (r *NodeRepo) GetPlanIDsByNode(nodeID string) ([]string, error) {
	var ids []string
	err := r.db.Model(&model.NodePlanBinding{}).
		Where("node_id = ?", nodeID).
		Pluck("plan_id", &ids).Error
	return ids, err
}

// CreatePlanBindings 为节点创建套餐绑定(批量插入)
func (r *NodeRepo) CreatePlanBindings(nodeID string, planIDs []string) error {
	if len(planIDs) == 0 {
		return nil
	}
	bindings := make([]model.NodePlanBinding, 0, len(planIDs))
	for _, pid := range planIDs {
		bindings = append(bindings, model.NodePlanBinding{NodeID: nodeID, PlanID: pid})
	}
	return r.db.Create(&bindings).Error
}

// ReplacePlanBindings 在事务内替换节点的套餐绑定(先删后增)
func (r *NodeRepo) ReplacePlanBindings(tx *gorm.DB, nodeID string, planIDs []string) error {
	if err := tx.Where("node_id = ?", nodeID).Delete(&model.NodePlanBinding{}).Error; err != nil {
		return err
	}
	if len(planIDs) == 0 {
		return nil
	}
	bindings := make([]model.NodePlanBinding, 0, len(planIDs))
	for _, pid := range planIDs {
		bindings = append(bindings, model.NodePlanBinding{NodeID: nodeID, PlanID: pid})
	}
	return tx.Create(&bindings).Error
}

// DeletePlanBindingsByNode 软删除节点时清理绑定
func (r *NodeRepo) DeletePlanBindingsByNode(nodeID string) error {
	return r.db.Where("node_id = ?", nodeID).Delete(&model.NodePlanBinding{}).Error
}

// Create 创建节点
func (r *NodeRepo) Create(n *model.Node) error {
	return r.db.Create(n).Error
}

// Update 更新节点
func (r *NodeRepo) Update(n *model.Node) error {
	return r.db.Save(n).Error
}

// SoftDelete 软删除
func (r *NodeRepo) SoftDelete(id string) error {
	return r.db.Model(&model.Node{}).Where("id = ? AND is_deleted = false", id).
		Update("is_deleted", true).Error
}


// MarkStaleNodesOffline marks nodes with last_seen_at older than threshold as offline
// 修复 BIZ-HEARTBEAT-01: gRPC 心跳超时自动降级
func (r *NodeRepo) MarkStaleNodesOffline(threshold time.Time) (int64, error) {
	res := r.db.Model(&model.Node{}).
		Where("is_deleted = false AND online = true AND (last_seen_at IS NULL OR last_seen_at < ?)", threshold).
		Updates(map[string]interface{}{
			"online":     false,
			"updated_at": time.Now(),
		})
	return res.RowsAffected, res.Error
}
// MarkOffline 清零节点 online 状态(不过滤 is_deleted，用于软删除后清理 stale 在线标记)
// 修复: 删除节点后 online 字段残留 true，导致后台误显示在线
func (r *NodeRepo) MarkOffline(id string) error {
	return r.db.Model(&model.Node{}).Where("id = ?", id).
		UpdateColumn("online", false).Error
}

// DeleteUserNodesByNodeID 软删除指定节点的所有 user_nodes 关联(避免孤儿记录)
func (r *NodeRepo) DeleteUserNodesByNodeID(nodeID string) error {
	return r.db.Model(&model.UserNode{}).Where("node_id = ? AND is_deleted = false", nodeID).
		Update("is_deleted", true).Error
}

// UpdateToken 轮换节点 token
func (r *NodeRepo) UpdateToken(id, token string) error {
	return r.db.Model(&model.Node{}).Where("id = ? AND is_deleted = false", id).
		Update("node_token", token).Error
}

// UpdateOnline 更新节点在线状态与最后上报时间
func (r *NodeRepo) UpdateOnline(id string, online bool, version string, t time.Time) error {
	return r.db.Model(&model.Node{}).Where("id = ? AND is_deleted = false", id).
		UpdateColumns(map[string]interface{}{
			"online":       online,
			"version":      version,
			"last_seen_at": t,
		}).Error
}

// CountAll 统计节点总数
func (r *NodeRepo) CountAll() (int64, error) {
	var n int64
	err := r.db.Model(&model.Node{}).Where("is_deleted = false").Count(&n).Error
	return n, err
}

// CountOnline 统计在线节点数
func (r *NodeRepo) CountOnline() (int64, error) {
	var n int64
	err := r.db.Model(&model.Node{}).Where("is_deleted = false AND online = true").Count(&n).Error
	return n, err
}

// CountEnabled 统计启用节点数
func (r *NodeRepo) CountEnabled() (int64, error) {
	var n int64
	err := r.db.Model(&model.Node{}).Where("is_deleted = false AND is_enabled = true").Count(&n).Error
	return n, err
}

// AddTrafficTx 在指定事务内累加节点流量统计

// TouchAllEnabled bumps updated_at on all enabled nodes to trigger config refresh on next heartbeat
func (r *NodeRepo) TouchAllEnabled() error {
	return r.db.Model(&model.Node{}).
		Where("is_deleted = false AND is_enabled = true").
		Update("updated_at", time.Now()).Error
}

func (r *NodeRepo) AddTrafficTx(tx *gorm.DB, id string, bytes int64) error {
	return tx.Model(&model.Node{}).Where("id = ? AND is_deleted = false", id).
		UpdateColumn("traffic_used", gorm.Expr("traffic_used + ?", bytes)).Error
}

// TrafficGroupRow 按 server_address 聚合的流量汇总行
type TrafficGroupRow struct {
	ServerAddress string `json:"server_address"`
	NodeCount     int64  `json:"node_count"`
	TrafficUsed   int64  `json:"traffic_used"`
	TrafficLimit  int64  `json:"traffic_limit"`
}

// TrafficSummaryByServer 按 server_address 聚合流量(管理员后台统一流量展示用)
// 同一台服务器 IP 下多个节点: 累加使用量、累加上限、统计节点数
func (r *NodeRepo) TrafficSummaryByServer() ([]TrafficGroupRow, error) {
	var rows []TrafficGroupRow
	err := r.db.Model(&model.Node{}).
		Select("server_address, COUNT(*) as node_count, COALESCE(SUM(traffic_used),0) as traffic_used, COALESCE(SUM(traffic_limit),0) as traffic_limit").
		Where("is_deleted = false").
		Group("server_address").
		Order("traffic_used DESC").
		Scan(&rows).Error
	return rows, err
}
