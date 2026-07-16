<template>
  <div class="admin-announcements">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">公告管理</h2>
          <p class="page-desc">发布与管理系统公告</p>
        </div>
        <el-button type="primary" @click="openDialog()"><el-icon><Plus /></el-icon>发布公告</el-button>
      </div>

      <el-table :data="list" stripe v-loading="loading">
        <el-table-column prop="title" label="标题" min-width="200">
          <template #default="{ row }">
            <span>{{ row.title }}</span>
            <el-tag v-if="row.pinned" size="small" type="warning" effect="dark" style="margin-left: 8px">置顶</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="内容预览" min-width="280">
          <template #default="{ row }">
            <span class="content-preview">{{ stripHtml(row.content).slice(0, 60) }}...</span>
          </template>
        </el-table-column>
        <el-table-column label="发布时间" width="170">
          <template #default="{ row }">{{ formatTime(row.published_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="{ row }">
            <el-button size="small" link @click="togglePin(row)">{{ row.pinned ? '取消置顶' : '置顶' }}</el-button>
            <el-button size="small" link type="primary" @click="openDialog(row)">编辑</el-button>
            <el-button size="small" link type="danger" @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 编辑对话框 -->
    <el-dialog v-model="dialogVisible" :title="editing ? '编辑公告' : '发布公告'" width="720px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-width="80px">
        <el-form-item label="标题" prop="title">
          <el-input v-model="form.title" placeholder="公告标题" />
        </el-form-item>
        <el-form-item label="置顶">
          <el-switch v-model="form.pinned" />
        </el-form-item>
        <el-form-item label="内容" prop="content">
          <!-- 富文本工具栏 -->
          <div class="rich-toolbar">
            <el-button-group>
              <el-button size="small" @click="exec('bold')"><b>B</b></el-button>
              <el-button size="small" @click="exec('italic')"><i>I</i></el-button>
              <el-button size="small" @click="exec('underline')"><u>U</u></el-button>
            </el-button-group>
            <el-button size="small" @click="exec('insertUnorderedList')">无序列表</el-button>
            <el-button size="small" @click="exec('insertOrderedList')">有序列表</el-button>
            <el-button size="small" @click="exec('formatBlock', '<h3>')">标题</el-button>
          </div>
          <div ref="editorRef" class="rich-editor" contenteditable="true" @input="onEditorInput"></div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, nextTick } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'
import { mockAnnouncements, type AnnouncementRecord } from '@/mock/data'

const loading = ref(false)
const saving = ref(false)
const list = ref<AnnouncementRecord[]>([...mockAnnouncements])

const stripHtml = (html: string) => html.replace(/<[^>]+>/g, '')

// 对话框
const dialogVisible = ref(false)
const editing = ref<AnnouncementRecord | null>(null)
const formRef = ref<FormInstance>()
const editorRef = ref<HTMLElement>()
const form = reactive({ title: '', content: '', pinned: false })
const rules: FormRules = {
  title: [{ required: true, message: '请输入标题', trigger: 'blur' }],
  content: [{ required: true, message: '请输入内容', trigger: 'blur' }],
}

const openDialog = (row?: any) => {
  editing.value = row || null
  if (row) {
    Object.assign(form, { title: row.title, content: row.content, pinned: row.pinned })
  } else {
    Object.assign(form, { title: '', content: '', pinned: false })
  }
  dialogVisible.value = true
  nextTick(() => {
    if (editorRef.value) editorRef.value.innerHTML = form.content
  })
}

const onEditorInput = () => {
  if (editorRef.value) form.content = editorRef.value.innerHTML
}

const exec = (cmd: string, val?: string) => {
  document.execCommand(cmd, false, val)
  onEditorInput()
  editorRef.value?.focus()
}

const handleSave = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    saving.value = true
    try {
      if (editing.value) {
        try { await request.put(`/api/v1/admin/announcements/${editing.value.id}`, { ...form }) } catch { /* */ }
        const idx = list.value.findIndex((a) => a.id === editing.value!.id)
        if (idx > -1) list.value[idx] = { ...list.value[idx], ...form }
        ElMessage.success('公告已更新')
      } else {
        const newItem: AnnouncementRecord = {
          id: 'a' + Date.now(), title: form.title, content: form.content, pinned: form.pinned,
          publishedAt: new Date().toISOString().replace('T', ' ').slice(0, 19),
        }
        try { const res = await request.post('/api/v1/admin/announcements', { ...form }); if (res && res.id) newItem.id = res.id } catch { /* */ }
        list.value.unshift(newItem)
        ElMessage.success('公告已发布')
      }
      dialogVisible.value = false
    } finally {
      saving.value = false
    }
  })
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm(`确定删除公告「${row.title}」吗？`, '提示', { type: 'warning' }).then(async () => {
    try { await request.delete(`/api/v1/admin/announcements/${row.id}`) } catch { /* */ }
    list.value = list.value.filter((a) => a.id !== row.id)
    ElMessage.success('公告已删除')
  }).catch(() => {})
}

const togglePin = async (row: any) => {
  try { await request.patch(`/api/v1/admin/announcements/${row.id}/pin`, { pinned: !row.pinned }) } catch { /* */ }
  row.pinned = !row.pinned
  ElMessage.success(row.pinned ? '已置顶' : '已取消置顶')
}

onMounted(async () => {
  loading.value = true
  try {
    const res = await request.get('/api/v1/admin/announcements')
    if (Array.isArray(res)) list.value = res
    else if (res && Array.isArray(res.data)) list.value = res.data
  } catch { /* mock */ } finally { loading.value = false }
})
</script>

<style scoped>
.page-card { padding: 20px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px; }
.page-title { margin: 0; font-size: 18px; color: var(--np-text); }
.page-desc { margin: 6px 0 0; font-size: 13px; color: var(--np-text-secondary); }
.content-preview { color: var(--np-text-secondary); font-size: 13px; }

.rich-toolbar { display: flex; gap: 8px; margin-bottom: 8px; flex-wrap: wrap; }
.rich-editor {
  min-height: 200px; max-height: 320px; overflow-y: auto; padding: 12px;
  background: var(--np-bg-soft); border: 1px solid var(--np-border); border-radius: 8px;
  color: var(--np-text); outline: none; font-size: 14px; line-height: 1.6;
}
.rich-editor:focus { border-color: var(--np-primary); }
.rich-editor :deep(h3) { color: var(--np-primary); margin: 8px 0; }
.rich-editor :deep(ul), .rich-editor :deep(ol) { padding-left: 24px; }
</style>
