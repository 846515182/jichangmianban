package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// =============================================================================
// parseUserTrafficStat 测试
// =============================================================================

func TestParseUserTrafficStat_ValidUplink(t *testing.T) {
	uuid := "d771e997-37f4-4ec7-915a-3c293f8950dd"
	name := "user>>>" + uuid + ">>>traffic>>>uplink"
	gotUID, gotIsUp := parseUserTrafficStat(name)
	if gotUID != uuid {
		t.Fatalf("expected uid=%s, got=%s", uuid, gotUID)
	}
	if !gotIsUp {
		t.Fatal("expected isUp=true for uplink stat")
	}
}

func TestParseUserTrafficStat_ValidDownlink(t *testing.T) {
	uuid := "d771e997-37f4-4ec7-915a-3c293f8950dd"
	name := "user>>>" + uuid + ">>>traffic>>>downlink"
	gotUID, gotIsUp := parseUserTrafficStat(name)
	if gotUID != uuid {
		t.Fatalf("expected uid=%s, got=%s", uuid, gotUID)
	}
	if gotIsUp {
		t.Fatal("expected isUp=false for downlink stat")
	}
}

func TestParseUserTrafficStat_NotUserPrefix(t *testing.T) {
	// 非用户流量 stat（inbound/outbound 聚合统计）
	name := "inbound>>>api>>>traffic>>>uplink"
	gotUID, _ := parseUserTrafficStat(name)
	if gotUID != "" {
		t.Fatalf("expected empty for non-user stat, got=%s", gotUID)
	}
}

func TestParseUserTrafficStat_InvalidUUID(t *testing.T) {
	// user>>> 前缀但 UUID 不合法
	name := "user>>>not-a-valid-uuid>>>traffic>>>uplink"
	gotUID, _ := parseUserTrafficStat(name)
	if gotUID != "" {
		t.Fatalf("expected empty for invalid UUID, got=%s", gotUID)
	}
}

func TestParseUserTrafficStat_WrongSuffix(t *testing.T) {
	uuid := "d771e997-37f4-4ec7-915a-3c293f8950dd"
	// 既不是 uplink 也不是 downlink
	name := "user>>>" + uuid + ">>>traffic>>>unknown"
	gotUID, _ := parseUserTrafficStat(name)
	if gotUID != "" {
		t.Fatalf("expected empty for unknown traffic type, got=%s", gotUID)
	}
}

func TestParseUserTrafficStat_EmptyString(t *testing.T) {
	gotUID, _ := parseUserTrafficStat("")
	if gotUID != "" {
		t.Fatalf("expected empty for empty string, got=%s", gotUID)
	}
}

func TestParseUserTrafficStat_TooShort(t *testing.T) {
	gotUID, _ := parseUserTrafficStat("user>>>")
	if gotUID != "" {
		t.Fatalf("expected empty for too-short string, got=%s", gotUID)
	}
}

func TestParseUserTrafficStat_MissingTripleArrow(t *testing.T) {
	uuid := "d771e997-37f4-4ec7-915a-3c293f8950dd"
	// 缺少 >>> 分隔符
	name := "user>>>" + uuid + "traffic>>>uplink"
	gotUID, _ := parseUserTrafficStat(name)
	if gotUID != "" {
		t.Fatalf("expected empty for missing >>> separator, got=%s", gotUID)
	}
}

// =============================================================================
// isValidUUID 测试
// =============================================================================

func TestIsValidUUID_Valid(t *testing.T) {
	uuid := "d771e997-37f4-4ec7-915a-3c293f8950dd"
	if !isValidUUID(uuid) {
		t.Fatal("expected valid UUID")
	}
}

func TestIsValidUUID_WrongLength(t *testing.T) {
	if isValidUUID("short") {
		t.Fatal("expected false for short string")
	}
	if isValidUUID("d771e997-37f4-4ec7-915a-3c293f8950dd-extra") {
		t.Fatal("expected false for too-long string")
	}
}

func TestIsValidUUID_MissingHyphens(t *testing.T) {
	uuid := "d771e99737f44ec7915a3c293f8950dd" // 去掉连字符
	if isValidUUID(uuid) {
		t.Fatal("expected false for UUID without hyphens")
	}
}

func TestIsValidUUID_WrongHyphenPositions(t *testing.T) {
	uuid := "d771e-997-37f4-4ec7-915a-3c293f8950dd" // 第一个连字符位置错误
	if isValidUUID(uuid) {
		t.Fatal("expected false for wrong hyphen positions")
	}
}

// =============================================================================
// calculateDeltas 测试
// =============================================================================

const (
	testUUID1 = "11111111-1111-1111-1111-111111111111"
	testUUID2 = "22222222-2222-2222-2222-222222222222"
	testUUID3 = "33333333-3333-3333-3333-333333333333"
)

