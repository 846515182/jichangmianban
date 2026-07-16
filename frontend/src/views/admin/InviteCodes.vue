<template>
  <div class="admin-page">
    <div class="page-header">
      <h2 class="page-title">邀请码管理</h2>
      <el-button type="primary" @click="openCreate">生成新邀请码</el-button>
    </div>
    <div class="np-card list-card">
      <el-table :data="list" v-loading="loading" stripe>
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="code" label="邀请码" min-width="180">
          <template #default="{ row }">
            <el-input v-model="row.code" readonly size="small">
              <template #append>
                <el-button @click="copyCode(row.code)" size="small">复制</el-button>
              </template>
            </el-input>
          </template>
        </el-table-column>
        <el-table-column label="使用情况" width="140">
          <template #default="{ row }">
            <span :class="{ 'text-danger': row.used_count >= row.max_uses && row.max_uses > 0 }">
              {{ row.used_count }} / {{ row.max_uses < 0 ? '∞' : row.max_uses }}
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="expires_at" label="过期时间" width="200">
          <template #default="{ row }">{{ row.expires_at || '永久' }}</template>
        </el-table-column>
        <el-table-column prop="disabled" label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.disabled ? 'danger' : 'success'" size="small">
              {{ row.disabled ? '已禁用' : '启用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="note" label="备注" min-width="120" />
        <el-table-column prop="created_at" label="创建时间" width="180" />
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }">
            <el-button v-if="!row.disabled" type="danger" size="small" @click="disable(row)">禁用</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="createOpen" title="生成新邀请码" width="420px">
      <el-form :model="form" label-width="100px">
        <el-form-item label="最大使用次数">
          <el-input-number v-model="form.max_uses" :min="-1" :max="10000" />
          <span class="hint">-1 表示不限</span>
        </el-form-item>
        <el-form-item label="有效期(小时)">
          <el-input-number v-model="form.expires_in" :min="0" :max="8760" />
          <span class="hint">0 表示永久</span>
        </el-form-item>
        <el-form-item label="长度">
          <el-input-number v-model="form.length" :min="8" :max="16" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="form.note" maxlength="200" show-word-limit />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createOpen = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitCreate">生成</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'

const list = ref<any[]>([])
const loading = ref(false)
const createOpen = ref(false)
const submitting = ref(false)
const form = reactive({ max_uses: 1, expires_in: 0, length: 12, note: '' })

const fetchList = async () => {
  loading.value = true
  try {
    const res: any = await request.get('/api/v1/admin/invite-codes', { params: { page: 1, size: 100 } })
    if (res && (res.code === 0 || res.code === 200)) {
      list.value = res.data?.list || []
    }
  } catch (e: any) {
    ElMessage.error(e?.message || '加载失败')
  } finally {
    loading.value = false
  }
}

const openCreate = () => {
  form.max_uses = 1; form.expires_in = 0; form.length = 12; form.note = ''
  createOpen.value = true
}

const submitCreate = async () => {
  submitting.value = true
  try {
    const res: any = await request.post('/api/v1/admin/invite-codes', {
      max_uses: form.max_uses,
      expires_in: form.expires_in || 0,
      length: form.length,
      note: form.note,
    })
    if (res && (res.code === 0 || res.code === 200)) {
      ElMessage.success('生成成功: ' + (res.data?.code || ''))
      createOpen.value = false
      fetchList()
    } else {
      ElMessage.error((res && res.msg) || '生成失败')
    }
  } catch (e: any) {
    ElMessage.error(e?.message || '请求失败')
  } finally {
    submitting.value = false
  }
}

const disable = async (row: any) => {
  try {
    await ElMessageBox.confirm(`确定禁用邀请码 ${row.code}?`, '确认', { type: 'warning' })
  } catch { return }
  try {
    const res: any = await request.post(`/api/v1/admin/invite-codes/${row.id}/disable`)
    if (res && (res.code === 0 || res.code === 200)) {
      ElMessage.success('已禁用')
      fetchList()
    } else {
      ElMessage.error((res && res.msg) || '操作失败')
    }
  } catch (e: any) {
    ElMessage.error(e?.message || '请求失败')
  }
}

const copyCode = (code: string) => {
  navigator.clipboard.writeText(code).then(() => ElMessage.success('已复制'))
}

onMounted(fetchList)
</script>

<style scoped>
.admin-page { padding: 20px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.page-title { margin: 0; font-size: 20px; color: var(--np-text); }
.list-card { padding: 16px; }
.hint { color: var(--np-text-secondary, #8b98a9); font-size: 12px; margin-left: 8px; }
.text-danger { color: #f56c6c; }
</style>
