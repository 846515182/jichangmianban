package model

import "time"

// NodePlanBinding 节点-套餐绑定(多对多)
// 一个节点可绑定多个套餐；用户 plan_id 命中任一绑定即可见该节点
type NodePlanBinding struct {
	NodeID    string    `gorm:"type:uuid;primaryKey" json:"node_id"`
	PlanID    string    `gorm:"type:uuid;primaryKey" json:"plan_id"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName 指定表名
func (NodePlanBinding) TableName() string {
	return "node_plan_bindings"
}