// TestCalculateDeltas_FirstQuery 首次查询：prev 为空，delta = cur - 0 = cur
// 注意：首次查询返回当前值是正确行为——agent 启动时用户可能已经产生了流量(Xray在agent之前启动)，
// 我们应该统计所有流量，不跳过首轮。
func TestCalculateDeltas_FirstQuery(t *testing.T) {
	prevUp := make(map[string]int64)
	prevDown := make(map[string]int64)

	currentUp := map[string]int64{testUUID1: 1000}
	currentDown := map[string]int64{testUUID1: 500}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 1 {
		t.Fatalf("首次查询有流量: 期望 1 条增量(cur-0=cur), 实际 %d 条", len(deltas))
	}
	if deltas[0].Upload != 1000 || deltas[0].Download != 500 {
		t.Fatalf("首次: upload=%d(期望1000), download=%d(期望500)", deltas[0].Upload, deltas[0].Download)
	}
	// 基线已更新
	if prevUp[testUUID1] != 1000 {
		t.Fatalf("prevUp 应更新为 1000，实际 %d", prevUp[testUUID1])
	}
	if prevDown[testUUID1] != 500 {
		t.Fatalf("prevDown 应更新为 500，实际 %d", prevDown[testUUID1])
	}
}

// TestCalculateDeltas_NormalIncrement 正常增量：两次查询之间产生的流量
func TestCalculateDeltas_NormalIncrement(t *testing.T) {
	prevUp := map[string]int64{testUUID1: 1000}
	prevDown := map[string]int64{testUUID1: 500}

	currentUp := map[string]int64{testUUID1: 3500}
	currentDown := map[string]int64{testUUID1: 1200}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 1 {
		t.Fatalf("应返回 1 条增量，实际 %d 条", len(deltas))
	}
	d := deltas[0]
	if d.UserID != testUUID1 {
		t.Fatalf("uid 应为 %s，实际 %s", testUUID1, d.UserID)
	}
	if d.Upload != 2500 {
		t.Fatalf("upload 增量应为 2500 (3500-1000)，实际 %d", d.Upload)
	}
	if d.Download != 700 {
		t.Fatalf("download 增量应为 700 (1200-500)，实际 %d", d.Download)
	}
	// 基线已更新
	if prevUp[testUUID1] != 3500 {
		t.Fatalf("prevUp 应更新为 3500，实际 %d", prevUp[testUUID1])
	}
}

// TestCalculateDeltas_NoChange 无变化：不返回增量
func TestCalculateDeltas_NoChange(t *testing.T) {
	prevUp := map[string]int64{testUUID1: 1000}
	prevDown := map[string]int64{testUUID1: 500}

	currentUp := map[string]int64{testUUID1: 1000}
	currentDown := map[string]int64{testUUID1: 500}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 0 {
		t.Fatalf("无变化应返回 0 条增量，实际 %d 条", len(deltas))
	}
}

// TestCalculateDeltas_CounterReset 计数器重置（Xray 重启）
func TestCalculateDeltas_CounterReset(t *testing.T) {
	prevUp := map[string]int64{testUUID1: 500000}
	prevDown := map[string]int64{testUUID1: 200000}

	// Xray 重启后 counter 归零，重新开始计数
	currentUp := map[string]int64{testUUID1: 100}
	currentDown := map[string]int64{testUUID1: 50}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 1 {
		t.Fatalf("应返回 1 条增量（重置场景），实际 %d 条", len(deltas))
	}
	d := deltas[0]
	// delta 为负触发重置逻辑，用当前值作为增量
	if d.Upload != 100 {
		t.Fatalf("重置场景 upload 应用当前值 100，实际 %d", d.Upload)
	}
	if d.Download != 50 {
		t.Fatalf("重置场景 download 应用当前值 50，实际 %d", d.Download)
	}
}

// TestCalculateDeltas_MultipleUsers 多用户场景
func TestCalculateDeltas_MultipleUsers(t *testing.T) {
	prevUp := map[string]int64{
		testUUID1: 1000,
		testUUID2: 2000,
	}
	prevDown := map[string]int64{
		testUUID1: 500,
		testUUID2: 800,
	}

	currentUp := map[string]int64{
		testUUID1: 3500,
		testUUID2: 6000,
	}
	currentDown := map[string]int64{
		testUUID1: 1200,
		testUUID2: 2500,
	}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 2 {
		t.Fatalf("应返回 2 条增量，实际 %d 条", len(deltas))
	}

	// 建立索引
	deltaMap := make(map[string]TrafficDelta)
	for _, d := range deltas {
		deltaMap[d.UserID] = d
	}

	d1 := deltaMap[testUUID1]
	if d1.Upload != 2500 || d1.Download != 700 {
		t.Fatalf("用户1: upload=%d(期望2500), download=%d(期望700)", d1.Upload, d1.Download)
	}

	d2 := deltaMap[testUUID2]
	if d2.Upload != 4000 || d2.Download != 1700 {
		t.Fatalf("用户2: upload=%d(期望4000), download=%d(期望1700)", d2.Upload, d2.Download)
	}
}

// TestCalculateDeltas_UserRemoved 用户被移除：旧基线应被清理
func TestCalculateDeltas_UserRemoved(t *testing.T) {
	prevUp := map[string]int64{
		testUUID1: 1000,
		testUUID2: 2000, // 此用户将被移除
	}
	prevDown := map[string]int64{
		testUUID1: 500,
		testUUID2: 800,
	}

	// testUUID2 不在当前快照中
	currentUp := map[string]int64{testUUID1: 3000}
	currentDown := map[string]int64{testUUID1: 900}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 1 {
		t.Fatalf("应返回 1 条增量（移除的用户不上报），实际 %d 条", len(deltas))
	}

	// 基线不应再包含 testUUID2
	if _, ok := prevUp[testUUID2]; ok {
		t.Fatal("prevUp 中应移除 testUUID2")
	}
	if _, ok := prevDown[testUUID2]; ok {
		t.Fatal("prevDown 中应移除 testUUID2")
	}
	// testUUID1 应保留
	if prevUp[testUUID1] != 3000 {
		t.Fatalf("prevUp[testUUID1] 应为 3000，实际 %d", prevUp[testUUID1])
	}
}

