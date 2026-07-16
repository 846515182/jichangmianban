<template>
  <div class="admin-tickets">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">工单管理</h2>
          <p class="page-desc">处理用户提交的工单与反馈</p>
        </div>
        <div class="header-actions">
          <el-select v-model="statusFilter" placeholder="状态筛选" clearable style="width: 130px">
            <el-option label="待处理" value="open" />
            <el-option label="已回复" value="replied" />
            <el-option label="已关闭" value="closed" />
          </el-select>
        </div>
      </div>

      <el-table :data="filteredList" stripe v-loading="loading">
        <el-table-column prop="id" label="工单号" width="90" />
        <el-table-column prop="subject" label="主题" min-width="200" />
        <el-table-column prop="username" label="提交用户" width="120" />
        <el-table-column label="优先级" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="priorityType(row.priority)" effect="dark">
              {{ priorityText(row.priority) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="statusType(row.status)" effect="plain">
              {{ statusText(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="更新时间" width="170">
          <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button size="small" link type="primary" @click="openDetail(row)">查看</el-button>
            <el-button v-if="row.status !== 'closed'" size="small" link @click="closeTicket(row)">关闭</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 工单详情对话框 -->
    <el-dialog v-model="detailVisible" title="工单详情" width="640px" destroy-on-close>
      <template v-if="current">
        <div class="detail-header">
          <h3>{{ current.subject }}</h3>
          <div class="detail-meta">
            <el-tag size="small" :type="statusType(current.status)" effect="plain">{{ statusText(current.status) }}</el-tag>
            <span>用户：{{ current.username }}</span>
            <span>创建：{{ formatTime(current.created_at) }}</span>
          </div>
        </div>
        <div class="message-list">
          <div v-for="msg in current.messages" :key="msg.id" class="message-item" :class="msg.from">
            <div class="message-avatar">{{ msg.from === 'admin' ? '管' : '户' }}</div>
            <div class="message-body">
              <div class="message-meta">
                <span class="message-from">{{ msg.from === 'admin' ? '管理员' : current.username }}</span>
                <span class="message-time">{{ formatTime(msg.createdAt) }}</span>
              </div>
              <div class="message-content">{{ msg.content }}</div>
            </div>
          </div>
        </div>
        <div class="reply-area" v-if="current.status !== 'closed'">
          <el-input v-model="replyText" type="textarea" :rows="3" placeholder="输入回复内容..." />
          <el-button type="primary" :loading="replying" @click="handleReply" style="margin-top: 12px">发送回复</el-button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

interface TicketRow {
  id: string
  user_id: string
  username: string
  subject: string
  priority: string
  status: string
  updated_at: string
  created_at: string
  messages?: Array<{ id: string; from: string; content: string; createdAt: string }>
}

const loading = ref(false)
const statusFilter = ref('')
const list = ref<TicketRow[]>([])

const filteredList = computed(() => {
  if (!statusFilter.value) return list.value
  return list.value.filter((t) => t.status === statusFilter.value)
})

// el-tag 标签类型
type TagType = 'primary' | 'success' | 'info' | 'warning' | 'danger'
const statusText = (s: string) => ({ open: '待处理', replied: '已回复', closed: '已关闭' }[s] || s)
const statusType = (s: string): TagType => ({ open: 'warning', replied: 'primary', closed: 'info' } as Record<string, TagType>)[s] || 'primary'
const priorityText = (p: string) => ({ low: '低', normal: '中', high: '高' }[p] || p)
const priorityType = (p: string): TagType => ({ low: 'info', normal: 'warning', high: 'danger' } as Record<string, TagType>)[p] || 'primary'

// 详情
const detailVisible = ref(false)
const current = ref<TicketRow | null>(null)
const replyText = ref('')
const replying = ref(false)

const openDetail = async (row: any) => {
  current.value = { ...row, messages: [] }
  replyText.value = ''
  detailVisible.value = true
  try {
    const res: any = await request.get(`/api/v1/admin/tickets/${row.id}`)
    if (res && res.data && Array.isArray(res.data.replies)) {
      if (current.value) {
        current.value.messages = res.data.replies.map((r: any) => ({
          id: r.id,
          from: r.reply_type === 'admin' ? 'admin' : 'user',
          content: r.content,
          createdAt: (r.created_at || '').replace('T', ' ').slice(0, 19),
        }))
      }
    }
  } catch (e: any) {
    ElMessage.error(e?.message || '加载详情失败')
  }
}

const handleReply = async () => {
  if (!replyText.value.trim() || !current.value) return
  replying.value = true
  try {
    await request.post(`/api/v1/admin/tickets/${current.value.id}/reply`, { content: replyText.value })
    if (current.value.messages) {
      current.value.messages.push({
        id: 'm' + Date.now(), from: 'admin', content: replyText.value,
        createdAt: new Date().toISOString().replace('T', ' ').slice(0, 19),
      })
    }
    current.value.status = 'replied'
    current.value.updated_at = new Date().toISOString().replace('T', ' ').slice(0, 19)
    replyText.value = ''
    ElMessage.success('回复已发送')
  } catch (e: any) {
    ElMessage.error(e?.message || '回复失败')
  } finally {
    replying.value = false
  }
}

const closeTicket = async (row: any) => {
  try {
    await request.post(`/api/v1/admin/tickets/${row.id}/close`)
    row.status = 'closed'
    ElMessage.success('工单已关闭')
  } catch (e: any) {
    ElMessage.error(e?.message || '关闭失败')
  }
}

onMounted(async () => {
  loading.value = true
  try {
    const res: any = await request.get('/api/v1/admin/tickets')
    if (res && res.data && Array.isArray(res.data.list)) {
      list.value = res.data.list
    } else if (res && Array.isArray(res.data)) {
      list.value = res.data
    } else if (Array.isArray(res)) {
      list.value = res
    }
  } catch (e: any) {
    ElMessage.error(e?.message || '加载工单列表失败')
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
.page-card { padding: 20px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px; flex-wrap: wrap; gap: 12px; }
.page-title { margin: 0; font-size: 18px; color: var(--np-text); }
.page-desc { margin: 6px 0 0; font-size: 13px; color: var(--np-text-secondary); }

.detail-header h3 { margin: 0 0 10px; color: var(--np-text); }
.detail-meta { display: flex; gap: 16px; align-items: center; font-size: 13px; color: var(--np-text-secondary); margin-bottom: 20px; flex-wrap: wrap; }
.message-list { max-height: 340px; overflow-y: auto; display: flex; flex-direction: column; gap: 16px; padding: 12px; background: var(--np-bg-soft); border-radius: 8px; margin-bottom: 16px; }
.message-item { display: flex; gap: 10px; }
.message-item.admin { flex-direction: row-reverse; }
.message-avatar { width: 32px; height: 32px; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-size: 12px; flex-shrink: 0; background: var(--np-primary-dim); color: var(--np-primary); }
.message-item.admin .message-avatar { background: rgba(157,78,221,0.2); color: var(--np-purple); }
.message-body { max-width: 75%; }
.message-item.admin .message-body { text-align: right; }
.message-meta { display: flex; gap: 8px; font-size: 12px; color: var(--np-text-muted); margin-bottom: 4px; }
.message-item.admin .message-meta { justify-content: flex-end; }
.message-content { background: var(--np-card); border: 1px solid var(--np-border); padding: 8px 12px; border-radius: 8px; font-size: 14px; color: var(--np-text); display: inline-block; text-align: left; }
</style>
