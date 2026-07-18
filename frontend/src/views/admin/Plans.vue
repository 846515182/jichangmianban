<template>
  <div class="admin-plans">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">套餐管理</h2>
          <p class="page-desc">管理售卖套餐、流量配额与价格。节点可见性通过「节点管理」绑定套餐实现，无需等级配置</p>
        </div>
        <div class="header-actions">
          <el-input v-model="keyword" placeholder="搜索套餐名称" :prefix-icon="Search" clearable style="width: 220px" />
          <el-button type="primary" @click="openDialog()"><el-icon><Plus /></el-icon>新增套餐</el-button>
        </div>
      </div>

      <el-table :data="filteredList" stripe v-loading="loading">
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column label="流量" min-width="120">
          <template #default="{ row }">{{ row.traffic_limit ? formatTraffic(row.traffic_limit) : '不限' }}</template>
        </el-table-column>
        <el-table-column label="有效期" width="100">
          <template #default="{ row }">{{ row.duration_days }} 天</template>
        </el-table-column>
        <el-table-column label="价格" width="110">
          <template #default="{ row }">
            <span class="price-text">¥ {{ (row.price_cents / 100).toFixed(2) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="设备限制" width="100">
          <template #default="{ row }">{{ row.device_limit || "无限" }}</template>
        </el-table-column>
        <el-table-column label="节点数量" width="100">
          <template #default="{ row }">
            <el-tag :type="row.node_count > 0 ? 'success' : 'info'" effect="plain" size="small">
              {{ row.node_count || 0 }} 个
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-switch :model-value="row.is_enabled" @change="(v: any) => toggleStatus(row, v)" />
          </template>
        </el-table-column>
        <el-table-column prop="sort_order" label="排序" width="80" />
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="{ row }">
            <el-button size="small" link type="primary" @click="openDialog(row)">编辑</el-button>
            <el-button size="small" link type="danger" @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialogVisible" :title="editing ? '编辑套餐' : '新增套餐'" width="560px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
        <el-form-item label="套餐名称" prop="name">
          <el-input v-model="form.name" placeholder="如：标准版" />
        </el-form-item>
        <el-form-item label="套餐描述">
          <el-input v-model="form.description" placeholder="一句话描述套餐特点" />
        </el-form-item>
        <el-form-item label="套餐优点">
          <div class="dynamic-list">
            <div v-for="(item, idx) in form.features" :key="'f'+idx" class="dynamic-row">
              <el-input v-model="form.features[idx]" placeholder="如：高速节点、专属线路" />
              <el-button link type="danger" @click="form.features.splice(idx, 1)">
                <el-icon><Delete /></el-icon>
              </el-button>
            </div>
            <el-button size="small" link type="primary" @click="form.features.push('')">
              <el-icon><Plus /></el-icon>添加优点
            </el-button>
          </div>
        </el-form-item>
        <el-form-item label="套餐缺点">
          <div class="dynamic-list">
            <div v-for="(item, idx) in form.limitations" :key="'l'+idx" class="dynamic-row">
              <el-input v-model="form.limitations[idx]" placeholder="如：流量较少、不支持特定节点" />
              <el-button link type="danger" @click="form.limitations.splice(idx, 1)">
                <el-icon><Delete /></el-icon>
              </el-button>
            </div>
            <el-button size="small" link type="primary" @click="form.limitations.push('')">
              <el-icon><Plus /></el-icon>添加缺点
            </el-button>
          </div>
        </el-form-item>
        <el-form-item label="流量(GB)" prop="trafficLimitGB">
          <el-input-number v-model="form.trafficLimitGB" :min="0" :precision="2" controls-position="right" style="width:100%" />
          <span class="form-tip">0 表示不限流量</span>
        </el-form-item>
        <el-form-item label="有效期(天)" prop="duration_days">
          <el-input-number v-model="form.duration_days" :min="1" :max="3650" controls-position="right" style="width:100%" />
        </el-form-item>
        <el-form-item label="价格(元)" prop="price">
          <el-input-number v-model="form.price" :min="0" :precision="2" controls-position="right" style="width:100%" />
        </el-form-item>
        <el-form-item label="原价(元)">
          <el-input-number v-model="form.original_price" :min="0" :precision="2" controls-position="right" style="width:100%" />
        </el-form-item>
        <el-form-item label="设备限制">
          <el-input-number v-model="form.device_limit" :min="0" :max="100" controls-position="right" style="width:100%" />
          <span class="form-tip">0 表示不限制</span>
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="form.sort_order" :min="0" controls-position="right" style="width:100%" />
          <span class="form-tip">数值越小越靠前</span>
        </el-form-item>
        <el-form-item label="启用状态">
          <el-switch v-model="form.is_enabled" />
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
import { ref, reactive, computed, onMounted } from "vue"
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from "element-plus"
import { Search, Plus, Delete } from "@element-plus/icons-vue"
import request from "@/utils/request"
import { formatTraffic } from "@/utils/format"

interface PlanRow {
  id: string; name: string; description: string; features: string; limitations: string
  traffic_limit: number; duration_days: number; price_cents: number; original_price_cents: number
  device_limit: number; node_count?: number; sort_order: number; is_enabled: boolean
}

// 解析 JSON 数组字符串为字符串数组(容错)
const parseList = (s: string): string[] => {
  if (!s) return []
  try {
    const arr = JSON.parse(s)
    return Array.isArray(arr) ? arr.map((x: any) => String(x)).filter(Boolean) : []
  } catch {
    return s.split('\n').map(x => x.trim()).filter(Boolean)
  }
}

const loading = ref(false)
const saving = ref(false)
const keyword = ref("")
const list = ref<PlanRow[]>([])

const filteredList = computed(() => {
  if (!keyword.value) return list.value
  const k = keyword.value.toLowerCase()
  return list.value.filter(p => p.name.toLowerCase().includes(k))
})

const dialogVisible = ref(false)
const editing = ref<PlanRow | null>(null)
const formRef = ref<FormInstance>()
const form = reactive({
  name: "", description: "", features: [] as string[], limitations: [] as string[],
  trafficLimitGB: 100, duration_days: 30,
  price: 0, original_price: 0, device_limit: 3,
  sort_order: 1, is_enabled: true,
})

const rules: FormRules = {
  name: [{ required: true, message: "请输入套餐名称", trigger: "blur" }],
  trafficLimitGB: [{ required: true, message: "请输入流量配额", trigger: "blur" }],
  duration_days: [{ required: true, message: "请输入有效期", trigger: "blur" }],
  price: [{ required: true, message: "请输入价格", trigger: "blur" }],
}

const openDialog = (row?: any) => {
  editing.value = row || null
  if (row) {
    Object.assign(form, {
      name: row.name, description: row.description || "",
      features: parseList(row.features), limitations: parseList(row.limitations),
      trafficLimitGB: +(row.traffic_limit / 1024 ** 3).toFixed(2),
      duration_days: row.duration_days,
      price: row.price_cents / 100, original_price: row.original_price_cents / 100,
      device_limit: row.device_limit,
      sort_order: row.sort_order, is_enabled: row.is_enabled,
    })
  } else {
    Object.assign(form, {
      name: "", description: "", features: [], limitations: [],
      trafficLimitGB: 100, duration_days: 30,
      price: 0, original_price: 0, device_limit: 3,
      sort_order: list.value.length + 1, is_enabled: true,
    })
  }
  dialogVisible.value = true
}

const handleSave = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    saving.value = true
    try {
      const payload = {
        name: form.name, description: form.description,
        features: JSON.stringify(form.features.filter(Boolean)),
        limitations: JSON.stringify(form.limitations.filter(Boolean)),
        traffic_limit: Math.round(form.trafficLimitGB * 1024 ** 3),
        duration_days: form.duration_days,
        price_cents: Math.round(form.price * 100),
        original_price_cents: Math.round(form.original_price * 100),
        device_limit: form.device_limit,
        sort_order: form.sort_order, is_enabled: form.is_enabled,
      }
      if (editing.value) {
        await request.put("/api/v1/admin/plans/" + editing.value.id, payload)
        ElMessage.success("套餐已更新")
      } else {
        const res: any = await request.post("/api/v1/admin/plans", payload)
        if (res && res.code === 0) { ElMessage.success("套餐已创建") }
        else { ElMessage.error((res as any)?.msg || "创建失败") }
      }
      dialogVisible.value = false
      await fetchList()
    } catch { /* */ }
    finally { saving.value = false }
  })
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm("确定删除套餐「" + row.name + "」吗？", "提示", { type: "warning" }).then(async () => {
    try { await request.delete("/api/v1/admin/plans/" + row.id); ElMessage.success("套餐已删除"); await fetchList() } catch { /* */ }
  }).catch(() => {})
}