// TestCalculateDeltas_NewUserAdded 新用户出现：当前快照有新用户，prev 中无记录
func TestCalculateDeltas_NewUserAdded(t *testing.T) {
	prevUp := map[string]int64{testUUID1: 1000}
	prevDown := map[string]int64{testUUID1: 500}

	// testUUID2 是新出现的用户
	currentUp := map[string]int64{
		testUUID1: 3000,
		testUUID2: 500,
	}
	currentDown := map[string]int64{
		testUUID1: 900,
		testUUID2: 200,
	}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 2 {
		t.Fatalf("应返回 2 条增量，实际 %d 条", len(deltas))
	}

	deltaMap := make(map[string]TrafficDelta)
	for _, d := range deltas {
		deltaMap[d.UserID] = d
	}

	// 新用户的增量 = 当前值 - 0 = 当前值
	d2 := deltaMap[testUUID2]
	if d2.Upload != 500 {
		t.Fatalf("新用户 upload 应为 500，实际 %d", d2.Upload)
	}
	if d2.Download != 200 {
		t.Fatalf("新用户 download 应为 200，实际 %d", d2.Download)
	}

	// 基线已更新
	if prevUp[testUUID2] != 500 {
		t.Fatalf("prevUp[testUUID2] 应为 500，实际 %d", prevUp[testUUID2])
	}
}

// TestCalculateDeltas_OnlyUploadChanges 仅上行流量变化
func TestCalculateDeltas_OnlyUploadChanges(t *testing.T) {
	prevUp := map[string]int64{testUUID1: 1000}
	prevDown := map[string]int64{testUUID1: 500}

	currentUp := map[string]int64{testUUID1: 3000}
	currentDown := map[string]int64{testUUID1: 500} // 不变

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 1 {
		t.Fatalf("应返回 1 条增量，实际 %d 条", len(deltas))
	}
	if deltas[0].Upload != 2000 {
		t.Fatalf("upload 应为 2000，实际 %d", deltas[0].Upload)
	}
	if deltas[0].Download != 0 {
		t.Fatalf("download 应为 0（无变化），实际 %d", deltas[0].Download)
	}
}

// TestCalculateDeltas_OnlyDownloadChanges 仅下行流量变化
func TestCalculateDeltas_OnlyDownloadChanges(t *testing.T) {
	prevUp := map[string]int64{testUUID1: 1000}
	prevDown := map[string]int64{testUUID1: 500}

	currentUp := map[string]int64{testUUID1: 1000} // 不变
	currentDown := map[string]int64{testUUID1: 1500}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 1 {
		t.Fatalf("应返回 1 条增量，实际 %d 条", len(deltas))
	}
	if deltas[0].Upload != 0 {
		t.Fatalf("upload 应为 0（无变化），实际 %d", deltas[0].Upload)
	}
	if deltas[0].Download != 1000 {
		t.Fatalf("download 应为 1000，实际 %d", deltas[0].Download)
	}
}

// TestCalculateDeltas_LargeNumbers 大数据量（MB/GB 级流量）
func TestCalculateDeltas_LargeNumbers(t *testing.T) {
	prevUp := map[string]int64{testUUID1: 100_000_000_000} // 100 GB
	prevDown := map[string]int64{testUUID1: 50_000_000_000}

	currentUp := map[string]int64{testUUID1: 101_500_000_000}  // +1.5 GB
	currentDown := map[string]int64{testUUID1: 50_300_000_000} // +300 MB

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 1 {
		t.Fatalf("应返回 1 条增量，实际 %d 条", len(deltas))
	}
	if deltas[0].Upload != 1_500_000_000 {
		t.Fatalf("upload 应为 1500000000，实际 %d", deltas[0].Upload)
	}
	if deltas[0].Download != 300_000_000 {
		t.Fatalf("download 应为 300000000，实际 %d", deltas[0].Download)
	}
}

// TestCalculateDeltas_EmptyAll 空快照不返回增量
func TestCalculateDeltas_EmptyAll(t *testing.T) {
	prevUp := make(map[string]int64)
	prevDown := make(map[string]int64)
	currentUp := make(map[string]int64)
	currentDown := make(map[string]int64)

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 0 {
		t.Fatalf("全部为空应返回 0 条增量，实际 %d 条", len(deltas))
	}
}

