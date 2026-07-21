<template>
  <div class="admin-page">
    <div class="page-header">
      <h2>订阅管理</h2>
      <div class="header-actions">
        <el-input v-model="keyword" placeholder="搜索用户名/邮箱" clearable style="width:240px" @keyup.enter="fetchList" @clear="fetchList">
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-button type="primary" @click="fetchList">刷新</el-button>
      </div>
    </div>

    <el-table :data="list" v-loading="loading" stripe style="width:100%">
      <el-table-column prop="username" label="用户名" min-width="120" />
      <el-table-column prop="email" label="邮箱" min-width="180" show-overflow-tooltip />
      <el-table-column prop="sub_type" label="类型" width="90">
        <template #default="{ row }">
          <el-tag size="small">{{ row.sub_type || 'clash' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="用户状态" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="row.status === 'active' ? 'success' : 'danger'">
            {{ row.status === 'active' ? '正常' : '禁用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="到期时间" width="160">
        <template #default="{ row }">
          <span v-if="row.user_expired_at">{{ formatTime(row.user_expired_at) }}</span>
          <span v-else style="color:#909399">不限</span>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="订阅时间" width="160">
        <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="订阅链接" min-width="280">
        <template #default="{ row }">
          <div class="sub-url-cell">
            <el-input :model-value="row.subscribe_url" readonly size="small" style="flex:1">
              <template #append>
                <el-button size="small" @click="copyText(row.subscribe_url)">
                  <el-icon><CopyDocument /></el-icon>
                </el-button>
              </template>
            </el-input>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="120" fixed="right">
        <template #default="{ row }">
          <el-dropdown @command="(cmd: string) => openClient(cmd, row.subscribe_url)">
            <el-button size="small" link type="primary">
              导入客户端<el-icon class="el-icon--right"><ArrowDown /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="clash">Clash</el-dropdown-item>
                <el-dropdown-item command="singbox">SingBox</el-dropdown-item>
                <el-dropdown-item command="v2ray">V2Ray</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[10, 20, 50]"
        layout="total, sizes, prev, pager, next"
        @size-change="fetchList"
        @current-change="fetchList"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Search, CopyDocument, ArrowDown } from '@element-plus/icons-vue'
import request from '@/utils/request'

interface SubRow {
  id: string
  user_id: string
  sub_token: string
  sub_type: string
  expires_at?: string
  created_at: string
  username: string
  email: string
  status: string
  user_expired_at?: string
  subscribe_url: string
}

const list = ref<SubRow[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const keyword = ref('')

const fetchList = async () => {
  loading.value = true
  try {
    const res = await request.get<any>('/api/v1/admin/subscriptions', {
      params: { page: page.value, size: pageSize.value, keyword: keyword.value },
    })
    if (res && res.code === 0 && res.data) {
      list.value = res.data.list || []
      total.value = res.data.total || 0
    }
  } catch (e: any) {
    ElMessage.error('获取订阅列表失败')
  } finally {
    loading.value = false
  }
}

const formatTime = (t: string) => {
  if (!t) return ''
  const d = new Date(t)
  return d.toLocaleString('zh-CN', { hour12: false })
}

const copyText = (text: string) => {
  navigator.clipboard.writeText(text).then(() => {
    ElMessage.success('已复制订阅链接')
  }).catch(() => {
    ElMessage.error('复制失败')
  })
}

const openClient = (type: string, url: string) => {
  let finalUrl = url
  if (url.includes('type=')) {
    finalUrl = url.replace(/type=[^&]*/, 'type=' + type)
  } else {
    finalUrl = url + (url.includes('?') ? '&' : '?') + 'type=' + type
  }
  // 修复 P1-FE1: 加 'noopener,noreferrer' 防止新窗口通过 window.opener 反向操作原页面(reverse tabnabbing 钓鱼)
  window.open(finalUrl, '_blank', 'noopener,noreferrer')
}

onMounted(() => {
  fetchList()
})
</script>

<style scoped>
.admin-page { padding: 20px; }
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}
.page-header h2 { margin: 0; font-size: 18px; }
.header-actions { display: flex; gap: 10px; }
.sub-url-cell { display: flex; align-items: center; }
.pagination {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
}
</style>
