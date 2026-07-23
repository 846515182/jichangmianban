package service

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"nexus-panel/internal/app"
	"nexus-panel/internal/config"
	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// setupNodeTestDB 创建 SQLite 内存数据库并初始化节点相关表,
// 同时设置全局 app.App(因为 NodeService 内部多处直接使用 app.Get().DB)
func setupNodeTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 SQLite 失败: %v", err)
	}
	// 清理残留表
	db.Exec("DROP TABLE IF EXISTS nodes")
	db.Exec("DROP TABLE IF EXISTS node_plan_bindings")
	db.Exec("DROP TABLE IF EXISTS users")
	db.Exec("DROP TABLE IF EXISTS plans")

	if err := db.AutoMigrate(
		&model.Node{},
		&model.NodePlanBinding{},
		&model.User{},
		&model.Plan{},
	); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}

	app.Init(&config.Config{
		AESMasterKey: "0123456789abcdef0123456789abcdef", // 32 字节测试密钥
	}, db, nil, nil)
	return db
}

func TestCreateNode_UnsupportedProtocol(t *testing.T) {
	setupNodeTestDB(t)
	svc := NewNodeService(repo.NewNodeRepo(app.Get().DB))

	_, err := svc.CreateNode(&CreateNodeInput{
		Name:          "trojan-node",
		Protocol:      "trojan",
		ServerAddress: "1.2.3.4",
		Port:          443,
		PlanIDs:       []string{"plan-1"},
	})
	if err == nil {
		t.Fatal("创建 trojan 节点应该失败")
	}
	if !contains(err.Error(), "仅支持 vless") {
		t.Fatalf("错误信息应提示仅支持 vless, 实际: %v", err)
	}
}

func TestCreateNode_GrpcPortDefault(t *testing.T) {
	db := setupNodeTestDB(t)
	svc := NewNodeService(repo.NewNodeRepo(db))

	// 先创建一个套餐, 否则 ReplacePlanBindings 会外键失败
	plan := &model.Plan{Name: "p1", PriceCents: 100, DurationDays: 30, IsEnabled: true}
	if err := db.Create(plan).Error; err != nil {
		t.Fatalf("创建套餐失败: %v", err)
	}

	node, err := svc.CreateNode(&CreateNodeInput{
		Name:          "grpc-default",
		Protocol:      "vless",
		ServerAddress: "1.2.3.4",
		Port:          443,
		GrpcPort:      0, // 测试默认值
		PlanIDs:       []string{plan.ID},
		ExtraConfig:   map[string]interface{}{"reality": map[string]interface{}{"dest": ""}},
	})
	if err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}
	if node.GrpcPort != 50051 {
		t.Fatalf("GrpcPort 应为 50051, 实际: %d", node.GrpcPort)
	}
}

func TestUpdateNode_DisableSyncOffline(t *testing.T) {
	db := setupNodeTestDB(t)
	svc := NewNodeService(repo.NewNodeRepo(db))

	plan := &model.Plan{Name: "p1", PriceCents: 100, DurationDays: 30, IsEnabled: true}
	if err := db.Create(plan).Error; err != nil {
		t.Fatalf("创建套餐失败: %v", err)
	}

	node, err := svc.CreateNode(&CreateNodeInput{
		Name:          "node-disable",
		Protocol:      "vless",
		ServerAddress: "1.2.3.4",
		Port:          443,
		PlanIDs:       []string{plan.ID},
		ExtraConfig:   map[string]interface{}{"reality": map[string]interface{}{"dest": ""}},
	})
	if err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}
	// 模拟在线
	if err := db.Model(&model.Node{}).Where("id = ?", node.ID).Update("online", true).Error; err != nil {
		t.Fatalf("设置在线失败: %v", err)
	}

	disable := false
	updated, err := svc.UpdateNode(node.ID, &UpdateNodeInput{
		IsEnabled: &disable,
	})
	if err != nil {
		t.Fatalf("更新节点失败: %v", err)
	}
	if updated.Online {
		t.Fatal("禁用节点后 online 应为 false")
	}
}

func TestUpdateNode_Uniqueness(t *testing.T) {
	db := setupNodeTestDB(t)
	svc := NewNodeService(repo.NewNodeRepo(db))

	plan := &model.Plan{Name: "p1", PriceCents: 100, DurationDays: 30, IsEnabled: true}
	if err := db.Create(plan).Error; err != nil {
		t.Fatalf("创建套餐失败: %v", err)
	}

	nodeA, err := svc.CreateNode(&CreateNodeInput{
		Name:          "node-a",
		Protocol:      "vless",
		ServerAddress: "1.2.3.4",
		Port:          443,
		PlanIDs:       []string{plan.ID},
		ExtraConfig:   map[string]interface{}{"reality": map[string]interface{}{"dest": ""}},
	})
	if err != nil {
		t.Fatalf("创建节点 A 失败: %v", err)
	}
	nodeB, err := svc.CreateNode(&CreateNodeInput{
		Name:          "node-b",
		Protocol:      "vless",
		ServerAddress: "5.6.7.8",
		Port:          443,
		PlanIDs:       []string{plan.ID},
		ExtraConfig:   map[string]interface{}{"reality": map[string]interface{}{"dest": ""}},
	})
	if err != nil {
		t.Fatalf("创建节点 B 失败: %v", err)
	}

	// 把 B 的名称改成 A 的名称, 应失败
	newName := nodeA.Name
	_, err = svc.UpdateNode(nodeB.ID, &UpdateNodeInput{
		Name: &newName,
	})
	if err == nil {
		t.Fatal("更新为重复名称应该失败")
	}
	if !contains(err.Error(), "名称") {
		t.Fatalf("错误信息应包含名称, 实际: %v", err)
	}

	// 把 B 的地址+端口改成 A 的地址+端口, 应失败
	newAddr := nodeA.ServerAddress
	newPort := nodeA.Port
	_, err = svc.UpdateNode(nodeB.ID, &UpdateNodeInput{
		ServerAddress: &newAddr,
		Port:          &newPort,
	})
	if err == nil {
		t.Fatal("更新为重复地址端口应该失败")
	}
	if !contains(err.Error(), "已被其他节点占用") {
		t.Fatalf("错误信息应提示地址被占用, 实际: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