// TestCalculateDeltas_ThreeRounds 连续三轮查询验证累计正确
func TestCalculateDeltas_ThreeRounds(t *testing.T) {
	prevUp := make(map[string]int64)
	prevDown := make(map[string]int64)

	// 第 1 轮：prev 为空 → delta = cur - 0 = cur（上报全部已有流量）
	currentUp := map[string]int64{testUUID1: 1000}
	currentDown := map[string]int64{testUUID1: 500}
	d1 := calculateDeltas(prevUp, prevDown, currentUp, currentDown)
	if len(d1) != 1 {
		t.Fatalf("第1轮(首次)应返回 1 条，实际 %d 条", len(d1))
	}
	if d1[0].Upload != 1000 || d1[0].Download != 500 {
		t.Fatalf("第1轮: upload=%d(期望1000), download=%d(期望500)", d1[0].Upload, d1[0].Download)
	}

	// 第 2 轮：正常增量 3000-1000=2000, 1500-500=1000
	currentUp2 := map[string]int64{testUUID1: 3000}
	currentDown2 := map[string]int64{testUUID1: 1500}
	d2 := calculateDeltas(prevUp, prevDown, currentUp2, currentDown2)
	if len(d2) != 1 {
		t.Fatalf("第2轮应返回 1 条，实际 %d 条", len(d2))
	}
	if d2[0].Upload != 2000 || d2[0].Download != 1000 {
		t.Fatalf("第2轮: upload=%d(期望2000), download=%d(期望1000)", d2[0].Upload, d2[0].Download)
	}

	// 第 3 轮：再次增量 5000-3000=2000, 2500-1500=1000
	currentUp3 := map[string]int64{testUUID1: 5000}
	currentDown3 := map[string]int64{testUUID1: 2500}
	d3 := calculateDeltas(prevUp, prevDown, currentUp3, currentDown3)
	if len(d3) != 1 {
		t.Fatalf("第3轮应返回 1 条，实际 %d 条", len(d3))
	}
	if d3[0].Upload != 2000 || d3[0].Download != 1000 {
		t.Fatalf("第3轮: upload=%d(期望2000), download=%d(期望1000)", d3[0].Upload, d3[0].Download)
	}
}

// =============================================================================
// 流量突增场景
// =============================================================================

// TestCalculateDeltas_TrafficSpike 流量突增：用户突然产生大量流量（如下载大文件/4K 视频）
// 模拟 5 轮正常流量(每轮 ~1MB)后第 6 轮突增 50GB
func TestCalculateDeltas_TrafficSpike(t *testing.T) {
	prevUp := make(map[string]int64)
	prevDown := make(map[string]int64)
	uuid := testUUID1

	// 第 1 轮：建立基线 (100 KB)
	currentUp := map[string]int64{uuid: 102_400}
	currentDown := map[string]int64{uuid: 51_200}
	d1 := calculateDeltas(prevUp, prevDown, currentUp, currentDown)
	if len(d1) != 0 {
		t.Fatalf("第1轮(建基线): 期望0条, 实际%d条", len(d1))
	}

	// 第 2~5 轮：正常增量，每轮 +1MB upload, +500KB download
	expectedCumUp := int64(102_400)
	expectedCumDown := int64(51_200)
	for round := 2; round <= 5; round++ {
		expectedCumUp += 1_048_576
		expectedCumDown += 524_288
		currentUp := map[string]int64{uuid: expectedCumUp}
		currentDown := map[string]int64{uuid: expectedCumDown}
		d := calculateDeltas(prevUp, prevDown, currentUp, currentDown)
		if len(d) != 1 {
			t.Fatalf("第%d轮(正常): 期望1条, 实际%d条", round, len(d))
		}
		if d[0].Upload != 1_048_576 {
			t.Fatalf("第%d轮 upload: 期望1048576, 实际%d", round, d[0].Upload)
		}
		if d[0].Download != 524_288 {
			t.Fatalf("第%d轮 download: 期望524288, 实际%d", round, d[0].Download)
		}
	}

	// 第 6 轮：突增 50GB upload + 10GB download（模拟大量下载）
	spikeUp := int64(50 * 1024 * 1024 * 1024)   // 50 GB
	spikeDown := int64(10 * 1024 * 1024 * 1024) // 10 GB
	currentUp6 := map[string]int64{uuid: expectedCumUp + spikeUp}
	currentDown6 := map[string]int64{uuid: expectedCumDown + spikeDown}
	d6 := calculateDeltas(prevUp, prevDown, currentUp6, currentDown6)
	if len(d6) != 1 {
		t.Fatalf("第6轮(突增): 期望1条, 实际%d条", len(d6))
	}
	if d6[0].Upload != spikeUp {
		t.Fatalf("突增 upload: 期望%d (50GB), 实际%d", spikeUp, d6[0].Upload)
	}
	if d6[0].Download != spikeDown {
		t.Fatalf("突增 download: 期望%d (10GB), 实际%d", spikeDown, d6[0].Download)
	}

	// 第 7 轮：恢复正常 (spike 后 +500KB)
	normalUp := int64(524_288)
	normalDown := int64(262_144)
	currentUp7 := map[string]int64{uuid: expectedCumUp + spikeUp + normalUp}
	currentDown7 := map[string]int64{uuid: expectedCumDown + spikeDown + normalDown}
	d7 := calculateDeltas(prevUp, prevDown, currentUp7, currentDown7)
	if len(d7) != 1 {
		t.Fatalf("第7轮(恢复正常): 期望1条, 实际%d条", len(d7))
	}
	if d7[0].Upload != normalUp {
		t.Fatalf("恢复正常 upload: 期望%d, 实际%d", normalUp, d7[0].Upload)
	}
	if d7[0].Download != normalDown {
		t.Fatalf("恢复正常 download: 期望%d, 实际%d", normalDown, d7[0].Download)
	}

	t.Logf("流量突增验证通过: 正常%d轮 + 50GB突增 + 恢复, 累计上行=%d",
		5, expectedCumUp+spikeUp+normalUp)
}

