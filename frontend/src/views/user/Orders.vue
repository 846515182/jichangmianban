<template>
  <div class="user-orders">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">我的订单</h2>
          <p class="page-desc">查看订单状态，完成支付或取消订单</p>
        </div>
        <div class="header-actions">
          <el-select v-model="statusFilter" placeholder="全部状态" clearable style="width: 140px">
            <el-option label="待支付" value="pending" />
            <el-option label="已支付" value="paid" />
            <el-option label="已取消" value="cancelled" />
            <el-option label="已过期" value="expired" />
            <el-option label="已退款" value="refunded" />
          </el-select>
          <el-button @click="loadData" :loading="loading">
            <el-icon><Refresh /></el-icon>刷新
          </el-button>
          <el-button type="primary" @click="$router.push('/user/plans')">
            <el-icon><ShoppingCart /></el-icon>购买套餐
          </el-button>
        </div>
      </div>

      <el-table :data="filteredList" stripe v-loading="loading">
        <el-table-column prop="order_no" label="订单号" min-width="200" />
        <el-table-column prop="plan_name" label="套餐" min-width="120" />
        <el-table-column label="金额" width="120">
          <template #default="{ row }">
            <span class="amount-text">¥ {{ (row.amount_cents / 100).toFixed(2) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="statusTagType(row.status)" effect="dark">
              {{ statusText(row.status) }}
            </el-tag>
            <div v-if="row.status === 'pending'" class="countdown">
              {{ countdownText(row) }}
            </div>
          </template>
        </el-table-column>
        <el-table-column label="支付方式" width="110">
          <template #default="{ row }">
            <span v-if="row.payment_method">{{ payMethodText(row.payment_method) }}</span>
            <span v-else class="text-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="160">
          <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button v-if="row.status === 'pending'" size="small" link type="primary" @click="goPay(row)">
              去支付
            </el-button>
            <el-button v-if="row.status === 'pending'" size="small" link type="warning" @click="cancelOrder(row)">
              取消
            </el-button>
            <el-button size="small" link @click="viewDetail(row)">详情</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-empty v-if="!loading && !filteredList.length" description="暂无订单记录" />
    </div>

    <!-- 订单详情对话框 -->
    <el-dialog v-model="detailVisible" title="订单详情" width="520px">
      <el-descriptions v-if="currentOrder" :column="1" border>
        <el-descriptions-item label="订单号">{{ currentOrder.order_no }}</el-descriptions-item>
        <el-descriptions-item label="套餐">{{ currentOrder.plan_name }}</el-descriptions-item>
        <el-descriptions-item label="订单金额">
          <span class="amount-final">¥ {{ (currentOrder.amount_cents / 100).toFixed(2) }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="订单状态">
          <el-tag size="small" :type="statusTagType(currentOrder.status)" effect="dark">
            {{ statusText(currentOrder.status) }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="支付方式">
          {{ currentOrder.payment_method ? payMethodText(currentOrder.payment_method) : '-' }}
        </el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(currentOrder.created_at) }}</el-descriptions-item>
        <el-descriptions-item v-if="currentOrder.paid_at" label="支付时间">{{ formatTime(currentOrder.paid_at) }}</el-descriptions-item>
        <el-descriptions-item v-if="currentOrder.expired_at" label="过期时间">{{ formatTime(currentOrder.expired_at) }}</el-descriptions-item>
      </el-descriptions>
      <template #footer>
        <el-button v-if="currentOrder && currentOrder.status === 'pending'" type="primary" @click="goPay(currentOrder)">
          去支付
        </el-button>
        <el-button @click="detailVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, ShoppingCart } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

// 订单类型(与后端 model.Order JSON tag 对齐, snake_case)
type OrderStatus = 'pending' | 'paid' | 'cancelled' | 'expired' | 'refunded'
type PayMethod = 'epay_alipay' | 'epay_wechat' | ''

interface Order {
  id: string
  order_no: string
  user_id: string
  plan_id: string
  plan_name: string
  amount_cents: number
  status: OrderStatus
  payment_method: PayMethod
  trade_no?: string
  created_at: string
  paid_at?: string
  expired_at: string
}

const router = useRouter()
const loading = ref(false)
const list = ref<Order[]>([])
const statusFilter = ref<OrderStatus | ''>('')
const detailVisible = ref(false)
const currentOrder = ref<Order | null>(null)

// 状态过滤
const filteredList = computed(() => {
  if (!statusFilter.value) return list.value
  return list.value.filter((o) => o.status === statusFilter.value)
})

// 状态映射
type TagType = 'primary' | 'success' | 'info' | 'warning' | 'danger'
const statusText = (s: OrderStatus): string => {
  const map: Record<OrderStatus, string> = {
    pending: '待支付', paid: '已支付', cancelled: '已取消', expired: '已过期', refunded: '已退款',
  }
  return map[s] || s
}
const statusTagType = (s: OrderStatus): TagType => {
  const map: Record<OrderStatus, TagType> = {
    pending: 'warning', paid: 'success', cancelled: 'info', expired: 'danger', refunded: 'danger',
  }
  return map[s] || 'info'
}

// 支付方式映射
const payMethodText = (m: PayMethod): string => {
  const map: Record<string, string> = {
    epay_alipay: '支付宝', epay_wechat: '微信支付',
  }
  return map[m] || m
}

// 倒计时显示
const countdownText = (order: any): string => {
  if (!order.expired_at) return ''
  const remain = new Date(order.expired_at).getTime() - Date.now()
  if (remain <= 0) return '即将过期'
  const min = Math.floor(remain / 60000)
  const sec = Math.floor((remain % 60000) / 1000)
  return `${min}:${String(sec).padStart(2, '0')}`
}

// 倒计时刷新计时器
let tickTimer: number | null = null
const startTick = () => {
  tickTimer = window.setInterval(() => {
    // 触发响应式刷新倒计时显示
    list.value = [...list.value]
    // 自动把超时的待支付订单标记为过期
    list.value.forEach((o) => {
      if (o.status === 'pending' && o.expired_at && new Date(o.expired_at).getTime() < Date.now()) {
        o.status = 'expired'
      }
    })
  }, 1000)
}

// 去支付：调用支付接口，跳转 EPay
const goPay = async (order: any) => {
  try {
    const res: any = await request.post(`/api/v1/user/orders/${order.id}/pay`)
    const data = res?.data || res
    const payUrl = data?.pay_url
    if (payUrl) {
      ElMessage.success('正在跳转支付页面')
      // 修复 P1-FE1: 加 'noopener,noreferrer' 防止支付页通过 window.opener 反向操作原页面
      window.open(payUrl, '_blank', 'noopener,noreferrer')
    } else {
      ElMessage.warning('未获取到支付链接，请稍后重试')
    }
  } catch {
    ElMessage.error('发起支付失败，请稍后重试')
  }
}

// 取消订单
// 修复 P1-FE2: 旧版 try{}catch{} 后无条件 order.status='cancelled' + 弹成功,
// API 失败时用户看到"已取消"但后端订单仍为 pending, 刷新后状态回退造成困惑。
// 改为 API 成功后才更新本地状态 + 弹成功, 失败时拦截器已弹错误。
const cancelOrder = (order: any) => {
  ElMessageBox.confirm(`确定取消订单「${order.order_no}」吗？`, '提示', {
    type: 'warning', confirmButtonText: '取消订单', cancelButtonText: '保留',
  }).then(async () => {
    try {
      await request.post(`/api/v1/user/orders/${order.id}/cancel`)
      order.status = 'cancelled'
      ElMessage.success('订单已取消')
    } catch { /* 拦截器已提示 */ }
  }).catch(() => {})
}

// 查看详情
const viewDetail = (order: any) => {
  currentOrder.value = order
  detailVisible.value = true
}

// 加载数据
const loadData = async () => {
  loading.value = true
  try {
    const res: any = await request.get('/api/v1/user/orders')
    const arr = res?.data?.list || (Array.isArray(res?.data) ? res.data : []) || []
    list.value = Array.isArray(arr) ? arr : []
  } catch {
    list.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadData()
  startTick()
})

onBeforeUnmount(() => {
  if (tickTimer) clearInterval(tickTimer)
})
</script>

<style scoped>
.page-card { padding: 20px; }
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 20px;
  flex-wrap: wrap;
  gap: 12px;
}
.page-title { margin: 0; font-size: 18px; color: var(--np-text); }
.page-desc { margin: 6px 0 0; font-size: 13px; color: var(--np-text-secondary); }
.header-actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }

.amount-text { color: var(--np-primary); font-weight: 600; }
.countdown {
  margin-top: 4px;
  font-size: 11px;
  color: var(--np-warning);
}
.text-muted { color: var(--np-text-muted); }
.amount-final { color: var(--np-primary); font-weight: 700; font-size: 16px; }
</style>
