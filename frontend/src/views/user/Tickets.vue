<template>
  <div class="user-tickets">
    <div class="page-header">
      <h2 class="page-title">我的工单</h2>
      <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon>提交工单</el-button>
    </div>

    <el-row :gutter="20">
      <el-col :xs="24" :md="8">
        <div class="np-card list-card">
          <div class="list-title">工单列表</div>
          <div class="ticket-list">
            <div
              v-for="t in tickets"
              :key="t.id"
              class="ticket-item"
              :class="{ active: current?.id === t.id }"
              @click="current = t; loadDetail(t.id)"
            >
              <div class="ticket-top">
                <span class="ticket-subject">{{ t.subject }}</span>
                <el-tag size="small" :type="statusType(t.status)" effect="plain">{{ statusText(t.status) }}</el-tag>
              </div>
              <div class="ticket-time">{{ formatTime(t.updated_at || t.updatedAt) }}</div>
            </div>
            <el-empty v-if="!tickets.length" description="暂无工单" :image-size="60" />
          </div>
        </div>
      </el-col>

      <el-col :xs="24" :md="16">
        <div class="np-card detail-card">
          <template v-if="current">
            <div class="detail-header">
              <h3>{{ current.subject }}</h3>
              <el-tag size="small" :type="statusType(current.status)" effect="dark">{{ statusText(current.status) }}</el-tag>
            </div>
            <div class="message-list">
              <div v-for="msg in current.messages" :key="msg.id" class="message-item" :class="msg.from">
                <div class="message-avatar">{{ msg.from === 'user' ? '我' : '管' }}</div>
                <div class="message-body">
                  <div class="message-meta">
                    <span class="message-from">{{ msg.from === 'user' ? '我' : '管理员' }}</span>
                    <span class="message-time">{{ formatTime(msg.created_at) }}</span>
                  </div>
                  <div class="message-content">{{ msg.content }}</div>
                </div>
              </div>
            </div>
            <div class="reply-area" v-if="current.status !== 'closed'">
              <el-input v-model="replyText" type="textarea" :rows="3" placeholder="输入回复内容..." />
              <el-button type="primary" @click="handleReply" :loading="replying" style="margin-top: 12px">发送</el-button>
            </div>
            <el-alert v-else title="该工单已关闭" type="info" :closable="false" />
          </template>
          <el-empty v-else description="选择左侧工单查看详情" />
        </div>
      </el-col>
    </el-row>

    <!-- 创建工单对话框 -->
    <el-dialog v-model="createDialog" title="提交工单" width="520px">
      <el-form ref="formRef" :model="form" :rules="rules" label-width="80px">
        <el-form-item label="主题" prop="subject">
          <el-input v-model="form.subject" placeholder="请简要描述问题" />
        </el-form-item>
        <el-form-item label="优先级" prop="priority">
          <el-radio-group v-model="form.priority">
            <el-radio value="low">低</el-radio>
            <el-radio value="medium">中</el-radio>
            <el-radio value="high">高</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="内容" prop="content">
          <el-input v-model="form.content" type="textarea" :rows="5" placeholder="详细描述您遇到的问题..." />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog = false">取消</el-button>
        <el-button type="primary" @click="handleCreate" :loading="creating">提交</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

interface TicketMessage {
  id: string
  from: 'user' | 'admin'
  content: string
  created_at: string
}
interface TicketRecord {
  id: string
  subject: string
  user_id?: string
  userId?: string
  username: string
  status: string
  priority: string
  created_at?: string
  createdAt?: string
  updated_at?: string
  updatedAt?: string
  messages?: TicketMessage[]
}

const tickets = ref<TicketRecord[]>([])
const current = ref<TicketRecord | null>(null)
const replyText = ref('')
const replying = ref(false)

// el-tag 标签类型
type TagType = 'primary' | 'success' | 'info' | 'warning' | 'danger'
const statusText = (s: string) => ({ open: '待处理', pending: '处理中', closed: '已关闭' }[s] || s)
const statusType = (s: string): TagType => ({ open: 'warning', pending: 'primary', closed: 'info' } as Record<string, TagType>)[s] || 'primary'

const handleReply = async () => {
  if (!replyText.value.trim() || !current.value) return
  replying.value = true
  try {
    await request.post(`/api/v1/user/tickets/${current.value.id}/reply`, { content: replyText.value })
    if (!current.value.messages) current.value.messages = []
    current.value.messages.push({
      id: 'm' + Date.now(), from: 'user', content: replyText.value,
      created_at: new Date().toISOString().replace('T', ' ').slice(0, 19),
    })
    current.value.updated_at = new Date().toISOString().replace('T', ' ').slice(0, 19)
    replyText.value = ''
    ElMessage.success('回复已发送')
  } catch { /* 拦截器处理 */ } finally {
    replying.value = false
  }
}

// 创建工单
const createDialog = ref(false)
const creating = ref(false)
const formRef = ref<FormInstance>()
const form = reactive<{ subject: string; priority: 'low' | 'medium' | 'high'; content: string }>({ subject: '', priority: 'medium', content: '' })
const rules: FormRules = {
  subject: [{ required: true, message: '请输入主题', trigger: 'blur' }],
  content: [{ required: true, message: '请输入内容', trigger: 'blur' }],
}