// TestCalculateDeltas_SpikeThenReset 流量突增后 Xray 重启（counter 归零）
// 用户有大流量 → Xray 重启 → counter 重置 → 后续流量正确计算
func TestCalculateDeltas_SpikeThenReset(t *testing.T) {
	prevUp := make(map[string]int64)
	prevDown := make(map[string]int64)
	uuid := testUUID1

	// 第 1 轮：建基线
	currentUp := map[string]int64{uuid: 1_000_000}
	currentDown := map[string]int64{uuid: 500_000}
	calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	// 第 2 轮：突增 10GB
	spikeUp := int64(10 * 1024 * 1024 * 1024)
	spikeDown := int64(5 * 1024 * 1024 * 1024)
	currentUp2 := map[string]int64{uuid: 1_000_000 + spikeUp}
	currentDown2 := map[string]int64{uuid: 500_000 + spikeDown}
	d2 := calculateDeltas(prevUp, prevDown, currentUp2, currentDown2)
	if d2[0].Upload != spikeUp || d2[0].Download != spikeDown {
		t.Fatalf("突增轮: upload=%d(期望%d), download=%d(期望%d)",
			d2[0].Upload, spikeUp, d2[0].Download, spikeDown)
	}

	// 第 3 轮：Xray 重启，counter 归零到很小的值
	currentUp3 := map[string]int64{uuid: 500} // 重置后新增
	currentDown3 := map[string]int64{uuid: 200}
	d3 := calculateDeltas(prevUp, prevDown, currentUp3, currentDown3)
	if len(d3) != 1 {
		t.Fatalf("重置轮: 期望1条, 实际%d条", len(d3))
	}
	// 重置检测：delta 为负 → 用当前值替代
	if d3[0].Upload != 500 {
		t.Fatalf("重置轮 upload: 期望500(当前值), 实际%d", d3[0].Upload)
	}
	if d3[0].Download != 200 {
		t.Fatalf("重置轮 download: 期望200(当前值), 实际%d", d3[0].Download)
	}

	// 第 4 轮：重置后的正常增量
	currentUp4 := map[string]int64{uuid: 1500}
	currentDown4 := map[string]int64{uuid: 700}
	d4 := calculateDeltas(prevUp, prevDown, currentUp4, currentDown4)
	if d4[0].Upload != 1000 || d4[0].Download != 500 {
		t.Fatalf("重置后正常轮: upload=%d(期望1000), download=%d(期望500)",
			d4[0].Upload, d4[0].Download)
	}

	t.Log("流量突增 → 重启重置 → 恢复 验证通过")
}

// =============================================================================
// 混合场景：部分用户重置、部分正常
// =============================================================================

// TestCalculateDeltas_MixedReset 多用户混合场景：部分用户 counter 重置，部分正常
// 模拟: user1 正常增量, user2 Xray 重启 counter 归零, user3 无变化
func TestCalculateDeltas_MixedReset(t *testing.T) {
	prevUp := map[string]int64{
		testUUID1: 100_000_000,   // user1: 100MB 基线
		testUUID2: 500_000_000,   // user2: 500MB 基线（将被重置）
		testUUID3: 1_000_000_000, // user3: 1GB 基线
	}
	prevDown := map[string]int64{
		testUUID1: 50_000_000,
		testUUID2: 200_000_000,
		testUUID3: 500_000_000,
	}

	// 当前快照:
	// user1: 正常增量 +10MB upload +5MB download
	// user2: counter 重置为 1KB (delta 为负), 应取当前值
	// user3: 无变化
	currentUp := map[string]int64{
		testUUID1: 110_000_000,   // +10MB
		testUUID2: 1024,          // 重置！(预期512KB→实际1KB，取1KB)
		testUUID3: 1_000_000_000, // 无变化
	}
	currentDown := map[string]int64{
		testUUID1: 55_000_000,  // +5MB
		testUUID2: 512,         // 重置！
		testUUID3: 500_000_000, // 无变化
	}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 2 {
		t.Fatalf("应返回2条增量(user1正常+user2重置), user3(无变化)不上报, 实际%d条", len(deltas))
	}

	deltaMap := make(map[string]TrafficDelta)
	for _, d := range deltas {
		deltaMap[d.UserID] = d
	}

	// user1: 正常增量
	d1, ok := deltaMap[testUUID1]
	if !ok {
		t.Fatal("user1 应有增量记录")
	}
	if d1.Upload != 10_000_000 {
		t.Fatalf("user1 upload: 期望10000000(+10MB), 实际%d", d1.Upload)
	}
	if d1.Download != 5_000_000 {
		t.Fatalf("user1 download: 期望5000000(+5MB), 实际%d", d1.Download)
	}

	// user2: 重置，取当前值
	d2, ok := deltaMap[testUUID2]
	if !ok {
		t.Fatal("user2 应有增量记录（重置场景）")
	}
	if d2.Upload != 1024 {
		t.Fatalf("user2 upload: 期望1024(重置取当前值), 实际%d", d2.Upload)
	}
	if d2.Download != 512 {
		t.Fatalf("user2 download: 期望512(重置取当前值), 实际%d", d2.Download)
	}

	// user3: 不应在 deltas 中
	if _, ok := deltaMap[testUUID3]; ok {
		t.Fatal("user3 无变化，不应有增量记录")
	}

	t.Logf("混合重置验证通过: user1正常%d/%d, user2重置%d/%d, user3无变化",
		d1.Upload, d1.Download, d2.Upload, d2.Download)
}

