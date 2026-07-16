<template>
  <div class="admin-audit">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">登录审计日志</h2>
          <p class="page-desc">查看所有用户登录记录，监控异常登录</p>
        </div>
        <div class="header-actions">
          <el-select v-model="statusFilter" placeholder="状态筛选" clearable style="width: 100%; max-width: 130px">
            <el-option label="成功" value="success" />
            <el-option label="失败" value="failed" />
          </el-select>
          <el-input v-model="keyword" placeholder="搜索用户名/IP" :prefix-icon="Search" clearable style="width: 100%; max-width: 220px" />
        </div>
      </div>

      <el-table :data="filteredList" stripe v-loading="loading">
        <el-table-column prop="id" label="日志号" width="80" />
        <el-table-column prop="username" label="用户名" min-width="120" />
        <el-table-column prop="ip" label="IP地址" min-width="140" />
        <el-table-column prop="location" label="登录位置" min-width="120" />
        <el-table-column prop="userAgent" label="User-Agent" min-width="240" show-overflow-tooltip />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="row.status === 'success' ? 'success' : 'danger'" effect="dark">
              <el-icon style="vertical-align: middle"><CircleCheck v-if="row.status === 'success'" /><CircleClose v-else /></el-icon>
              {{ row.status === 'success' ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="时间" width="180">
          <template #default="{ row }">{{ formatTime(row.createdAt) }}</template>
        </el-table-column>
      </el-table>

      <div class="audit-stats">
        <el-row :gutter="16">
          <el-col :xs="24" :sm="8">
            <div class="stat-mini np-card">
              <div class="stat-mini-label">总登录次数</div>
              <div class="stat-mini-value">{{ list.length }}</div>
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
import { mockLoginAudits, type LoginAuditRecord } from '@/mock/data'

const loading = ref(false)
const keyword = ref('')
const statusFilter = ref('')
const list = ref<LoginAuditRecord[]>([...mockLoginAudits])

const filteredList = computed(() => {
  return list.value.filter((item) => {
    if (statusFilter.value && item.status !== statusFilter.value) return false
    if (keyword.value) {
      const k = keyword.value.toLowerCase()
      if (!item.username.toLowerCase().includes(k) && !item.ip.toLowerCase().includes(k)) return false
    }
    return true
  })
})

const successCount = computed(() => list.value.filter((l) => l.status === 'success').length)
const failedCount = computed(() => list.value.filter((l) => l.status === 'failed').length)

onMounted(async () => {
  loading.value = true
  try {
    const res = await request.get('/api/v1/admin/system/login-audit')
    if (Array.isArray(res)) list.value = res
    else if (res && Array.isArray(res.data)) list.value = res.data
  } catch { /* mock */ } finally { loading.value = false }
})
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
</style>
