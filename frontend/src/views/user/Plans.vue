<template>
  <div class="user-plans">
    <!-- 顶部说明 -->
    <div class="plans-header">
      <div>
        <h2 class="page-title">套餐商店</h2>
        <p class="page-desc">选择适合你的套餐，畅享全球高速网络</p>
      </div>
      <el-button @click="loadData" :loading="loading">
        <el-icon><Refresh /></el-icon>刷新
      </el-button>
    </div>

    <!-- 套餐卡片网格 -->
    <div v-loading="loading" class="plans-grid">
      <div
        v-for="plan in planList"
        :key="plan.id"
        class="plan-card np-card"
      >
        <!-- 套餐名 -->
        <div class="plan-name">{{ plan.name }}</div>
        <div v-if="plan.description" class="plan-desc">{{ plan.description }}</div>

        <!-- 价格 -->
        <div class="plan-price">
          <span class="price-symbol">¥</span>
          <span class="price-value">{{ (plan.price_cents / 100).toFixed(2) }}</span>
          <span class="price-unit">/ {{ plan.duration_days }}天</span>
        </div>

        <!-- 流量与设备数 -->
        <div class="plan-features">
          <div class="feature-item">
            <el-icon><DataLine /></el-icon>
            <span class="feature-label">流量</span>
            <span class="feature-value">{{ plan.traffic_limit ? formatTraffic(plan.traffic_limit) : '不限' }}</span>
          </div>
          <div class="feature-item">
            <el-icon><Calendar /></el-icon>
            <span class="feature-label">有效期</span>
            <span class="feature-value">{{ plan.duration_days }} 天</span>
          </div>
          <div class="feature-item">
            <el-icon><Connection /></el-icon>
            <span class="feature-label">设备数</span>
            <span class="feature-value">{{ plan.device_limit ? plan.device_limit : '不限' }}</span>
          </div>
          <div class="feature-item">
            <el-icon><Cpu /></el-icon>
            <span class="feature-label">节点数</span>
            <span class="feature-value">{{ plan.node_count || 0 }} 个</span>
          </div>
        </div>

        <!-- 优点列表 -->
        <div v-if="parseList(plan.features).length" class="pros-cons-list pros">
          <div v-for="(item, idx) in parseList(plan.features)" :key="'pf'+idx" class="pros-cons-item">
            <el-icon class="pros-icon"><CircleCheckFilled /></el-icon>
            <span>{{ item }}</span>
          </div>
        </div>

        <!-- 缺点列表 -->
        <div v-if="parseList(plan.limitations).length" class="pros-cons-list cons">
          <div v-for="(item, idx) in parseList(plan.limitations)" :key="'pl'+idx" class="pros-cons-item">
            <el-icon class="cons-icon"><CircleCloseFilled /></el-icon>
            <span>{{ item }}</span>
          </div>
        </div>

        <!-- 购买按钮 -->
        <el-button
          type="primary"
          class="buy-btn neon-btn"
          @click="openPurchase(plan)"
        >
          立即购买
        </el-button>
      </div>

      <el-empty v-if="!loading && !planList.length" description="暂无可用套餐" />
    </div>

    <!-- 购买对话框 -->
    <el-dialog
      v-model="purchaseVisible"
      title="确认订单"
      :width="'min(92vw, 480px)'"
      destroy-on-close
    >
      <div class="purchase-body">
        <!-- 套餐信息 -->
        <div class="purchase-plan">
          <div class="purchase-plan-name">{{ currentPlan?.name }}</div>
          <div class="purchase-plan-price">¥ {{ currentPlan ? (currentPlan.price_cents / 100).toFixed(2) : '0.00' }}</div>
        </div>

        <el-descriptions :column="1" border size="small" class="purchase-desc">
          <el-descriptions-item label="流量">{{ currentPlan ? (currentPlan.traffic_limit ? formatTraffic(currentPlan.traffic_limit) : '不限') : '-' }}</el-descriptions-item>
          <el-descriptions-item label="有效期">{{ currentPlan?.duration_days }} 天</el-descriptions-item>
          <el-descriptions-item label="设备数">{{ currentPlan?.device_limit ? currentPlan.device_limit : '不限' }}</el-descriptions-item>
        </el-descriptions>

        <!-- 支付方式 -->
        <div class="form-section-title">选择支付方式</div>
        <div class="pay-methods">
          <div
            v-for="m in payMethods"
            :key="m.value"
            class="pay-method-item"
            :class="{ active: payForm.paymentMethod === m.value }"
            @click="payForm.paymentMethod = m.value"
          >
            <el-icon :style="{ color: m.color }"><component :is="m.icon" /></el-icon>
            <span>{{ m.label }}</span>
          </div>
        </div>

        <!-- 优惠码 -->
        <div class="form-section-title">优惠码（可选）</div>
        <div class="coupon-row">
          <el-input
            v-model="payForm.couponCode"
            placeholder="输入优惠码享受折扣"
            clearable
            :disabled="couponApplied"
          />
          <el-button v-if="!couponApplied" @click="verifyCoupon" :loading="verifyingCoupon">验证</el-button>
          <el-button v-else type="danger" plain @click="removeCoupon">取消</el-button>
        </div>

        <!-- 金额明细 -->
        <div class="amount-detail">
          <div class="amount-row">
            <span>订单金额</span>
            <span>¥ {{ currentPlan ? (currentPlan.price_cents / 100).toFixed(2) : '0.00' }}</span>
          </div>
          <div v-if="couponApplied" class="amount-row discount">
            <span>优惠码已应用</span>
            <span class="coupon-applied-text">最终金额以订单为准</span>
          </div>
          <el-divider class="amount-divider" />
          <div class="amount-row total">
            <span>实付金额</span>
            <span class="total-price">¥ {{ currentPlan ? (currentPlan.price_cents / 100).toFixed(2) : '0.00' }}</span>
          </div>
        </div>
      </div>

      <template #footer>
        <el-button @click="purchaseVisible = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="createOrder">
          确认支付
        </el-button>
      </template>
    </el-dialog>

    <!-- 支付二维码对话框 -->
    <el-dialog
      v-model="payVisible"
      title="请使用扫码支付"
      :width="'min(92vw, 360px)'"
      :close-on-click-modal="false"
      destroy-on-close
      @close="onPayDialogClose"
    >
      <div class="qr-body">
        <div class="qr-wrap">
          <img v-if="qrDataUrl" :src="qrDataUrl" alt="支付二维码" class="qr-img" />
          <div v-else class="qr-loading">
            <el-icon class="is-loading"><Loading /></el-icon>
          </div>
        </div>
        <div class="qr-amount">¥ {{ lastOrderAmount.toFixed(2) }}</div>
        <div class="qr-tip">{{ payMethodName }} 扫码支付</div>
        <div v-if="lastOrderId" class="qr-order-no">订单号：{{ lastOrderNo }}</div>
        <div class="qr-actions">
          <el-button type="primary" @click="openPayUrl" :disabled="!lastPayUrl">
            打开支付页
          </el-button>
          <el-button @click="refreshOrderStatus()" :loading="checkingStatus">
            已支付，查询状态
          </el-button>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, DataLine, Calendar, Connection, Cpu, Loading, CircleCheckFilled, CircleCloseFilled } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { formatTraffic } from '@/utils/format'