// TestCalculateDeltas_AllUsersReset 全部用户同时重置（如整台机器重启）
func TestCalculateDeltas_AllUsersReset(t *testing.T) {
	prevUp := map[string]int64{
		testUUID1: 500_000_000,
		testUUID2: 1_000_000_000,
	}
	prevDown := map[string]int64{
		testUUID1: 200_000_000,
		testUUID2: 500_000_000,
	}

	// 全部重置到极小值
	currentUp := map[string]int64{
		testUUID1: 100,
		testUUID2: 200,
	}
	currentDown := map[string]int64{
		testUUID1: 50,
		testUUID2: 100,
	}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 2 {
		t.Fatalf("全部重置: 期望2条, 实际%d条", len(deltas))
	}

	deltaMap := make(map[string]TrafficDelta)
	for _, d := range deltas {
		deltaMap[d.UserID] = d
	}

	if deltaMap[testUUID1].Upload != 100 || deltaMap[testUUID1].Download != 50 {
		t.Fatalf("user1 重置: upload=%d(期望100), download=%d(期望50)",
			deltaMap[testUUID1].Upload, deltaMap[testUUID1].Download)
	}
	if deltaMap[testUUID2].Upload != 200 || deltaMap[testUUID2].Download != 100 {
		t.Fatalf("user2 重置: upload=%d(期望200), download=%d(期望100)",
			deltaMap[testUUID2].Upload, deltaMap[testUUID2].Download)
	}

	t.Log("全部用户重置验证通过")
}

// =============================================================================
// 重置检测正确性验证 — 确保 delta 不会为负数
// =============================================================================

// TestResetDetection_CounterToZero counter 归零到 0：delta=0 不上报（因为当前值和上次值都是0时用户确实没有新流量）
// 注意: upload=0 && download=0 时 calculateDeltas 会正确过滤掉该记录(0 增量不需要上报)
func TestResetDetection_CounterToZero(t *testing.T) {
	prevUp := map[string]int64{testUUID1: 10 * 1024 * 1024 * 1024} // 10 GB
	prevDown := map[string]int64{testUUID1: 5 * 1024 * 1024 * 1024}

	currentUp := map[string]int64{testUUID1: 0}   // Xray 重启, upload counter = 0
	currentDown := map[string]int64{testUUID1: 0} // download counter = 0

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	// delta = 0-0 = 0, upload==0 && download==0 → 不上报(正确行为)
	if len(deltas) != 0 {
		t.Fatalf("重置到0且无新流量: 期望0条(0增量不上报), 实际%d条", len(deltas))
	}
	t.Log("counter 归零 + 无新流量: 0 条增量(正确过滤) ✓")
}

// TestResetDetection_VariousResetMagnitudes 多种重置幅度：验证无论 prev 多大，重置后都取当前值
func TestResetDetection_VariousResetMagnitudes(t *testing.T) {
	tests := []struct {
		name     string
		prevUp   int64
		curUp    int64
		wantUp   int64
		prevDown int64
		curDown  int64
		wantDown int64
	}{
		{
			name: "TinyReset", prevUp: 10000, curUp: 100, wantUp: 100,
			prevDown: 5000, curDown: 50, wantDown: 50,
		},
		{
			name: "MediumReset", prevUp: 500_000_000, curUp: 1024, wantUp: 1024,
			prevDown: 200_000_000, curDown: 512, wantDown: 512,
		},
		{
			name: "LargeReset_10GB", prevUp: 10 * 1024 * 1024 * 1024, curUp: 500, wantUp: 500,
			prevDown: 5 * 1024 * 1024 * 1024, curDown: 200, wantDown: 200,
		},
		{
			name: "MaxReset", prevUp: 1_000_000_000_000, curUp: 1, wantUp: 1,
			prevDown: 500_000_000_000, curDown: 1, wantDown: 1,
		},
		{
			name: "ZeroToNonZero", prevUp: 0, curUp: 500, wantUp: 500, // 新用户首次有流量 (非重置)
			prevDown: 0, curDown: 200, wantDown: 200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prevUp := map[string]int64{testUUID1: tt.prevUp}
			prevDown := map[string]int64{testUUID1: tt.prevDown}
			currentUp := map[string]int64{testUUID1: tt.curUp}
			currentDown := map[string]int64{testUUID1: tt.curDown}

			deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

			// 新用户(zeroToNonZero) 和重置场景都应返回增量
			if len(deltas) != 1 {
				t.Fatalf("期望 1 条增量, 实际 %d 条", len(deltas))
			}

			// 任何情况下 delta 都不应 < 0
			if deltas[0].Upload < 0 {
				t.Fatalf("upload=%d 为负数！", deltas[0].Upload)
			}
			if deltas[0].Download < 0 {
				t.Fatalf("download=%d 为负数！", deltas[0].Download)
			}

			// 验证具体值
			if deltas[0].Upload != tt.wantUp {
				t.Fatalf("upload: 期望%d, 实际%d", tt.wantUp, deltas[0].Upload)
			}
			if deltas[0].Download != tt.wantDown {
				t.Fatalf("download: 期望%d, 实际%d", tt.wantDown, deltas[0].Download)
			}
		})
	}
}

