<template>
  <div class="admin-orders">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">订单管理</h2>
          <p class="page-desc">查看全部订单，支持按状态、关键词、日期筛选</p>
        </div>
        <div class="header-actions">
          <el-button @click="loadData" :loading="loading">
            <el-icon><Refresh /></el-icon>刷新
          </el-button>
        </div>
      </div>

      <!-- 筛选区 -->
      <div class="filter-bar">
        <el-select v-model="filter.status" placeholder="订单状态" clearable style="width: 130px" @change="onFilterChange">
          <el-option label="待支付" value="pending" />
          <el-option label="已支付" value="paid" />
          <el-option label="已取消" value="cancelled" />
          <el-option label="已过期" value="expired" />
          <el-option label="已退款" value="refunded" />
        </el-select>
        <el-input
          v-model="filter.keyword"
          placeholder="订单号/用户名"
          clearable
          style="width: 220px"
          @keyup.enter="onFilterChange"
          @clear="onFilterChange"
        />
        <el-date-picker
          v-model="filter.dateRange"
          type="daterange"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          value-format="YYYY-MM-DD"
          style="width: 260px"
        />
        <el-button type="primary" @click="onFilterChange">查询</el-button>
        <el-button @click="resetFilter">重置</el-button>

        <!-- 统计 -->
        <div class="stat-summary">
          <span class="stat-item">待支付 <b>{{ stats.pending_count }}</b></span>
          <span class="stat-item">已支付 <b>{{ stats.paid_count }}</b></span>
          <span class="stat-item">总收入 <b class="primary">¥ {{ totalIncomeYuan.toFixed(2) }}</b></span>
        </div>
      </div>

      <el-table :data="filteredList" stripe v-loading="loading">
        <el-table-column prop="order_no" label="订单号" min-width="170" />
        <el-table-column label="用户" min-width="110">
          <template #default="{ row }">
            <span v-if="row.user_username">{{ row.user_username }}</span>
            <span v-else class="text-muted" :title="row.user_id">{{ row.user_id ? row.user_id.slice(0, 8) + '…' : '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="plan_name" label="套餐" min-width="110" />
        <el-table-column label="金额" width="120">
          <template #default="{ row }">
            <!-- 修复 P0: 后端 amount_cents 单位是分, 需 /100 转元显示 -->
            <span class="amount-text">¥ {{ yuan(row.amount_cents).toFixed(2) }}</span>
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
            <span v-if="row.payment_method">{{ payMethodText(row.payment_method) }}</span>
            <span v-else class="text-muted">-</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="160">
          <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="220" fixed="right">
          <template #default="{ row }">
            <el-button size="small" link @click="viewDetail(row)">详情</el-button>
            <el-button
              v-if="row.status === 'pending'"
              size="small"
              link
              type="success"
              :loading="actionLoadingId === row.id"
              @click="markPaid(row)"
            >标记已付</el-button>
            <el-button
              v-if="row.status === 'paid'"
              size="small"
              link
              type="warning"
              :loading="actionLoadingId === row.id"
              @click="refund(row)"
            >退款</el-button>
            <el-button
              v-if="row.status === 'pending'"
              size="small"
              link
              type="danger"
              :loading="actionLoadingId === row.id"
              @click="cancelOrder(row)"
            >取消</el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 修复 P1: 加分页组件, 旧版无分页组件, 后端默认 size=20, 第 21 条之后永远看不到 -->
      <div class="pagination-wrap">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[10, 20, 50, 100]"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @current-change="loadData"
          @size-change="onSizeChange"
        />
      </div>
    </div>

    <!-- 订单详情对话框 -->
    <el-dialog v-model="detailVisible" title="订单详情" width="560px">
      <el-descriptions v-if="currentOrder" :column="1" border>
        <el-descriptions-item label="订单号">{{ currentOrder.order_no }}</el-descriptions-item>
        <el-descriptions-item label="用户">
          {{ currentOrder.user_username || (currentOrder.user_id ? currentOrder.user_id.slice(0, 8) + '…' : '-') }}
        </el-descriptions-item>
        <el-descriptions-item label="套餐">{{ currentOrder.plan_name }}</el-descriptions-item>
        <el-descriptions-item label="实付金额">
          <span class="amount-final">¥ {{ yuan(currentOrder.amount_cents).toFixed(2) }}</span>
        </el-descriptions-item>
        <el-descriptions-item v-if="currentOrder.coupon_code" label="优惠券">
          {{ currentOrder.coupon_code }}
        </el-descriptions-item>
        <el-descriptions-item label="订单状态">
          <el-tag size="small" :type="statusTagType(currentOrder.status)" effect="dark">
            {{ statusText(currentOrder.status) }}
          </el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="支付方式">
          {{ currentOrder.payment_method ? payMethodText(currentOrder.payment_method) : '-' }}
        </el-descriptions-item>
        <el-descriptions-item v-if="currentOrder.trade_no" label="流水号">{{ currentOrder.trade_no }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(currentOrder.created_at) }}</el-descriptions-item>
        <el-descriptions-item v-if="currentOrder.paid_at" label="支付时间">{{ formatTime(currentOrder.paid_at) }}</el-descriptions-item>
        <el-descriptions-item v-if="currentOrder.expired_at" label="过期时间">{{ formatTime(currentOrder.expired_at) }}</el-descriptions-item>
      </el-descriptions>
      <template #footer>
        <el-button
          v-if="currentOrder && currentOrder.status === 'pending'"
          type="success"
          :loading="actionLoadingId === currentOrder.id"
          @click="markPaid(currentOrder)"
        >手动标记已支付</el-button>
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

// 修复 P0: 字段名全部改为 snake_case, 与后端 model.Order + OrderListItem DTO 一致
// 后端返回字段: id, order_no, user_id, user_username, plan_id, plan_name,
//   amount_cents, status, payment_method, trade_no, coupon_id, coupon_code,
//   paid_at, expired_at, created_at, updated_at
interface Order {
  id: string
  order_no: string
  user_id: string
  user_username: string
  plan_id: string
  plan_name: string
  amount_cents: number // 实付金额(分)
  status: string
  payment_method: string
  trade_no: string
  coupon_id?: string
  coupon_code?: string
  paid_at?: string
  expired_at?: string
  created_at: string
  updated_at?: string
}

const loading = ref(false)
const list = ref<Order[]>([])
const detailVisible = ref(false)
const currentOrder = ref<Order | null>(null)
// 修复 P2: 操作按钮加 loading, 防快速双击重复请求
const actionLoadingId = ref('')

// 修复 P1: 分页状态
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)

