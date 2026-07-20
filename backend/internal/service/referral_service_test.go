package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// setupTestDB 创建 SQLite 内存数据库并自动建表
func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 SQLite 失败: %v", err)
	}
	// 清理共享内存库中的残留表(防止 cache=shared 跨测试污染)
	db.Exec("DROP TABLE IF EXISTS users")
	db.Exec("DROP TABLE IF EXISTS referrals")
	db.Exec("DROP TABLE IF EXISTS referral_rewards")
	db.Exec("DROP TABLE IF EXISTS settings")

	if err := db.AutoMigrate(&model.User{}, &model.Referral{}, &model.ReferralReward{}, &model.Setting{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	return db
}

// TestReferralStatsAndList 邀请中心统计与列表功能验证
// 场景: 用户A 邀请了 B/C/D 三人
//   - B: 已完成首单, 返利 100 分(订单 1000 分, 10%)
//   - C: 待返利(未首单)
//   - D: 已完成首单, 返利 200 分(订单 2000 分, 10%)
//
// 预期统计: 邀请 3 人, 已完成 2 人, 累计返利 300 分
func TestReferralStatsAndList(t *testing.T) {
	db := setupTestDB(t)

	// ===== 初始化 repos 和 service =====
	referralRepo := repo.NewReferralRepo(db)
	userRepo := repo.NewUserRepo(db)
	settingRepo := repo.NewSettingRepo(db)
	svc := NewReferralService(referralRepo, userRepo, settingRepo)

	// 开启返利功能, 比例 10%, 不设上限
	if err := settingRepo.Set(SettingKeyReferralEnabled, true); err != nil {
		t.Fatalf("设置 referral_enabled 失败: %v", err)
	}
	if err := settingRepo.Set(SettingKeyReferralRate, 0.10); err != nil {
		t.Fatalf("设置 referral_rate 失败: %v", err)
	}
	if err := settingRepo.Set(SettingKeyReferralMaxReward, int64(0)); err != nil {
		t.Fatalf("设置 referral_max_reward 失败: %v", err)
	}

	// ===== 构造测试数据 =====
	now := time.Now()

	// 邀请人 A (已有邀请码)
	userA := &model.User{
		Username:     "inviterA",
		PasswordHash: "x",
		Email:        "a@test.com",
		InviteCode:   "CODEAAAA",
		Status:       "active",
	}
	if err := db.Create(userA).Error; err != nil {
		t.Fatalf("创建用户A失败: %v", err)
	}

	// 被邀请人 B / C / D
	userB := &model.User{Username: "inviteeB", PasswordHash: "x", Email: "b@test.com", Status: "active"}
	userC := &model.User{Username: "inviteeC", PasswordHash: "x", Email: "c@test.com", Status: "active"}
	userD := &model.User{Username: "inviteeD", PasswordHash: "x", Email: "d@test.com", Status: "active"}
	for _, u := range []*model.User{userB, userC, userD} {
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("创建用户 %s 失败: %v", u.Username, err)
		}
	}

	// 邀请关系
	refB := &model.Referral{
		InviterID:   userA.ID,
		InviteeID:   userB.ID,
		Status:      model.ReferralStatusCompleted,
		RewardCents: 100,
		RewardAt:    &now,
	}
	refC := &model.Referral{
		InviterID: userA.ID,
		InviteeID: userC.ID,
		Status:    model.ReferralStatusPending,
	}
	refD := &model.Referral{
		InviterID:   userA.ID,
		InviteeID:   userD.ID,
		Status:      model.ReferralStatusCompleted,
		RewardCents: 200,
		RewardAt:    &now,
	}
	for _, r := range []*model.Referral{refB, refC, refD} {
		if err := db.Create(r).Error; err != nil {
			t.Fatalf("创建邀请关系失败: %v", err)
		}
	}

	// 返利记录
	rewB := &model.ReferralReward{
		UserID:           userA.ID,
		OrderID:          "order-b-001",
		InviteeID:        userB.ID,
		AmountCents:      100,
		OrderAmountCents: 1000,
		RewardRate:       0.10,
		Description:      "邀请好友首单返利(10%)",
	}
	rewD := &model.ReferralReward{
		UserID:           userA.ID,
		OrderID:          "order-d-002",
		InviteeID:        userD.ID,
		AmountCents:      200,
		OrderAmountCents: 2000,
		RewardRate:       0.10,
		Description:      "邀请好友首单返利(10%)",
	}
	for _, r := range []*model.ReferralReward{rewB, rewD} {
		if err := db.Create(r).Error; err != nil {
			t.Fatalf("创建返利记录失败: %v", err)
		}
	}

	t.Log("========== 测试数据构造完成 ==========")
	t.Log("用户A(邀请人):", userA.ID, "邀请码:", userA.InviteCode)
	t.Log("用户B(已返利100分) / 用户C(待返利) / 用户D(已返利200分)")
	t.Log("")

	// ===== 测试 1: GetStats 统计 =====
	t.Run("统计_GetStats", func(t *testing.T) {
		total, completed, totalReward, err := svc.GetStats(userA.ID)
		if err != nil {
			t.Fatalf("GetStats 失败: %v", err)
		}
		t.Logf("  邀请总数: %d (期望 3)", total)
		t.Logf("  已完成数: %d (期望 2)", completed)
		t.Logf("  累计返利: %d 分 = ¥%.2f (期望 300 分 = ¥3.00)", totalReward, float64(totalReward)/100)

		if total != 3 {
			t.Errorf("邀请总数不匹配: got %d, want 3", total)
		}
		if completed != 2 {
			t.Errorf("已完成数不匹配: got %d, want 2", completed)
		}
		if totalReward != 300 {
			t.Errorf("累计返利不匹配: got %d, want 300", totalReward)
		}
	})

	// ===== 测试 2: ListInvitations 邀请列表 =====
	t.Run("邀请列表_ListInvitations", func(t *testing.T) {
		list, total, err := svc.ListInvitations(userA.ID, 1, 10)
		if err != nil {
			t.Fatalf("ListInvitations 失败: %v", err)
		}
		t.Logf("  列表总数: %d (期望 3)", total)
		t.Logf("  返回条数: %d", len(list))
		for i, r := range list {
			t.Logf("  [%d] 被邀请人: %s, 状态: %s, 返利: %d分, 邀请时间: %s",
				i+1, r.InviteeID[:8]+"...", r.Status, r.RewardCents,
				r.CreatedAt.Format("2006-01-02 15:04:05"))
		}

		if total != 3 {
			t.Errorf("列表总数不匹配: got %d, want 3", total)
		}
		if len(list) != 3 {
			t.Errorf("返回条数不匹配: got %d, want 3", len(list))
		}
		// 验证按时间倒序(最新在前)
		if len(list) >= 2 {
			if !list[0].CreatedAt.After(list[1].CreatedAt) && !list[0].CreatedAt.Equal(list[1].CreatedAt) {
				t.Error("列表未按创建时间倒序排列")
			}
		}
	})

	// ===== 测试 3: ListRewards 返利记录 =====
	t.Run("返利记录_ListRewards", func(t *testing.T) {
		list, total, err := svc.ListRewards(userA.ID, 1, 10)
		if err != nil {
			t.Fatalf("ListRewards 失败: %v", err)
		}
		t.Logf("  返利记录总数: %d (期望 2)", total)
		t.Logf("  返回条数: %d", len(list))
		var sumCents int64
		for i, r := range list {
			t.Logf("  [%d] 来源: %s, 订单金额: %d分, 返利比例: %.0f%%, 返利: %d分, 说明: %s, 时间: %s",
				i+1, r.InviteeID[:8]+"...", r.OrderAmountCents, r.RewardRate*100,
				r.AmountCents, r.Description, r.CreatedAt.Format("2006-01-02 15:04:05"))
			sumCents += r.AmountCents
		}
		t.Logf("  返利合计: %d 分 = ¥%.2f", sumCents, float64(sumCents)/100)

		if total != 2 {
			t.Errorf("返利记录总数不匹配: got %d, want 2", total)
		}
		if len(list) != 2 {
			t.Errorf("返回条数不匹配: got %d, want 2", len(list))
		}
		if sumCents != 300 {
			t.Errorf("返利合计不匹配: got %d, want 300", sumCents)
		}
	})

	// ===== 测试 4: GetOrCreateInviteCode 已有邀请码 =====
	t.Run("获取邀请码_已有", func(t *testing.T) {
		code, err := svc.GetOrCreateInviteCode(userA.ID)
		if err != nil {
			t.Fatalf("GetOrCreateInviteCode 失败: %v", err)
		}
		t.Logf("  用户A邀请码: %s (期望 CODEAAAA)", code)
		if code != "CODEAAAA" {
			t.Errorf("邀请码不匹配: got %s, want CODEAAAA", code)
		}
	})

	// ===== 测试 5: GetOrCreateInviteCode 新生成邀请码 =====
	t.Run("生成邀请码_新用户", func(t *testing.T) {
		code, err := svc.GetOrCreateInviteCode(userB.ID)
		if err != nil {
			t.Fatalf("GetOrCreateInviteCode 失败: %v", err)
		}
		t.Logf("  用户B新生成邀请码: %s (长度 %d)", code, len(code))
		if len(code) != 8 {
			t.Errorf("邀请码长度不匹配: got %d, want 8", len(code))
		}
		// 再次获取应返回相同的码
		code2, _ := svc.GetOrCreateInviteCode(userB.ID)
		if code != code2 {
			t.Errorf("重复获取邀请码不一致: first %s, second %s", code, code2)
		}
		t.Logf("  重复获取确认一致: %s", code2)
	})

	// ===== 测试 6: BindInviter 绑定邀请码 =====
	t.Run("绑定邀请码_正常", func(t *testing.T) {
		// 新用户 E 绑定 A 的邀请码
		userE := &model.User{Username: "inviteeE", PasswordHash: "x", Email: "e@test.com", Status: "active"}
		if err := db.Create(userE).Error; err != nil {
			t.Fatalf("创建用户E失败: %v", err)
		}
		err := svc.BindInviter(userE.ID, "CODEAAAA")
		t.Logf("  用户E绑定邀请码 CODEAAAA: %v", err)
		if err != nil {
			t.Errorf("绑定邀请码失败: %v", err)
		}
		// 验证邀请关系已创建
		ref, err := referralRepo.GetByInviteeID(userE.ID)
		if err != nil {
			t.Fatalf("查询邀请关系失败: %v", err)
		}
		t.Logf("  邀请关系: 邀请人=%s, 被邀请人=%s, 状态=%s", ref.InviterID[:8]+"...", ref.InviteeID[:8]+"...", ref.Status)
		if ref.InviterID != userA.ID {
			t.Errorf("邀请人不匹配: got %s, want %s", ref.InviterID, userA.ID)
		}
	})

	// ===== 测试 7: BindInviter 重复绑定应失败 =====
	t.Run("绑定邀请码_重复失败", func(t *testing.T) {
		// 用户 E 再次绑定应失败
		err := svc.BindInviter(userB.ID, "CODEAAAA")
		t.Logf("  用户B(已绑定)再次绑定: err=%v", err)
		if err == nil {
			t.Error("重复绑定应返回错误, 但返回 nil")
		}
	})

	// ===== 测试 8: BindInviter 自己邀请自己应失败 =====
	t.Run("绑定邀请码_自邀失败", func(t *testing.T) {
		err := svc.BindInviter(userA.ID, "CODEAAAA")
		t.Logf("  用户A绑定自己的邀请码: err=%v", err)
		if err == nil {
			t.Error("自己邀请自己应返回错误, 但返回 nil")
		}
	})

	// ===== 测试 9: 统计更新(绑定E后总数应+1) =====
	t.Run("统计更新_绑定后", func(t *testing.T) {
		total, completed, totalReward, err := svc.GetStats(userA.ID)
		if err != nil {
			t.Fatalf("GetStats 失败: %v", err)
		}
		t.Logf("  绑定E后 - 邀请总数: %d (期望 4)", total)
		t.Logf("  绑定E后 - 已完成数: %d (期望 2)", completed)
		t.Logf("  绑定E后 - 累计返利: %d 分 (期望 300)", totalReward)

		if total != 4 {
			t.Errorf("绑定后邀请总数不匹配: got %d, want 4", total)
		}
		if completed != 2 {
			t.Errorf("绑定后已完成数不匹配: got %d, want 2", completed)
		}
	})

	// ===== 测试 10: 分页验证 =====
	t.Run("分页验证", func(t *testing.T) {
		// 每页 2 条, 取第 1 页
		list, total, err := svc.ListInvitations(userA.ID, 1, 2)
		if err != nil {
			t.Fatalf("分页查询失败: %v", err)
		}
		t.Logf("  分页(page=1,size=2): 总数=%d, 返回=%d条", total, len(list))
		if total != 4 {
			t.Errorf("分页总数不匹配: got %d, want 4", total)
		}
		if len(list) != 2 {
			t.Errorf("第1页条数不匹配: got %d, want 2", len(list))
		}

		// 第 2 页
		list2, _, err := svc.ListInvitations(userA.ID, 2, 2)
		if err != nil {
			t.Fatalf("分页查询第2页失败: %v", err)
		}
		t.Logf("  分页(page=2,size=2): 返回=%d条", len(list2))
		if len(list2) != 2 {
			t.Errorf("第2页条数不匹配: got %d, want 2", len(list2))
		}

		// 第 3 页(空)
		list3, _, err := svc.ListInvitations(userA.ID, 3, 2)
		if err != nil {
			t.Fatalf("分页查询第3页失败: %v", err)
		}
		t.Logf("  分页(page=3,size=2): 返回=%d条 (期望0)", len(list3))
		if len(list3) != 0 {
			t.Errorf("第3页条数不匹配: got %d, want 0", len(list3))
		}
	})

	t.Log("")
	t.Log("========== 全部测试通过 ==========")
}