import QRCode from 'qrcode'

// 套餐类型(与后端 model.Plan JSON tag 对齐，snake_case)
interface Plan {
  id: string
  name: string
  description?: string
  features?: string        // 优点(JSON 数组字符串)
  limitations?: string     // 缺点(JSON 数组字符串)
  traffic_limit: number    // 字节, 0=不限
  duration_days: number    // 天
  price_cents: number      // 分
  original_price_cents: number  // 原价(分, 划线价), 0=无
  device_limit: number     // 设备数, 0=不限
  node_count?: number      // 绑定节点数量
  sort_order: number
  is_enabled: boolean
}

const router = useRouter()
const loading = ref(false)
const planList = ref<Plan[]>([])

// 支付方式列表
const payMethods = [
  { value: 'epay_alipay', label: '支付宝', icon: 'Wallet', color: '#1677ff' },
  { value: 'epay_wechat', label: '微信支付', icon: 'ChatDotRound', color: '#07c160' },
  { value: 'epay_usdt', label: 'USDT', icon: 'Money', color: '#f5a623' },
]

// 解析 JSON 数组字符串为字符串数组(容错)
const parseList = (s?: string): string[] => {
  if (!s) return []
  try {
    const arr = JSON.parse(s)
    return Array.isArray(arr) ? arr.map((x: any) => String(x)).filter(Boolean) : []
  } catch {
    return s.split('\n').map(x => x.trim()).filter(Boolean)
  }
}

// 购买对话框
const purchaseVisible = ref(false)
const currentPlan = ref<Plan | null>(null)
const creating = ref(false)

// 优惠码验证结果(仅标记有效, 不在前端计算折扣金额, 以后端订单返回为准)
const couponApplied = ref(false)