// 筛选条件
const filter = reactive({
  status: '' as string,
  keyword: '',
  dateRange: [] as string[],
})

// 金额(分)转元, 用整数运算避免 float 精度问题
const yuan = (cents: number | undefined): number => {
  if (!cents || isNaN(cents)) return 0
  return cents / 100
}

// 状态映射
type TagType = 'primary' | 'success' | 'info' | 'warning' | 'danger'
const statusText = (s: string): string => {
  const map: Record<string, string> = {
    pending: '待支付', paid: '已支付', cancelled: '已取消', expired: '已过期', refunded: '已退款',
  }
  return map[s] || s
}
const statusTagType = (s: string): TagType => {
  const map: Record<string, TagType> = {
    pending: 'warning', paid: 'success', cancelled: 'info', expired: 'danger', refunded: 'danger',
  }
  return map[s] || 'info'
}
const payMethodText = (m: string): string => {
  const map: Record<string, string> = {
    epay_alipay: '支付宝', epay_wechat: '微信支付',
  }
  return map[m] || m
}

// 修复 P1: 旧版 filteredList 在前端做 keyword/dateRange 过滤, 但只对当前 20 条生效,
// 第 21 条之后永远搜不到。现 keyword 改由后端搜索, dateRange 仍在前端做(后端暂未支持),
// 注: 后端不支持 date_range, 这里只在当前页基础上做时间过滤, 大数据量下仍有限制,
// 已在后端 ListAll 增加 keyword 支持, dateRange 暂保留前端过滤(可作为后续优化)。
const filteredList = computed(() => {
  return list.value.filter((o) => {
    if (filter.dateRange && filter.dateRange.length === 2) {
      // 修复 P1: 旧版字符串比较因 ISO 格式含 'T'(0x54) > ' '(0x20) 失效, 当天订单被错误过滤掉。
      // 现改用 Date 数值比较, 正确处理 RFC3339 格式。
      const start = new Date(filter.dateRange[0] + 'T00:00:00').getTime()
      const end = new Date(filter.dateRange[1] + 'T23:59:59').getTime()
      const created = new Date(o.created_at).getTime()
      if (isNaN(start) || isNaN(end) || isNaN(created)) return true // 解析失败则不过滤
      if (created < start || created > end) return false
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
  currentPage.value = 1
  loadData()
}

// 筛选条件变化时回到第 1 页重新加载
const onFilterChange = () => {
  currentPage.value = 1
  loadData()
}

const onSizeChange = (size: number) => {
  pageSize.value = size
  currentPage.value = 1
  loadData()
}

// 查看详情
const viewDetail = (row: any) => {
  currentOrder.value = row
  detailVisible.value = true
}

// 手动标记已支付（用于线下收款）
// 修复 P0: 旧版 try{}catch{} 后无条件更新本地状态 + 弹成功, API 失败时用户被误导。
// 现仅在 API 成功后才更新本地状态 + 弹成功, 失败时拦截器已弹错误, 这里不再改状态。
// 成功后调用 loadData() 重新拉取列表(含 paid_at 后端返回的真实值), 避免本地写时区错乱。
const markPaid = (row: any) => {
  ElMessageBox.confirm(
    `确认将订单「${row.order_no}」标记为已支付吗？\n此操作通常用于线下收款确认。`,
    '手动标记已支付',
    { type: 'warning', confirmButtonText: '确认标记', cancelButtonText: '取消' },
  ).then(async () => {
    actionLoadingId.value = row.id
    try {
      await request.post(`/api/v1/admin/orders/${row.id}/mark-paid`)
      ElMessage.success('订单已标记为已支付')
      // 重新拉取列表+统计, 拿后端返回的真实 paid_at / trade_no
      await loadData()
    } catch {
      // 拦截器已弹错误, 不改本地状态
    } finally {
      actionLoadingId.value = ''
    }
  }).catch(() => {})
}

// 退款
const refund = (row: any) => {
  ElMessageBox.confirm(
    `确认对订单「${row.order_no}」进行退款吗？退款后订单状态将变为已退款。`,
    '退款操作',
    { type: 'warning', confirmButtonText: '确认退款', cancelButtonText: '取消' },
  ).then(async () => {
    actionLoadingId.value = row.id
    try {
      await request.post(`/api/v1/admin/orders/${row.id}/refund`)
      ElMessage.success('订单已退款')
      await loadData()
    } catch {
      // 拦截器已弹错误
    } finally {
      actionLoadingId.value = ''
    }
  }).catch(() => {})
}

// 取消订单
const cancelOrder = (row: any) => {
  ElMessageBox.confirm(`确认取消订单「${row.order_no}」吗？`, '提示', {
    type: 'warning', confirmButtonText: '取消订单', cancelButtonText: '保留',
  }).then(async () => {
    actionLoadingId.value = row.id
    try {
      await request.post(`/api/v1/admin/orders/${row.id}/cancel`)
      ElMessage.success('订单已取消')
      await loadData()
    } catch {
      // 拦截器已弹错误
    } finally {
      actionLoadingId.value = ''
    }
  }).catch(() => {})
}

// 加载数据
// 修复 ORDER-LIST-01 (P0): 后端 AdminOrderList 返回 {list: [...], total: N},
// 前端原代码期望数组导致 ElMessage.error("订单数据格式异常") 刷屏。
// 修复 P1: 加 page/size 参数支持分页, 加 keyword 参数后端搜索。
const loadData = async () => {
  loading.value = true
  try {
    const res: any = await request.get('/api/v1/admin/orders', {
      params: {
        status: filter.status || undefined,
        keyword: filter.keyword || undefined,
        page: currentPage.value,
        size: pageSize.value,
      },
    })
    // 兼容两种结构: 标准 {list, total} 或裸数组(老接口/测试用)
    const data = res?.data || res
    const arr = Array.isArray(data) ? data : (data?.list || [])
    list.value = Array.isArray(arr) ? arr : []
    // 修复 P1: 从响应中读取 total, 驱动分页组件
    total.value = Array.isArray(data) ? data.length : (Number(data?.total) || 0)
  } catch {
    list.value = []
    total.value = 0
    // 拦截器已弹错误, 这里不重复弹
  } finally {
    loading.value = false
  }
  // 并行加载统计(不阻塞列表, 失败静默), 修复 P1: 操作后 stats 也需刷新
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
.text-muted { color: var(--np-text-muted); }
.amount-final { color: var(--np-primary); font-weight: 700; font-size: 16px; }

.pagination-wrap {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
}
</style>