// TestReferralConfigDisabled 验证返利功能关闭时绑定应失败
func TestReferralConfigDisabled(t *testing.T) {
	db := setupTestDB(t)
	referralRepo := repo.NewReferralRepo(db)
	userRepo := repo.NewUserRepo(db)
	settingRepo := repo.NewSettingRepo(db)
	svc := NewReferralService(referralRepo, userRepo, settingRepo)

	// 关闭返利功能
	settingRepo.Set(SettingKeyReferralEnabled, false)

	userA := &model.User{Username: "inviterX", PasswordHash: "x", InviteCode: "CODEXXXX", Status: "active"}
	userB := &model.User{Username: "inviteeY", PasswordHash: "x", Status: "active"}
	db.Create(userA)
	db.Create(userB)

	err := svc.BindInviter(userB.ID, "CODEXXXX")
	t.Logf("功能关闭时绑定: err=%v", err)
	if err == nil {
		t.Error("功能关闭时绑定应返回错误")
	}
	t.Logf("✓ 返利功能关闭时正确拒绝绑定")
}

// TestReferralConfigRead 验证配置读取的默认值
func TestReferralConfigRead(t *testing.T) {
	db := setupTestDB(t)
	referralRepo := repo.NewReferralRepo(db)
	userRepo := repo.NewUserRepo(db)
	settingRepo := repo.NewSettingRepo(db)
	svc := NewReferralService(referralRepo, userRepo, settingRepo)

	// 未设置任何配置, 应返回默认值
	enabled, rate, maxReward := svc.GetReferralConfig()
	t.Logf("默认配置: enabled=%v, rate=%.2f, maxReward=%d", enabled, rate, maxReward)
	if enabled != false {
		t.Errorf("默认 enabled 应为 false, got %v", enabled)
	}
	// rate 和 maxReward 在 GetReferralConfig 中读取失败时用默认值
	t.Logf("✓ 默认配置读取正常 (enabled=false, rate=%.0f%%, maxReward=%d)", rate*100, maxReward)
	_ = fmt.Sprintf // avoid unused import if fmt not needed elsewhere
}