const payForm = reactive({
  paymentMethod: 'epay_alipay',
  couponCode: '',
})

// 支付二维码对话框
const payVisible = ref(false)
const qrDataUrl = ref('')
const lastOrderId = ref('')
const lastOrderNo = ref('')
const lastOrderAmount = ref(0)
const lastPayUrl = ref('')
const checkingStatus = ref(false)
let statusTimer: number | null = null

const payMethodName = computed(() => {
  const m = payMethods.find((x) => x.value === payForm.paymentMethod)
  return m ? m.label : '扫码'
})

// 打开购买对话框
const openPurchase = (plan: Plan) => {
  currentPlan.value = plan
  payForm.paymentMethod = 'epay_alipay'
  payForm.couponCode = ''
  couponApplied.value = false
  purchaseVisible.value = true
}

// 验证优惠码(仅校验有效性, 不显示折扣金额, 避免与后端实际扣减产生竞态)
const verifyingCoupon = ref(false)
const verifyCoupon = async () => {
  if (!payForm.couponCode.trim()) {
    ElMessage.warning('请输入优惠码')
    return
  }
  verifyingCoupon.value = true
  try {
    const res: any = await request.post('/api/v1/user/coupon/validate', {
      code: payForm.couponCode,
      amount_cents: currentPlan.value ? currentPlan.value.price_cents : 0,
    })
    const data = res?.data || res
    if (data && data.valid) {
      couponApplied.value = true
      ElMessage.success('优惠码已应用，最终金额以订单为准')
    } else {
      ElMessage.error(data?.message || '优惠码无效')
    }
  } catch {
    // 错误提示已由 request 拦截器统一处理
  } finally {
    verifyingCoupon.value = false
  }
}

// 取消优惠码
const removeCoupon = () => {
  couponApplied.value = false
  payForm.couponCode = ''
}

// 创建订单
const createOrder = async () => {
  if (!currentPlan.value) return
  creating.value = true
  try {
    const payload = {
      plan_id: currentPlan.value.id,
      payment_method: payForm.paymentMethod,
      coupon_code: couponApplied.value ? payForm.couponCode : '',
    }
    const res: any = await request.post('/api/v1/user/orders', payload)
    const data = res?.data || res
    const orderId = data?.id || data?.order_id || ''
    const orderNo = data?.order_no || data?.orderNo || ''
    if (!orderId) {
      ElMessage.error('订单创建失败')
      return
    }
    // 0 元订单(100% 折扣): 后端直接标记已支付, 跳过支付网关
    if (data?.status === 'paid') {
      ElMessage.success('订单已支付，套餐已激活')
      purchaseVisible.value = false
      router.push('/user/orders')
      return
    }
    ElMessage.success('订单已创建，正在生成支付链接')
    purchaseVisible.value = false
    await requestPay(orderId, orderNo)
  } catch {
    ElMessage.error('订单创建失败，请稍后重试')
  } finally {
    creating.value = false
  }
}

// 请求支付链接
const requestPay = async (orderId: string, orderNo: string) => {
  try {
    const res: any = await request.post(`/api/v1/user/orders/${orderId}/pay`)
    const data = res?.data || res
    // 0 元订单(100% 折扣)已是已支付状态, 直接显示成功
    if (data?.status === 'paid') {
      ElMessage.success('订单已支付，套餐已激活')
      router.push('/user/orders')
      return
    }
    const payUrl = data?.pay_url || ''
    lastOrderId.value = orderId
    lastOrderNo.value = orderNo
    // 金额以后端返回为准, 避免前端折扣计算与后端不一致
    lastOrderAmount.value = data?.amount_cents ? data.amount_cents / 100 : (currentPlan.value ? currentPlan.value.price_cents / 100 : 0)
    lastPayUrl.value = payUrl
    // 生成二维码：优先使用 pay_url，否则用订单号
    await generateQR(payUrl || orderNo)
    payVisible.value = true
    startStatusPolling()
  } catch {
    ElMessage.error('获取支付链接失败，请前往订单列表重试')
    router.push('/user/orders')
  }
}

// 生成二维码
const generateQR = async (text: string) => {
  try {
    qrDataUrl.value = await QRCode.toDataURL(text, {
      width: 240,
      margin: 1,
      color: { dark: '#0a0e17', light: '#ffffff' },
    })
  } catch {
    qrDataUrl.value = ''
  }
}

