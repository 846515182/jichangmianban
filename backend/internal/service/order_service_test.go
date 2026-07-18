package service

import (
	"testing"

	"nexus-panel/internal/model"
)

// TestCalcCouponDiscount_Percent 验证百分比折扣计算
func TestCalcCouponDiscount_Percent(t *testing.T) {
	c := &model.Coupon{Type: model.CouponTypePercent, Value: 50, MinAmountCents: 0}
	d, err := calcCouponDiscount(c, 10000)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d != 5000 {
		t.Fatalf("expected 5000, got %d", d)
	}
}

// TestCalcCouponDiscount_PercentFullFree 验证 100% 折扣(全额免单)被允许
// 回归用例: 修复前 calcCouponDiscount 上限为 90, 创建允许 1-100 但使用被拒,
// 修复后统一为 1-100, 100% 优惠券应可用且折扣=全额
func TestCalcCouponDiscount_PercentFullFree(t *testing.T) {
	c := &model.Coupon{Type: model.CouponTypePercent, Value: 100, MinAmountCents: 0}
	d, err := calcCouponDiscount(c, 10000)
	if err != nil {
		t.Fatalf("100%% coupon should be allowed after fix: %v", err)
	}
	if d != 10000 {
		t.Fatalf("expected 10000, got %d", d)
	}
}

// TestCalcCouponDiscount_PercentBounds 验证百分比边界(0 与 101 非法)
func TestCalcCouponDiscount_PercentBounds(t *testing.T) {
	if _, err := calcCouponDiscount(&model.Coupon{Type: model.CouponTypePercent, Value: 0}, 10000); err == nil {
		t.Fatal("expected error for value=0")
	}
	if _, err := calcCouponDiscount(&model.Coupon{Type: model.CouponTypePercent, Value: 101}, 10000); err == nil {
		t.Fatal("expected error for value=101")
	}
}

// TestCalcCouponDiscount_Fixed 验证固定金额折扣及"超过订单金额时钳制为订单金额"
func TestCalcCouponDiscount_Fixed(t *testing.T) {
	c := &model.Coupon{Type: model.CouponTypeFixed, Value: 500, MinAmountCents: 0}
	d, err := calcCouponDiscount(c, 1000)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d != 500 {
		t.Fatalf("expected 500, got %d", d)
	}
	// fixed > amount => clamp to amount
	d2, _ := calcCouponDiscount(c, 200)
	if d2 != 200 {
		t.Fatalf("expected clamped 200, got %d", d2)
	}
}

// TestCalcCouponDiscount_MinAmountNotMet 验证未达最低消费
func TestCalcCouponDiscount_MinAmountNotMet(t *testing.T) {
	c := &model.Coupon{Type: model.CouponTypeFixed, Value: 100, MinAmountCents: 500}
	if _, err := calcCouponDiscount(c, 100); err == nil {
		t.Fatal("expected min amount error")
	}
}

// TestGenerateOrderNo 验证订单号格式与唯一性
func TestGenerateOrderNo(t *testing.T) {
	n1, err := generateOrderNo()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(n1) < 16 || n1[:2] != "NP" {
		t.Fatalf("unexpected order no: %s", n1)
	}
	n2, _ := generateOrderNo()
	if n1 == n2 {
		t.Fatal("order numbers should be unique")
	}
}