const openCreate = () => {
  Object.assign(form, { subject: '', priority: 'medium', content: '' })
  createDialog.value = true
}

const handleCreate = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    creating.value = true
    try {
      // 修复 P1-FE3: 旧版内层 try{}catch{} 吞掉 API 错误后无条件 unshift + 弹成功,
      // 后端实际未创建工单时用户看到"已提交"但刷新后消失。改为 API 成功拿到 id 后再 unshift,
      // 失败时拦截器已弹错误, 本地状态不动。
      const now = new Date().toISOString().replace('T', ' ').slice(0, 19)
      const newTicket: TicketRecord = {
        id: 't' + Date.now(), subject: form.subject, username: '',
        status: 'open', priority: form.priority, created_at: now, updated_at: now,
        messages: [{ id: 'm' + Date.now(), from: 'user', content: form.content, created_at: now }],
      }
      const res: any = await request.post('/api/v1/user/tickets', { ...form })
      const id = res?.data?.id || res?.id
      if (!id) throw new Error('创建失败')
      newTicket.id = id
      tickets.value.unshift(newTicket)
      current.value = newTicket
      createDialog.value = false
      ElMessage.success('工单已提交')
    } catch {
      // 拦截器已弹错误, 不修改本地状态
    } finally {
      creating.value = false
    }
  })
}

onMounted(async () => {
  try {
    const res: any = await request.get('/api/v1/user/tickets')
    let arr: TicketRecord[] = []
    if (Array.isArray(res)) arr = res
    else if (res && Array.isArray(res.data)) arr = res.data
    else if (res && res.data && Array.isArray(res.data.list)) arr = res.data.list
    if (arr.length) {
      tickets.value = arr
      current.value = arr[0]
      // 拉取第一个工单详情(包含回复列表)
      await loadDetail(arr[0].id)
    }
  } catch { /* 拦截器处理 */ }
})

// 拉取工单详情(含回复)
const loadDetail = async (id: string) => {
  try {
    const res: any = await request.get(`/api/v1/user/tickets/${id}`)
    if (res && res.data) {
      const target = tickets.value.find((x) => x.id === id)
      if (target) {
        target.messages = (res.data.replies || []).map((r: any) => ({
          id: r.id,
          from: r.reply_type === 'admin' ? 'admin' : 'user',
          content: r.content,
          created_at: (r.created_at || '').replace('T', ' ').slice(0, 19),
        }))
      }
    }
  } catch { /* */ }
}
</script>

<style scoped>
.user-tickets { overflow-x: hidden; }
.page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 20px; }
.page-title { margin: 0; font-size: 20px; color: var(--np-text); }
.list-card { padding: 16px; }
.list-title { font-size: 14px; color: var(--np-text-secondary); margin-bottom: 12px; padding: 0 8px; }
.ticket-list { display: flex; flex-direction: column; gap: 4px; max-height: 500px; overflow-y: auto; }
.ticket-item { padding: 12px; border-radius: 8px; cursor: pointer; transition: background 0.2s; }
.ticket-item:hover { background: var(--np-card-hover); }
.ticket-item.active { background: var(--np-primary-dim); border-left: 3px solid var(--np-primary); }
.ticket-top { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 6px; }
.ticket-subject { font-size: 14px; color: var(--np-text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ticket-time { font-size: 12px; color: var(--np-text-muted); }

.detail-card { padding: 20px; min-height: 400px; }
.detail-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 20px; padding-bottom: 16px; border-bottom: 1px solid var(--np-border); }
.detail-header h3 { margin: 0; color: var(--np-text); }
.message-list { max-height: 360px; overflow-y: auto; display: flex; flex-direction: column; gap: 16px; margin-bottom: 16px; }
.message-item { display: flex; gap: 10px; }
.message-item.admin { flex-direction: row-reverse; }
.message-avatar { width: 32px; height: 32px; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-size: 12px; flex-shrink: 0; background: var(--np-primary-dim); color: var(--np-primary); }
.message-item.admin .message-avatar { background: rgba(157,78,221,0.2); color: var(--np-purple); }
.message-body { max-width: 75%; }
.message-item.admin .message-body { text-align: right; }
.message-meta { display: flex; gap: 8px; font-size: 12px; color: var(--np-text-muted); margin-bottom: 4px; }
.message-item.admin .message-meta { justify-content: flex-end; }
.message-content { background: var(--np-bg-soft); border: 1px solid var(--np-border); padding: 8px 12px; border-radius: 8px; font-size: 14px; color: var(--np-text); display: inline-block; text-align: left; }

@media (max-width: 768px) {
  .page-header { flex-direction: column; align-items: flex-start; gap: 12px; }
  .page-header .el-button { width: 100%; }
  .list-card { margin-bottom: 16px; }
  .ticket-list { max-height: 300px; }
  .detail-card { padding: 14px; min-height: auto; }
  .detail-header { flex-direction: column; align-items: flex-start; gap: 10px; }
  .message-list { max-height: 260px; }
  .message-body { max-width: 85%; }
  .reply-area .el-button { width: 100%; }
}
</style>