// 打开支付页（用于 PC 端跳转）
const openPayUrl = () => {
  if (!lastPayUrl.value) {
    ElMessage.warning('暂无可跳转的支付页')
    return
  }
  // 修复 P1-FE1: 加 'noopener,noreferrer' 防止支付页通过 window.opener 反向操作原页面
  window.open(lastPayUrl.value, '_blank', 'noopener,noreferrer')
}

// 开始轮询订单状态
const startStatusPolling = () => {
  stopStatusPolling()
  statusTimer = window.setInterval(async () => {
    await refreshOrderStatus(true)
  }, 3000)
}

// 停止轮询
const stopStatusPolling = () => {
  if (statusTimer) {
    clearInterval(statusTimer)
    statusTimer = null
  }
}

// 刷新订单状态
const refreshOrderStatus = async (silent = false) => {
  if (!lastOrderId.value) return
  checkingStatus.value = true
  try {
    const res: any = await request.get(`/api/v1/user/orders/${lastOrderId.value}`)
    const data = res?.data || res
    const status = data?.status
    if (status === 'paid') {
      stopStatusPolling()
      payVisible.value = false
      ElMessage.success('支付成功，套餐已激活')
      router.push('/user/orders')
    } else if (status === 'expired' || status === 'cancelled') {
      stopStatusPolling()
      if (!silent) ElMessage.warning('订单已失效')
    } else if (!silent) {
      ElMessage.info('订单尚未支付，请扫码完成支付')
    }
  } catch {
    if (!silent) ElMessage.info('订单尚未支付，请扫码完成支付')
  } finally {
    checkingStatus.value = false
  }
}

// 关闭支付对话框
const onPayDialogClose = () => {
  stopStatusPolling()
}

// 加载套餐列表(用户端接口，仅返回已启用套餐)
const loadData = async () => {
  loading.value = true
  try {
    const res: any = await request.get('/api/v1/plans')
    // 后端返回 { code:0, data:{ list:[...], total:n } }
    // request 拦截器已解包 response.data，所以 res = { code:0, data:{ list:[...], total:n } }
    const arr = res?.data?.list || (Array.isArray(res?.data) ? res.data : []) || []
    // 防御性过滤: 即便后端 ListEnabled 没过滤掉试用套餐, 前端也过滤掉
    // 防止后端 is_trial 字段未正确标记时试用套餐泄露到商店
    const filtered = Array.isArray(arr)
      ? arr.filter((p: any) => !p.is_trial && (p.price_cents === undefined || p.price_cents > 0))
      : []
    planList.value = filtered.sort((a: Plan, b: Plan) => a.sort_order - b.sort_order)
  } catch {
    planList.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadData()
})

onBeforeUnmount(() => {
  stopStatusPolling()
})
</script>

<style scoped>
.plans-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 24px;
  flex-wrap: wrap;
  gap: 12px;
}
.page-title { margin: 0; font-size: 20px; color: var(--np-text); }
.page-desc { margin: 6px 0 0; font-size: 13px; color: var(--np-text-secondary); }

/* 套餐卡片网格 */
.plans-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 20px;
}
.plan-card {
  position: relative;
  padding: 24px;
  display: flex;
  flex-direction: column;
  transition: all 0.3s ease;
}
.plan-card:hover {
  transform: translateY(-4px);
  border-color: var(--np-primary-dim);
}
.plan-card.recommended {
  border-color: var(--np-primary);
  box-shadow: 0 0 24px var(--np-primary-dim), inset 0 0 12px var(--np-primary-dim);
  background: linear-gradient(160deg, var(--np-card), rgba(0, 245, 212, 0.04));
}
.plan-card.recommended:hover {
  box-shadow: 0 0 32px var(--np-primary-glow), inset 0 0 14px var(--np-primary-dim);
}

.recommend-badge {
  position: absolute;
  top: 0;
  right: 20px;
  background: var(--np-primary);
  color: var(--np-bg);
  font-size: 11px;
  font-weight: 700;
  padding: 3px 12px;
  border-radius: 0 0 6px 6px;
  letter-spacing: 1px;
}

.plan-name {
  font-size: 18px;
  font-weight: 700;
  color: var(--np-text);
}
.plan-desc {
  margin-top: 6px;
  font-size: 12px;
  color: var(--np-text-muted);
  min-height: 18px;
}

/* 价格 */
.plan-price {
  margin: 18px 0;
  display: flex;
  align-items: baseline;
  gap: 2px;
}
.price-symbol { font-size: 16px; color: var(--np-primary); font-weight: 600; }
.price-value {
  font-size: 36px;
  font-weight: 800;
  color: var(--np-primary);
  line-height: 1;
  text-shadow: 0 0 12px var(--np-primary-dim);
}
.price-unit { font-size: 12px; color: var(--np-text-muted); margin-left: 4px; }