const toggleStatus = async (row: any, val: boolean) => {
  try { await request.put("/api/v1/admin/plans/" + row.id, { is_enabled: val }); row.is_enabled = val; ElMessage.success(val ? "已启用" : "已禁用") } catch { /* */ }
}

const fetchList = async () => {
  loading.value = true
  try {
    const res: any = await request.get("/api/v1/admin/plans")
    let arr: any[] = []
    if (res && res.data && Array.isArray(res.data.list)) arr = res.data.list
    else if (res && Array.isArray(res.data)) arr = res.data
    list.value = arr.sort((a: PlanRow, b: PlanRow) => a.sort_order - b.sort_order)
  } catch { /* */ }
  finally { loading.value = false }
}

onMounted(() => { fetchList() })
</script>

<style scoped>
.page-card { padding: 20px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px; flex-wrap: wrap; gap: 12px; }
.page-title { margin: 0; font-size: 18px; color: var(--np-text); }
.page-desc { margin: 6px 0 0; font-size: 13px; color: var(--np-text-secondary); }
.header-actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.price-text { color: var(--np-primary); font-weight: 600; }
.form-tip { font-size: 12px; color: var(--np-text-muted); margin-left: 8px; }
.dynamic-list { width: 100%; }
.dynamic-row { display: flex; gap: 8px; margin-bottom: 8px; align-items: center; }
.dynamic-row .el-input { flex: 1; }
</style>
