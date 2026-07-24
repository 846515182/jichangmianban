<template>
  <div class="admin-audit">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">登录审计日志</h2>
          <p class="page-desc">查看所有用户登录记录，监控异常登录</p>
        </div>
        <div class="header-actions">
          <el-select v-model="statusFilter" placeholder="状态筛选" clearable style="width: 100%; max-width: 130px" @change="onFilterChange">
            <el-option label="成功" value="success" />
            <el-option label="失败" value="failed" />
          </el-select>
          <!-- 修复 P1: 旧版 placeholder 写"搜索用户名/IP", 但后端 LoginAudit 不存 username, 实际可搜 IP/位置/target_id -->
          <el-input v-model="keyword" placeholder="搜索 IP/位置" :prefix-icon="Search" clearable style="width: 100%; max-width: 220px" @keyup.enter="onFilterChange" @clear="onFilterChange" />
        </div>
      </div>

      <div class="table-wrap">
        <el-table :data="filteredList" stripe v-loading="loading">
          <el-table-column prop="id" label="日志号" width="80" />
          <el-table-column label="目标" min-width="140">
            <template #default="{ row }">
              <el-tag size="small" :type="row.target_type === 'admin' ? 'warning' : 'info'" effect="plain">{{ row.target_type || '-' }}</el-tag>
              <span v-if="row.target_id" class="target-id" :title="row.target_id">{{ row.target_id.slice(0, 8) }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="ip" label="IP地址" min-width="140" />
          <el-table-column prop="location" label="登录位置" min-width="120" />
          <el-table-column prop="user_agent" label="User-Agent" min-width="240" show-overflow-tooltip />
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="row.success ? 'success' : 'danger'" effect="dark">
                <el-icon style="vertical-align: middle"><CircleCheck v-if="row.success" /><CircleClose v-else /></el-icon>
                {{ row.success ? '成功' : '失败' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="时间" width="180">
            <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
          </el-table-column>
        </el-table>
      </div>

      <!-- 修复 P1: 旧版无分页组件, 后端默认 size=20, 第 21 条之后不可见 -->
      <div class="pagination-wrap">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[10, 20, 50, 100]"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @current-change="fetchList"
          @size-change="onSizeChange"
        />
      </div>

      <div class="audit-stats">
        <el-row :gutter="16">
          <el-col :xs="24" :sm="8">
            <div class="stat-mini np-card">
              <div class="stat-mini-label">总登录次数</div>
              <div class="stat-mini-value">{{ total }}</div>
            </div>
          </el-col>
          <el-col :xs="24" :sm="8">
            <div class="stat-mini np-card">
              <div class="stat-mini-label">成功登录</div>
              <div class="stat-mini-value success">{{ successCount }}</div>
            </div>
          </el-col>
          <el-col :xs="24" :sm="8">
            <div class="stat-mini np-card">
              <div class="stat-mini-label">失败尝试</div>
              <div class="stat-mini-value danger">{{ failedCount }}</div>
            </div>
          </el-col>
        </el-row>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { Search } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

const loading = ref(false)
const keyword = ref('')
const statusFilter = ref('')
const list = ref<any[]>([])

// 修复 P1: 分页状态。旧版无分页组件, 后端默认 size=20, 第 21 条之后不可见。
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)

// 修复 P1: 旧版 keyword/statusFilter 只在前端过滤当前页, 第 21 条之后永远搜不到。
// 现改为后端查询(后端 LoginAudit ListAll 支持 keyword + success 参数)。
const filteredList = computed(() => list.value)

// 注意: 成功/失败次数为当前页统计, 非全局总数(后端未提供聚合统计接口)
const successCount = computed(() => list.value.filter((l) => l.success).length)
const failedCount = computed(() => list.value.filter((l) => !l.success).length)

// 修复 P1: 提取为可复用函数, 加分页 + 后端搜索参数
const fetchList = async () => {
  loading.value = true
  try {
    const params: any = {
      page: currentPage.value,
      size: pageSize.value,
    }
    if (keyword.value) params.keyword = keyword.value
    if (statusFilter.value) params.success = statusFilter.value === 'success' ? 'true' : 'false'
    const res: any = await request.get('/api/v1/admin/system/login-audit', { params })
    list.value = res?.data?.list || (Array.isArray(res?.data) ? res.data : []) || []
    total.value = Number(res?.data?.total) || list.value.length
  } catch { /* */ } finally { loading.value = false }
}

const onSizeChange = (size: number) => {
  pageSize.value = size
  currentPage.value = 1
  fetchList()
}

const onFilterChange = () => {
  currentPage.value = 1
  fetchList()
}

onMounted(() => { fetchList() })
</script>

<style scoped>
.page-card { padding: 20px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px; flex-wrap: wrap; gap: 12px; }
.page-title { margin: 0; font-size: 18px; color: var(--np-text); }
.page-desc { margin: 6px 0 0; font-size: 13px; color: var(--np-text-secondary); }
.header-actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.audit-stats { margin-top: 24px; }
.stat-mini { padding: 16px 20px; text-align: center; }
.stat-mini-label { font-size: 13px; color: var(--np-text-secondary); margin-bottom: 8px; }
.stat-mini-value { font-size: 28px; font-weight: 700; color: var(--np-text); }
.stat-mini-value.success { color: var(--np-primary); }
.stat-mini-value.danger { color: var(--np-danger); }
.target-id { margin-left: 6px; font-family: 'JetBrains Mono', monospace; font-size: 12px; color: var(--np-text-muted); }
.pagination-wrap { margin-top: 16px; display: flex; justify-content: flex-end; }

@media (max-width: 768px) {
  .page-card { padding: 14px; }
  .page-header { flex-direction: column; align-items: stretch; }
  .header-actions { flex-direction: column; align-items: stretch; width: 100%; }
  .header-actions .el-select,
  .header-actions .el-input {
    width: 100% !important;
    max-width: none !important;
    margin-left: 0 !important;
  }
  .header-actions .el-select + .el-input,
  .header-actions .el-input + .el-select {
    margin-top: 8px;
  }
  .pagination-wrap { justify-content: center; }
  .stat-mini { padding: 12px; }
  .stat-mini-value { font-size: 22px; }
}
</style>