/* 特性列表 */
.plan-features {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-bottom: 20px;
  flex: 1;
}
.feature-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--np-text-secondary);
}
.feature-item .el-icon { color: var(--np-primary); }
.feature-label { min-width: 56px; color: var(--np-text-muted); }
.feature-value { color: var(--np-text); font-weight: 500; }

/* 购买按钮 */
.buy-btn {
  width: 100%;
}

/* 优缺点列表 */
.pros-cons-list {
  margin-bottom: 16px;
  padding: 10px 12px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.8;
}
.pros-cons-list.pros {
  background: rgba(0, 200, 120, 0.06);
  border: 1px solid rgba(0, 200, 120, 0.2);
}
.pros-cons-list.cons {
  background: rgba(245, 108, 108, 0.06);
  border: 1px solid rgba(245, 108, 108, 0.2);
}
.pros-cons-item {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--np-text-secondary);
}
.pros-icon { color: #00c878; flex-shrink: 0; }
.cons-icon { color: #f56c6c; flex-shrink: 0; }
.neon-btn {
  background: transparent !important;
  border: 1px solid var(--np-primary) !important;
  color: var(--np-primary) !important;
  box-shadow: 0 0 10px var(--np-primary-dim);
}
.neon-btn:hover {
  background: var(--np-primary-dim) !important;
  box-shadow: 0 0 20px var(--np-primary-glow);
}

/* 购买对话框 */
.purchase-body { padding: 0 4px; }
.purchase-plan {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  background: var(--np-bg-soft);
  border: 1px solid var(--np-border);
  border-radius: 8px;
  margin-bottom: 16px;
}
.purchase-plan-name { font-size: 16px; font-weight: 600; color: var(--np-text); }
.purchase-plan-price { font-size: 18px; color: var(--np-primary); font-weight: 700; }
.purchase-desc { margin-bottom: 16px; }

.form-section-title {
  font-size: 13px;
  color: var(--np-text-secondary);
  margin: 16px 0 8px;
}

/* 支付方式 */
.pay-methods {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}
.pay-method-item {
  flex: 1;
  min-width: 100px;
  display: flex;
  align-items: center;
  gap: 6px;
  justify-content: center;
  padding: 10px;
  border: 1px solid var(--np-border);
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.2s;
  font-size: 13px;
  color: var(--np-text-secondary);
  background: var(--np-bg-soft);
}
.pay-method-item:hover {
  border-color: var(--np-primary-dim);
  color: var(--np-text);
}
.pay-method-item.active {
  border-color: var(--np-primary);
  color: var(--np-primary);
  box-shadow: 0 0 12px var(--np-primary-dim);
  background: var(--np-primary-dim);
}
.pay-method-item .el-icon { font-size: 16px; }

/* 优惠码 */
.coupon-row {
  display: flex;
  gap: 10px;
}

/* 金额明细 */
.amount-detail {
  margin-top: 20px;
  padding: 12px 16px;
  background: var(--np-bg-soft);
  border: 1px solid var(--np-border);
  border-radius: 8px;
}
.amount-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  color: var(--np-text-secondary);
  padding: 4px 0;
}
.amount-row.discount { color: var(--np-primary); }
.coupon-applied-text { font-size: 12px; color: var(--np-text-secondary); }
.amount-divider { margin: 8px 0; }
.amount-row.total {
  font-size: 15px;
  color: var(--np-text);
  font-weight: 600;
}
.total-price {
  color: var(--np-primary);
  font-size: 20px;
  font-weight: 800;
  text-shadow: 0 0 8px var(--np-primary-dim);
}

/* 二维码对话框 */
.qr-body {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 8px 0;
}
.qr-wrap {
  width: 240px;
  height: 240px;
  background: #fff;
  border-radius: 8px;
  padding: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.qr-img { width: 100%; height: 100%; display: block; }
.qr-loading { font-size: 32px; color: var(--np-text-muted); }
.qr-amount {
  font-size: 24px;
  font-weight: 800;
  color: var(--np-primary);
  text-shadow: 0 0 8px var(--np-primary-dim);
}
.qr-tip { font-size: 14px; color: var(--np-text-secondary); }
.qr-order-no { font-size: 12px; color: var(--np-text-muted); }
.qr-actions {
  display: flex;
  gap: 10px;
  margin-top: 8px;
}
</style>