// TestResetDetection_NoFalsePositive 确认正常增量不会触发重置检测（无误报）
func TestResetDetection_NoFalsePositive(t *testing.T) {
	baseUp := int64(1_000_000)
	baseDown := int64(500_000)

	// 各种正常增量都应为 positive delta，不走重置分支
	tests := []struct {
		name    string
		curUp   int64
		curDown int64
	}{
		{"SmallIncrement", 1_001_000, 500_500},
		{"MediumIncrement", 2_000_000, 1_000_000},
		{"BigJump", 100_000_000, 50_000_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 每个子测试独立创建 prev maps，避免相互污染
			prevUp := map[string]int64{testUUID1: baseUp}
			prevDown := map[string]int64{testUUID1: baseDown}
			currentUp := map[string]int64{testUUID1: tt.curUp}
			currentDown := map[string]int64{testUUID1: tt.curDown}

			deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)
			if len(deltas) != 1 {
				t.Fatalf("期望1条, 实际%d条", len(deltas))
			}
			expectedUp := tt.curUp - baseUp
			expectedDown := tt.curDown - baseDown

			if deltas[0].Upload != expectedUp {
				t.Fatalf("upload: 期望%d (cur-prev), 实际%d", expectedUp, deltas[0].Upload)
			}
			if deltas[0].Download != expectedDown {
				t.Fatalf("download: 期望%d (cur-prev), 实际%d", expectedDown, deltas[0].Download)
			}
		})
	}
}

// TestResetDetection_ResetThenNormal 重置后多轮验证：确保重置后的基准正确建立
func TestResetDetection_ResetThenNormal(t *testing.T) {
	prevUp := make(map[string]int64)
	prevDown := make(map[string]int64)
	uuid := testUUID1

	// 第1轮：建基线 10GB
	currentUp := map[string]int64{uuid: 10 * 1024 * 1024 * 1024}
	currentDown := map[string]int64{uuid: 5 * 1024 * 1024 * 1024}
	calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	// 第2轮：正常 +100MB
	currentUp2 := map[string]int64{uuid: 10*1024*1024*1024 + 100*1024*1024}
	currentDown2 := map[string]int64{uuid: 5*1024*1024*1024 + 50*1024*1024}
	calculateDeltas(prevUp, prevDown, currentUp2, currentDown2)

	// 第3轮：Xray 重启，counter → ~0
	currentUp3 := map[string]int64{uuid: 500}
	currentDown3 := map[string]int64{uuid: 200}
	d3 := calculateDeltas(prevUp, prevDown, currentUp3, currentDown3)

	// 第4轮：重置后正常增量 +10KB
	currentUp4 := map[string]int64{uuid: 500 + 10240}
	currentDown4 := map[string]int64{uuid: 200 + 5120}
	d4 := calculateDeltas(prevUp, prevDown, currentUp4, currentDown4)

	// 第5轮：再次正常增量 +10KB
	currentUp5 := map[string]int64{uuid: 500 + 20480}
	currentDown5 := map[string]int64{uuid: 200 + 10240}
	d5 := calculateDeltas(prevUp, prevDown, currentUp5, currentDown5)

	// 验证
	if d3[0].Upload != 500 || d3[0].Download != 200 {
		t.Fatalf("第3轮(重置): upload=%d(期望500), download=%d(期望200)", d3[0].Upload, d3[0].Download)
	}
	if d4[0].Upload != 10240 || d4[0].Download != 5120 {
		t.Fatalf("第4轮(恢复1): upload=%d(期望10240), download=%d(期望5120)", d4[0].Upload, d4[0].Download)
	}
	if d5[0].Upload != 10240 || d5[0].Download != 5120 {
		t.Fatalf("第5轮(恢复2): upload=%d(期望10240), download=%d(期望5120)", d5[0].Upload, d5[0].Download)
	}

	// 任何轮次都不应出现负数
	for i, d := range [][]TrafficDelta{{}, {}, d3, d4, d5} {
		if len(d) == 0 {
			continue
		}
		if d[0].Upload < 0 {
			t.Fatalf("第%d轮 upload 出现负数: %d", i, d[0].Upload)
		}
		if d[0].Download < 0 {
			t.Fatalf("第%d轮 download 出现负数: %d", i, d[0].Download)
		}
	}

	t.Log("重置 → 恢复1 → 恢复2 全链路验证通过，无负数")
}

