<template>
  <div class="admin-coupons">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">优惠券管理</h2>
          <p class="page-desc">创建、管理优惠券，支持批量生成优惠码</p>
        </div>
        <div class="header-actions">
          <el-input v-model="keyword" placeholder="搜索优惠码" :prefix-icon="Search" clearable style="width: 220px" />
          <el-button type="primary" @click="openDialog()">
            <el-icon><Plus /></el-icon>新增优惠券
          </el-button>
        </div>
      </div>

      <el-table :data="filteredList" stripe v-loading="loading">
        <el-table-column prop="code" label="优惠码" min-width="160">
          <template #default="{ row }">
            <span class="coupon-code">{{ row.code }}</span>
          </template>
        </el-table-column>
        <el-table-column label="类型" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="row.type === 'percent' ? 'primary' : 'warning'" effect="plain">
              {{ row.type === 'percent' ? '百分比' : '固定金额' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="面值" width="100">
          <template #default="{ row }">
            <span class="value-text">
              {{ row.type === 'percent' ? row.value + '%' : '¥ ' + row.value.toFixed(2) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="最低消费" width="110">
          <template #default="{ row }">¥ {{ row.minSpend.toFixed(2) }}</template>
        </el-table-column>
        <el-table-column label="使用情况" width="140">
          <template #default="{ row }">
            <div class="usage-cell">
              <span>{{ row.usedCount }} / {{ row.totalCount }}</span>
              <el-progress
                :percentage="usagePercent(row)"
                :stroke-width="6"
                :show-text="false"
                :color="usageColor(row)"
              />
            </div>
          </template>
        </el-table-column>
        <el-table-column label="过期时间" min-width="160">
          <template #default="{ row }">
            <span :class="{ expired: isExpired(row) }">{{ formatTime(row.expireAt) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-switch
              :model-value="row.status === 'active'"
              :disabled="isExpired(row) || row.usedCount >= row.totalCount"
              @change="(v) => toggleStatus(row, v)"
            />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <el-button size="small" link @click="copyCode(row)">复制</el-button>
            <el-button size="small" link type="danger" @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 新增/批量生成对话框 -->
    <el-dialog
      v-model="dialogVisible"
      title="新增优惠券"
      width="560px"
      destroy-on-close
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="110px">
        <el-form-item label="优惠类型" prop="type">
          <el-radio-group v-model="form.type">
            <el-radio value="percent">百分比折扣</el-radio>
            <el-radio value="fixed">固定金额</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="form.type === 'percent' ? '折扣比例(%)' : '减免金额(元)'" prop="value">
          <el-input-number
            v-model="form.value"
            :min="form.type === 'percent' ? 1 : 0.01"
            :max="form.type === 'percent' ? 100 : 99999"
            :precision="form.type === 'percent' ? 0 : 2"
            controls-position="right"
            style="width: 100%"
          />
          <span class="form-tip" v-if="form.type === 'percent'">如：10 表示打 9 折</span>
        </el-form-item>
        <el-form-item label="最低消费(元)" prop="minSpend">
          <el-input-number v-model="form.minSpend" :min="0" :precision="2" controls-position="right" style="width: 100%" />
        </el-form-item>
        <el-form-item label="发行总量" prop="totalCount">
          <el-input-number v-model="form.totalCount" :min="1" :max="100000" controls-position="right" style="width: 100%" />
        </el-form-item>
        <el-form-item label="过期时间" prop="expireAt">
          <el-date-picker
            v-model="form.expireAt"
            type="datetime"
            placeholder="选择过期时间"
            style="width: 100%"
            value-format="YYYY-MM-DD HH:mm:ss"
          />
        </el-form-item>
        <el-form-item label="批量生成">
          <el-switch v-model="form.batch" />
          <span class="form-tip">开启后按下列数量批量生成优惠码</span>
        </el-form-item>
        <el-form-item v-if="form.batch" label="生成数量" prop="batchCount">
          <el-input-number v-model="form.batchCount" :min="1" :max="1000" controls-position="right" style="width: 100%" />
        </el-form-item>
        <el-form-item v-if="!form.batch" label="优惠码">
          <el-input v-model="form.code" placeholder="自定义优惠码，留空自动生成">
            <template #append>
              <el-button @click="generateCode">随机生成</el-button>
            </template>
          </el-input>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { Search, Plus } from '@element-plus/icons-vue'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

type CouponType = 'percent' | 'fixed'
type CouponStatus = 'active' | 'disabled'

interface Coupon {
  id: string
  code: string
  type: CouponType
  value: number
  minSpend: number
  usedCount: number
  totalCount: number
  expireAt: string
  status: CouponStatus
  createdAt: string
}

const loading = ref(false)
const saving = ref(false)
const keyword = ref('')
const list = ref<Coupon[]>([])



const filteredList = computed(() => {
  if (!keyword.value) return list.value
  const k = keyword.value.toLowerCase()
  return list.value.filter((c) => c.code.toLowerCase().includes(k))
})

// 使用率
const usagePercent = (row: any): number => {
  if (!row.totalCount) return 0
  return Math.min(100, Math.round((row.usedCount / row.totalCount) * 100))
}
const usageColor = (row: any): string => {
  const p = usagePercent(row)
  if (p >= 100) return '#ff006e'
  if (p >= 80) return '#ffbe0b'
  return '#00f5d4'
}

// 是否过期
const isExpired = (row: any): boolean => {
  return new Date(row.expireAt).getTime() < Date.now()
}

// 对话框
const dialogVisible = ref(false)
const formRef = ref<FormInstance>()
const form = reactive({
  type: 'percent' as CouponType,
  value: 10,
  minSpend: 0,
  totalCount: 100,
  expireAt: '',
  batch: false,
  batchCount: 10,
  code: '',
})

const rules: FormRules = {
  type: [{ required: true, message: '请选择优惠类型', trigger: 'change' }],
  value: [{ required: true, message: '请输入面值', trigger: 'blur' }],
  totalCount: [{ required: true, message: '请输入发行总量', trigger: 'blur' }],
  expireAt: [{ required: true, message: '请选择过期时间', trigger: 'change' }],
}

// 随机生成优惠码
const generateCode = () => {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
  let code = ''
  for (let i = 0; i < 8; i++) {
    code += chars.charAt(Math.floor(Math.random() * chars.length))
  }
  form.code = code
}

// 打开对话框
const openDialog = () => {
  Object.assign(form, {
    type: 'percent', value: 10, minSpend: 0, totalCount: 100,
    expireAt: '', batch: false, batchCount: 10, code: '',
  })
  // 默认过期时间为30天后
  const d = new Date(Date.now() + 30 * 24 * 60 * 60 * 1000)
  form.expireAt = d.toISOString().replace('T', ' ').slice(0, 19)
  dialogVisible.value = true
}

// 保存（新增或批量生成）
const handleSave = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    saving.value = true
    try {
      const payload = {
        type: form.type,
        value: form.value,
        minSpend: form.minSpend,
        totalCount: form.totalCount,
        expireAt: form.expireAt,
        batch: form.batch,
        batchCount: form.batchCount,
        code: form.code,
      }
      let createdCount = 0
      try {
        const res: any = await request.post('/api/v1/admin/coupons', payload)
        const data = res?.data || res
        if (Array.isArray(data)) {
          // 批量返回
          list.value.unshift(...data)
          createdCount = data.length
        } else if (data && data.id) {
          list.value.unshift(data)
          createdCount = 1
        }
      } catch {
        ElMessage.error('创建优惠券失败，请稍后重试')
        return
      }
      ElMessage.success(`成功创建 ${createdCount} 张优惠券`)
      dialogVisible.value = false
    } finally {
      saving.value = false
    }
  })
}

// 启用/禁用
const toggleStatus = async (row: any, value: boolean | string | number) => {
  const newStatus: CouponStatus = value ? 'active' : 'disabled'
  try { await request.patch(`/api/v1/admin/coupons/${row.id}/status`, { status: newStatus }) } catch { /* */ }
  row.status = newStatus
  ElMessage.success(newStatus === 'active' ? '已启用' : '已禁用')
}

// 删除
const handleDelete = (row: any) => {
  ElMessageBox.confirm(`确定删除优惠码「${row.code}」吗？`, '提示', {
    type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
  }).then(async () => {
    try { await request.delete(`/api/v1/admin/coupons/${row.id}`) } catch { /* */ }
    list.value = list.value.filter((c) => c.id !== row.id)
    ElMessage.success('优惠券已删除')
  }).catch(() => {})
}

// 复制优惠码
const copyCode = async (row: any) => {
  try {
    await navigator.clipboard.writeText(row.code)
    ElMessage.success(`已复制：${row.code}`)
  } catch {
    ElMessage.warning('复制失败，请手动复制')
  }
}

// 加载数据
onMounted(async () => {
  loading.value = true
  try {
    const res: any = await request.get('/api/v1/admin/coupons')
    const arr = res?.data || res
    if (Array.isArray(arr)) {
      list.value = arr
    } else {
      list.value = []
      ElMessage.error('优惠券数据格式异常')
    }
  } catch {
    list.value = []
    ElMessage.error('加载优惠券列表失败，请稍后重试')
  } finally {
    loading.value = false
  }
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

.coupon-code {
  font-family: 'JetBrains Mono', 'Cascadia Code', monospace;
  color: var(--np-primary);
  font-weight: 600;
  letter-spacing: 1px;
}
.value-text { color: var(--np-amber); font-weight: 600; }
.usage-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 12px;
  color: var(--np-text-secondary);
}
.expired { color: var(--np-danger); }
.form-tip { font-size: 12px; color: var(--np-text-muted); margin-left: 8px; }
</style>
