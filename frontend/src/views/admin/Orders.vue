<template>
  <div class="admin-orders">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">订单管理</h2>
          <p class="page-desc">查看全部订单，支持按状态、用户、日期筛选</p>
        </div>
        <div class="header-actions">
          <el-button @click="loadData" :loading="loading">
            <el-icon><Refresh /></el-icon>刷新
          </el-button>
        </div>
      </div>

      <!-- 筛选区 -->
      <div class="filter-bar">
        <el-select v-model="filter.status" placeholder="订单状态" clearable style="width: 130px">
          <el-option label="待支付" value="pending" />
          <el-option label="已支付" value="paid" />
          <el-option label="已取消" value="cancelled" />
          <el-option label="已过期" value="expired" />
          <el-option label="已退款" value="refunded" />
        </el-select>
        <el-input v-model="filter.keyword" placeholder="订单号/用户名" clearable style="width: 220px" />
        <el-date-picker
          v-model="filter.dateRange"
          type="daterange"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          value-format="YYYY-MM-DD"
          style="width: 260px"
        />
        <el-button type="primary" @click="loadData">查询</el-button>
        <el-button @click="resetFilter">重置</el-button>

        <!-- 统计 -->
        <div class="stat-summary">
          <span class="stat-item">待支付 <b>{{ stats.pending_count }}</b></span>
          <span class="stat-item">已支付 <b>{{ stats.paid_count }}</b></span>
          <span class="stat-item">总收入 <b class="primary">¥ {{ totalIncomeYuan.toFixed(2) }}</b></span>
        </div>
      </div>

      <el-table :data="filteredList" stripe v-loading="loading">
        <el-table-column prop="orderNo" label="订单号" min-width="170" />
        <el-table-column prop="username" label="用户" min-width="110" />
        <el-table-column prop="planName" label="套餐" min-width="110" />
        <el-table-column label="金额" width="120">
          <template #default="{ row }">
            <span class="amount-text">¥ {{ row.finalAmount.toFixed(2) }}</span>
            <span v-if="row.discount > 0" class="amount-origin">¥ {{ row.amount.toFixed(2) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="statusTagType(row.status)" effect="dark">
              {{ statusText(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="支付方式" width="110">
          <template #default="{ row }">
            <span v-if="row.paymentMethod">{{ payMethodText(row.paymentMethod) }}</span>
            <span v-else class="text-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="160">
          <template #default="{ row }">{{ formatTime(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="220" fixed="right">
          <template #default="{ row }">
            <el-button size="small" link @click="viewDetail(row)">详情</el-button>
            <el-button v-if="row.status === 'pending'" size="small" link type="success" @click="markPaid(row)">
              标记已付
            </el-button>
            <el-button v-if="row.status === 'paid'" size="small" link type="warning" @click="refund(row)">
              退款
            </el-button>
            <el-button v-if="row.status === 'pending'" size="small" link type="danger" @click="cancelOrder(row)">
              取消
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 订单详情对话框 -->
    <el-dialog v-model="detailVisible" title="订单详情" width="560px">
      <el-descriptions v-if="currentOrder" :column="1" border>
        <el-descriptions-item label="订单号">{{ currentOrder.orderNo }}</el-descriptions-item>
        <el-descriptions-item label="用户">{{ currentOrder.username }}</el-descriptions-item>
        <el-descriptions-item label="套餐">{{ currentOrder.planName }}</el-descriptions-item>
        <el-descriptions-item label="订单金额">¥ {{ currentOrder.amount.toFixed(2) }}</el-descriptions-item>
        <el-descriptions-item v-if="currentOrder.discount > 0" label="优惠减免">
          - ¥ {{ currentOrder.discount.toFixed(2) }}
          <span v-if="currentOrder.couponCode" class="text-muted">（{{ currentOrder.couponCode }}）</span>
        </el-descriptions-item>
        <el-descriptions-item label="实付金额">
          <span class="amount-final">¥ {{ currentOrder.finalAmount.toFixed(2) }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="订单状态">
          <el-tag size="small" :type="statusTagType(currentOrder.status)" effect="dark">
            {{ statusText(currentOrder.status) }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="支付方式">
          {{ currentOrder.paymentMethod ? payMethodText(currentOrder.paymentMethod) : '-' }}
        </el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(currentOrder.createdAt) }}</el-descriptions-item>
        <el-descriptions-item v-if="currentOrder.paidAt" label="支付时间">{{ formatTime(currentOrder.paidAt) }}</el-descriptions-item>
        <el-descriptions-item v-if="currentOrder.expiredAt" label="过期时间">{{ formatTime(currentOrder.expiredAt) }}</el-descriptions-item>
      </el-descriptions>
      <template #footer>
        <el-button v-if="currentOrder && currentOrder.status === 'pending'" type="success" @click="markPaid(currentOrder)">
          手动标记已支付
        </el-button>
        <el-button @click="detailVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

type OrderStatus = 'pending' | 'paid' | 'cancelled' | 'expired' | 'refunded'
type PayMethod = 'epay_alipay' | 'epay_wechat' | ''

interface Order {
  id: string
  orderNo: string
  userId: string
  username: string
  planId: string
  planName: string
  amount: number
  discount: number
  finalAmount: number
  status: OrderStatus
  paymentMethod: PayMethod
  couponCode?: string
  createdAt: string
  paidAt?: string
  expiredAt?: string
}

const loading = ref(false)
const list = ref<Order[]>([])
const detailVisible = ref(false)
const currentOrder = ref<Order | null>(null)

// 筛选条件
const filter = reactive({
  status: '' as OrderStatus | '',
  keyword: '',
  dateRange: [] as string[],
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
const payMethodText = (m: PayMethod): string => {
  const map: Record<string, string> = {
    epay_alipay: '支付宝', epay_wechat: '微信支付',
  }
  return map[m] || m
}

// 过滤后的列表
const filteredList = computed(() => {
  return list.value.filter((o) => {
    if (filter.status && o.status !== filter.status) return false
    if (filter.keyword) {
      const k = filter.keyword.toLowerCase()
      if (!o.orderNo.toLowerCase().includes(k) && !o.username.toLowerCase().includes(k)) return false
    }
    if (filter.dateRange && filter.dateRange.length === 2) {
      const start = filter.dateRange[0] + ' 00:00:00'
      const end = filter.dateRange[1] + ' 23:59:59'
      if (o.createdAt < start || o.createdAt > end) return false
    }
    return true
  })
})

// 统计信息(从后端聚合接口获取, 避免基于当前页数据计算导致偏差)
interface OrderStats {
  pending_count: number
  paid_count: number
  cancelled_count: number
  expired_count: number
  refunded_count: number
  total_income_cents: number // 已支付总金额(分)
}
const stats = ref<OrderStats>({
  pending_count: 0,
  paid_count: 0,
  cancelled_count: 0,
  expired_count: 0,
  refunded_count: 0,
  total_income_cents: 0,
})

// 总收入(元) = 分 / 100, 用 computed 保证 total_income_cents 变化时自动更新
const totalIncomeYuan = computed(() => stats.value.total_income_cents / 100)

// 加载统计(失败不阻塞列表展示, 静默处理)
const loadStats = async () => {
  try {
    const res: any = await request.get('/api/v1/admin/orders/stats', { silent: true })
    const data = res?.data || res
    if (data && typeof data === 'object') {
      stats.value = {
        pending_count: Number(data.pending_count) || 0,
        paid_count: Number(data.paid_count) || 0,
        cancelled_count: Number(data.cancelled_count) || 0,
        expired_count: Number(data.expired_count) || 0,
        refunded_count: Number(data.refunded_count) || 0,
        total_income_cents: Number(data.total_income_cents) || 0,
      }
    }
  } catch {
    // 统计加载失败不影响列表展示
  }
}

// 重置筛选
const resetFilter = () => {
  filter.status = ''
  filter.keyword = ''
  filter.dateRange = []
}

// 查看详情
const viewDetail = (row: any) => {
  currentOrder.value = row
  detailVisible.value = true
}

// 手动标记已支付（用于线下收款）
const markPaid = (row: any) => {
  ElMessageBox.confirm(
    `确认将订单「${row.orderNo}」标记为已支付吗？\n此操作通常用于线下收款确认。`,
    '手动标记已支付',
    { type: 'warning', confirmButtonText: '确认标记', cancelButtonText: '取消' },
  ).then(async () => {
    try {
      await request.post(`/api/v1/admin/orders/${row.id}/mark-paid`)
    } catch { /* */ }
    row.status = 'paid'
    row.paidAt = new Date().toISOString().replace('T', ' ').slice(0, 19)
    ElMessage.success('订单已标记为已支付')
    if (currentOrder.value && currentOrder.value.id === row.id) {
      currentOrder.value = { ...row }
    }
  }).catch(() => {})
}

// 退款
const refund = (row: any) => {
  ElMessageBox.confirm(
    `确认对订单「${row.orderNo}」进行退款吗？退款后订单状态将变为已退款。`,
    '退款操作',
    { type: 'warning', confirmButtonText: '确认退款', cancelButtonText: '取消' },
  ).then(async () => {
    try { await request.post(`/api/v1/admin/orders/${row.id}/refund`) } catch { /* */ }
    row.status = 'refunded'
    ElMessage.success('订单已退款')
  }).catch(() => {})
}

// 取消订单
const cancelOrder = (row: any) => {
  ElMessageBox.confirm(`确认取消订单「${row.orderNo}」吗？`, '提示', {
    type: 'warning', confirmButtonText: '取消订单', cancelButtonText: '保留',
  }).then(async () => {
    try { await request.post(`/api/v1/admin/orders/${row.id}/cancel`) } catch { /* */ }
    row.status = 'cancelled'
    ElMessage.success('订单已取消')
  }).catch(() => {})
}

// 加载数据
const loadData = async () => {
  loading.value = true
  try {
    const res: any = await request.get('/api/v1/admin/orders', {
      params: {
        status: filter.status || undefined,
        keyword: filter.keyword || undefined,
        start_date: filter.dateRange?.[0],
        end_date: filter.dateRange?.[1],
      },
    })
    const arr = res?.data || res
    if (Array.isArray(arr)) {
      list.value = arr
    } else {
      list.value = []
      ElMessage.error('订单数据格式异常')
    }
  } catch {
    list.value = []
    ElMessage.error('加载订单列表失败，请稍后重试')
  } finally {
    loading.value = false
  }
  // 并行加载统计(不阻塞列表, 失败静默)
  loadStats()
}

onMounted(() => {
  loadData()
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

.filter-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 20px;
  padding: 16px;
  background: var(--np-bg-soft);
  border: 1px solid var(--np-border);
  border-radius: 8px;
}
.stat-summary {
  margin-left: auto;
  display: flex;
  gap: 20px;
  font-size: 13px;
  color: var(--np-text-secondary);
}
.stat-item b { color: var(--np-text); margin-left: 4px; font-weight: 700; }
.stat-item b.primary { color: var(--np-primary); }

.amount-text { color: var(--np-primary); font-weight: 600; }
.amount-origin {
  display: block;
  font-size: 11px;
  color: var(--np-text-muted);
  text-decoration: line-through;
}
.text-muted { color: var(--np-text-muted); }
.amount-final { color: var(--np-primary); font-weight: 700; font-size: 16px; }
</style>
