<template>
  <div class="admin-coupons">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">优惠券管理</h2>
          <p class="page-desc">创建、管理优惠券</p>
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
              {{ row.type === 'percent' ? row.value + '%' : '¥ ' + (row.value / 100).toFixed(2) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="最低消费" width="110">
          <template #default="{ row }">¥ {{ (row.min_amount_cents / 100).toFixed(2) }}</template>
        </el-table-column>
        <el-table-column label="使用情况" width="140">
          <template #default="{ row }">
            <div class="usage-cell">
              <span>{{ row.used_count }} / {{ row.max_uses === 0 ? '不限' : row.max_uses }}</span>
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
            <span :class="{ expired: isExpired(row) }">{{ row.expire_at ? formatTime(row.expire_at) : '永不过期' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-switch
              :model-value="!!row.is_enabled"
              :disabled="isExpired(row) || (row.max_uses > 0 && row.used_count >= row.max_uses)"
              @change="(v) => toggleStatus(row, v)"
            />
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button size="small" link type="primary" @click="openDialog(row)">编辑</el-button>
            <el-button size="small" link @click="copyCode(row)">复制</el-button>
            <el-button size="small" link type="danger" @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 新增/编辑对话框 -->
    <el-dialog
      v-model="dialogVisible"
      :title="editing ? '编辑优惠券' : '新增优惠券'"
      width="560px"
      destroy-on-close
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="110px">
        <el-form-item label="优惠类型" prop="type">
          <el-radio-group v-model="form.type" :disabled="!!editing">
            <el-radio value="percent">百分比折扣</el-radio>
            <el-radio value="fixed">固定金额</el-radio>
          </el-radio-group>
          <span class="form-tip" v-if="editing">类型不可修改, 请删除后重建</span>
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
          <el-input-number v-model="form.totalCount" :min="0" :max="100000" controls-position="right" style="width: 100%" />
          <span class="form-tip">0 表示不限</span>
        </el-form-item>
        <el-form-item label="过期时间" prop="expireAt">
          <el-date-picker
            v-model="form.expireAt"
            type="datetime"
            placeholder="选择过期时间(可留空表示永不过期)"
            style="width: 100%"
            value-format="YYYY-MM-DDTHH:mm:ssZ"
          />
        </el-form-item>
        <el-form-item label="启用状态">
          <el-switch v-model="form.isEnabled" />
        </el-form-item>
        <el-form-item label="优惠码">
          <el-input v-model="form.code" placeholder="自定义优惠码，留空自动生成">
            <template #append>
              <el-button @click="generateCode">随机生成</el-button>
            </template>
          </el-input>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">{{ editing ? '保存' : '创建' }}</el-button>
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

// 修复 P1 bug: 字段名/单位与后端 model.Coupon JSON tag 完全对齐
// - value: percent 时 1-100 整数; fixed 时金额(分)
// - min_amount_cents: 最低消费(分)
// - max_uses: 0=不限
// - expire_at: *time.Time, 可空
// - is_enabled: bool (后端切换接口接受 status: 'active'/'disabled')
interface Coupon {
  id: string
  code: string
  type: CouponType
  value: number
  min_amount_cents: number
  max_uses: number
  used_count: number
  expire_at: string | null
  is_enabled: boolean
  created_at: string
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

const usagePercent = (row: any): number => {
  if (!row.max_uses || row.max_uses === 0) {
    return row.used_count > 0 ? 100 : 0
  }
  return Math.min(100, Math.round((row.used_count / row.max_uses) * 100))
}
const usageColor = (row: any): string => {
  const p = usagePercent(row)
  if (p >= 100) return '#ff006e'
  if (p >= 80) return '#ffbe0b'
  return '#00f5d4'
}

const isExpired = (row: any): boolean => {
  if (!row.expire_at) return false
  return new Date(row.expire_at).getTime() < Date.now()
}

// 对话框
const dialogVisible = ref(false)
const formRef = ref<FormInstance>()
const editing = ref<Coupon | null>(null)
const form = reactive({
  type: 'percent' as CouponType,
  value: 10,
  minSpend: 0,
  totalCount: 100,
  expireAt: '',
  isEnabled: true,
  code: '',
})

const rules: FormRules = {
  type: [{ required: true, message: '请选择优惠类型', trigger: 'change' }],
  value: [{ required: true, message: '请输入面值', trigger: 'blur' }],
  totalCount: [{ required: true, message: '请输入发行总量', trigger: 'blur' }],
}

// 使用 Web Crypto API 生成密码学安全的随机优惠码
// Math.random 是伪随机可预测, 攻击者可推测下一张优惠码进行薅羊毛
const generateCode = () => {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
  const len = 8
  const buf = new Uint32Array(len)
  crypto.getRandomValues(buf)
  let code = ''
  for (let i = 0; i < len; i++) {
    code += chars.charAt(buf[i] % chars.length)
  }
  form.code = code
}

const openDialog = (row?: any) => {
  editing.value = (row as Coupon) || null
  if (row) {
    // 编辑: 从 row 加载, value/minSpend 从分转元(percent 的 value 是整数百分比, 直接用)
    const r = row as Coupon
    Object.assign(form, {
      type: r.type,
      value: r.type === 'percent' ? r.value : r.value / 100,
      minSpend: r.min_amount_cents / 100,
      totalCount: r.max_uses,
      expireAt: r.expire_at || '',
      isEnabled: r.is_enabled,
      code: r.code,
    })
  } else {
    Object.assign(form, {
      type: 'percent', value: 10, minSpend: 0, totalCount: 100,
      expireAt: '', isEnabled: true, code: '',
    })
  }
  dialogVisible.value = true
}

const handleSave = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    // 校验优惠码必填(后端 binding:"required")
    if (!form.code.trim()) {
      ElMessage.warning('请输入优惠码或点击随机生成')
      return
    }
    saving.value = true
    try {
      // 修复 P1 bug: 字段名/单位对齐后端 createCouponRequest / updateCouponRequest
      // - fixed 类型 value 从元转分; percent 类型 value 直接为百分比整数
      // - min_amount_cents 从元转分
      // - expire_at: 后端 *time.Time 接受 RFC3339, el-date-picker value-format 已设为 RFC3339
      const isPercent = form.type === 'percent'
      const payload: any = {
        code: form.code.trim(),
        type: form.type,
        value: isPercent ? Math.round(form.value) : Math.round(form.value * 100),
        min_amount_cents: Math.round(form.minSpend * 100),
        max_uses: form.totalCount,
        is_enabled: form.isEnabled,
      }
      if (form.expireAt) payload.expire_at = form.expireAt
      try {
        if (editing.value) {
          // 编辑: PUT, 后端会忽略 type 跨类型修改
          const res: any = await request.put(`/api/v1/admin/coupons/${editing.value.id}`, payload)
          const data = res?.data || res
          if (data && data.id) {
            const idx = list.value.findIndex((c) => c.id === editing.value!.id)
            if (idx >= 0) list.value[idx] = data as Coupon
            ElMessage.success('优惠券已更新')
            dialogVisible.value = false
          }
        } else {
          // 创建: POST
          const res: any = await request.post('/api/v1/admin/coupons', payload)
          const data = res?.data || res
          if (data && data.id) {
            list.value.unshift(data as Coupon)
            ElMessage.success('优惠券创建成功')
            dialogVisible.value = false
          }
        }
      } catch {
        // 错误提示已由 request 拦截器统一处理
      }
    } finally {
      saving.value = false
    }
  })
}

// 启用/禁用
const toggleStatus = async (row: any, value: boolean | string | number) => {
  const newStatus = value ? 'active' : 'disabled'
  try {
    await request.patch(`/api/v1/admin/coupons/${row.id}/status`, { status: newStatus })
    row.is_enabled = value === true
    ElMessage.success(newStatus === 'active' ? '已启用' : '已禁用')
  } catch {
    // 错误提示已由 request 拦截器统一处理
  }
}

// 删除
const handleDelete = (row: any) => {
  ElMessageBox.confirm(`确定删除优惠码「${row.code}」吗？`, '提示', {
    type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
  }).then(async () => {
    try {
      await request.delete(`/api/v1/admin/coupons/${row.id}`)
      list.value = list.value.filter((c) => c.id !== row.id)
      ElMessage.success('优惠券已删除')
    } catch { /* */ }
  }).catch(() => {})
}

// 复制优惠码
const copyCode = async (row: any) => {
  const text = row.code
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      ElMessage.success(`已复制：${text}`)
      return
    }
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.top = '-9999px'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    document.body.removeChild(ta)
    ElMessage.success(`已复制：${text}`)
  } catch {
    ElMessage.warning('复制失败，请手动复制')
  }
}

// 加载数据
onMounted(async () => {
  loading.value = true
  try {
    const res: any = await request.get('/api/v1/admin/coupons')
    // 修复 P1 bug: 后端返回 {code:0, data:{list:[...], total:N}}
    list.value = res?.data?.list || (Array.isArray(res?.data) ? res.data : []) || []
  } catch {
    list.value = []
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