// TestResetDetection_UploadOnly 仅上行重置，下行正常
// Xray stats 中 upload 和 download 是独立 counter，可能只有一个方向发生重置
func TestResetDetection_UploadOnly(t *testing.T) {
	prevUp := map[string]int64{testUUID1: 100_000_000}
	prevDown := map[string]int64{testUUID1: 50_000_000}

	// upload counter 重置, download 正常递增
	currentUp := map[string]int64{testUUID1: 100}
	currentDown := map[string]int64{testUUID1: 55_000_000}

	deltas := calculateDeltas(prevUp, prevDown, currentUp, currentDown)

	if len(deltas) != 1 {
		t.Fatalf("期望1条, 实际%d条", len(deltas))
	}
	// upload 重置: 取当前值
	if deltas[0].Upload != 100 {
		t.Fatalf("upload(重置侧): 期望100, 实际%d", deltas[0].Upload)
	}
	// download 正常 increment
	if deltas[0].Download != 5_000_000 {
		t.Fatalf("download(正常侧): 期望5000000, 实际%d", deltas[0].Download)
	}
	if deltas[0].Upload < 0 || deltas[0].Download < 0 {
		t.Fatalf("不期望出现负数: u=%d d=%d", deltas[0].Upload, deltas[0].Download)
	}
	t.Log("仅上行重置: upload=100(取当前值), download=5M(正常增量) ✓")
}

// =============================================================================
// QueryDelta + Mock 集成测试
// =============================================================================

// TestQueryDelta_WithMockStatsQuery 通过注入 mock statsQueryFn 验证 QueryDelta 完整流程
func TestQueryDelta_WithMockStatsQuery(t *testing.T) {
	ut := &UserTraffic{
		prevUp:   make(map[string]int64),
		prevDown: make(map[string]int64),
	}

	// Mock: 模拟 Xray Stats 返回 1 个用户
	ut.statsQueryFn = func() (map[string]int64, map[string]int64, error) {
		return map[string]int64{testUUID1: 5000}, map[string]int64{testUUID1: 2000}, nil
	}

	// 第 1 次查询 → prev 为空 → delta = cur - 0 = cur → 有增量(正确: 用户确实用了流量)
	deltas, err := ut.QueryDelta()
	if err != nil {
		t.Fatalf("QueryDelta error: %v", err)
	}
	if len(deltas) != 1 {
		t.Fatalf("首次查询有流量: 期望1条增量, 实际%d条", len(deltas))
	}
	if deltas[0].Upload != 5000 || deltas[0].Download != 2000 {
		t.Fatalf("首次: upload=%d(期望5000), download=%d(期望2000)", deltas[0].Upload, deltas[0].Download)
	}

	// 第 2 次查询 → 有增量
	ut.statsQueryFn = func() (map[string]int64, map[string]int64, error) {
		return map[string]int64{testUUID1: 8000}, map[string]int64{testUUID1: 3000}, nil
	}
	deltas, err = ut.QueryDelta()
	if err != nil {
		t.Fatalf("QueryDelta error: %v", err)
	}
	if len(deltas) != 1 {
		t.Fatalf("第2次应返回 1 条增量，实际 %d 条", len(deltas))
	}
	if deltas[0].Upload != 3000 || deltas[0].Download != 1000 {
		t.Fatalf("upload=%d(期望3000), download=%d(期望1000)", deltas[0].Upload, deltas[0].Download)
	}
}

// TestQueryDelta_StatsQueryError 查询失败时返回 error
func TestQueryDelta_StatsQueryError(t *testing.T) {
	ut := &UserTraffic{
		prevUp:   make(map[string]int64),
		prevDown: make(map[string]int64),
	}
	errExpected := errors.New("xray not running")
	ut.statsQueryFn = func() (map[string]int64, map[string]int64, error) {
		return nil, nil, errExpected
	}

	_, err := ut.QueryDelta()
	if err == nil {
		t.Fatal("查询失败应返回 error")
	}
	if !strings.Contains(err.Error(), "xray not running") {
		t.Fatalf("error 应包含原始错误信息，实际: %v", err)
	}
}

// TestQueryDelta_ConcurrentSafety 并发安全性
func TestQueryDelta_ConcurrentSafety(t *testing.T) {
	ut := &UserTraffic{
		prevUp:   make(map[string]int64),
		prevDown: make(map[string]int64),
	}

	callCount := 0
	var mu sync.Mutex
	ut.statsQueryFn = func() (map[string]int64, map[string]int64, error) {
		mu.Lock()
		callCount++
		n := callCount
		mu.Unlock()
		return map[string]int64{testUUID1: int64(n * 1000)}, map[string]int64{testUUID1: int64(n * 500)}, nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ut.QueryDelta()
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("并发查询失败: %v", err)
	}

	// 不应 panic，基线应被更新
	ut.mu.Lock()
	totalUp := ut.prevUp[testUUID1]
	ut.mu.Unlock()
	if totalUp == 0 {
		t.Fatal("并发查询后基线不应为空")
	}
	t.Logf("10 并发查询完成，最终 prevUp=%d", totalUp)
}

// =============================================================================
// 辅助函数
// =============================================================================

// TestTrafficDelta_String 简单验证 TrafficDelta 结构体字段
func TestTrafficDelta_String(t *testing.T) {
	d := TrafficDelta{
		UserID:   testUUID1,
		Upload:   1024,
		Download: 512,
	}
	s := fmt.Sprintf("%s:%d/%d", d.UserID, d.Upload, d.Download)
	expected := testUUID1 + ":1024/512"
	if s != expected {
		t.Fatalf("期望 %s，实际 %s", expected, s)
	}
}
