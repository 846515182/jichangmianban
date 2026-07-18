<template>
  <div class="admin-users">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">用户管理</h2>
          <p class="page-desc">管理用户账号、流量配额与状态</p>
        </div>
        <div class="header-actions">
          <el-input v-model="keyword" placeholder="搜索用户名" :prefix-icon="Search" clearable style="width: 220px" />
          <el-button type="primary" @click="openDialog()"><el-icon><Plus /></el-icon>新增用户</el-button>
        </div>
      </div>

      <el-table :data="filteredList" stripe v-loading="loading">
        <el-table-column prop="username" label="用户名" min-width="100" />
        <el-table-column label="套餐" min-width="120">
          <template #default="{ row }">{{ row.plan_id ? planName(row.plan_id) : "未选择" }}</template>
        </el-table-column>

        <el-table-column label="流量用量" min-width="200">
          <template #default="{ row }">
            <div class="traffic-cell">
              <el-progress :percentage="trafficPercent(row)" :stroke-width="8" :color="trafficColor(row)" :show-text="false" />
              <span class="traffic-text">
                {{ formatTraffic(row.traffic_used) }}
                <template v-if="row.traffic_limit">/ {{ formatTraffic(row.traffic_limit) }}</template>
                <template v-else>/ 不限</template>
              </span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="到期时间" min-width="160">
          <template #default="{ row }">
            <span :class="{ expired: isExpired(row) }">{{ row.expired_at ? formatTime(row.expired_at) : "不限期" }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.status === 'active' ? 'success' : 'danger'" effect="plain">
              {{ row.status === 'active' ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="订阅链接" min-width="220">
          <template #default="{ row }">
            <el-input v-if="row.subscribe_url" :model-value="row.subscribe_url" readonly size="small" style="width:100%">
              <template #append>
                <el-button size="small" @click="copySubUrl(row.subscribe_url)"><el-icon><CopyDocument /></el-icon></el-button>
              </template>
            </el-input>
            <span v-else style="color:#909399;font-size:12px">无订阅</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="340" fixed="right">
          <template #default="{ row }">
            <el-button size="small" link type="primary" @click="openDialog(row)">编辑</el-button>
            <el-button size="small" link type="success" @click="openActivateDialog(row)">开通套餐</el-button>
            <el-button size="small" link @click="resetTraffic(row)">重置流量</el-button>
            <el-button size="small" link :type="row.status === 'active' ? 'warning' : 'success'" @click="toggleStatus(row)">
              {{ row.status === 'active' ? '禁用' : '启用' }}
            </el-button>
            <el-button size="small" link type="danger" @click="handleDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialogVisible" :title="editing ? '编辑用户' : '新增用户'" width="520px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" :disabled="!!editing" />
        </el-form-item>
        <el-form-item v-if="!editing" label="密码" prop="password">
          <el-input v-model="form.password" type="password" show-password />
        </el-form-item>
        <el-form-item label="流量配额(GB)">
          <el-input-number v-model="form.trafficLimitGB" :min="0" controls-position="right" style="width:100%" />
          <span class="form-tip">0 表示无限</span>
        </el-form-item>
        <el-form-item label="到期时间">
          <el-date-picker v-model="form.expireAt" type="datetime" placeholder="选择到期时间" style="width:100%" value-format="YYYY-MM-DD HH:mm:ss" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
      </template>
    </el-dialog>

    <!-- 开通套餐对话框 -->
    <el-dialog v-model="activateVisible" title="手动开通套餐" width="460px" destroy-on-close>
      <div style="margin-bottom:12px;color:var(--np-text-secondary);font-size:13px">
        为用户「{{ activateRow?.username }}」开通套餐（无需支付，直接生效）
      </div>
      <el-select v-model="activatePlanId" placeholder="选择套餐" style="width:100%" size="large">
        <el-option
          v-for="p in planList"
          :key="p.id"
          :label="`${p.name}${p.traffic_limit ? ' / ' + formatTraffic(p.traffic_limit) : ' / 不限'}${p.duration_days ? ' / ' + p.duration_days + '天' : ''}`"
          :value="p.id"
        />
      </el-select>
      <template #footer>
        <el-button @click="activateVisible = false">取消</el-button>
        <el-button type="primary" :loading="activating" @click="confirmActivate">确认开通</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from "vue"
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from "element-plus"
import { Search, Plus, CopyDocument } from "@element-plus/icons-vue"
import request from "@/utils/request"
import { formatTraffic, formatTime } from "@/utils/format"

interface PlanRow { id: string; name: string; traffic_limit: number; duration_days: number }
interface UserRow {
  id: string; username: string; email: string; traffic_used: number; traffic_limit: number
  expired_at: string; status: string; plan_id: string
  subscribe_url: any; sub_token: string; created_at: string
}

const loading = ref(false)
const saving = ref(false)
const keyword = ref("")
const list = ref<UserRow[]>([])
const planList = ref<PlanRow[]>([])

const filteredList = computed(() => {
  if (!keyword.value) return list.value
  const k = keyword.value.toLowerCase()
  return list.value.filter(u => u.username.toLowerCase().includes(k) || u.email.toLowerCase().includes(k))
})

const planName = (id: string) => { const p = planList.value.find(x => x.id === id); return p ? p.name : "" }

const trafficPercent = (row: any) => {
  if (!row.traffic_limit || row.traffic_limit <= 0) return 0
  return Math.min(100, Math.round((row.traffic_used / row.traffic_limit) * 100))
}
const trafficColor = (row: any) => {
  const p = trafficPercent(row)
  if (p >= 90) return "#ff006e"
  if (p >= 70) return "#ffbe0b"
  return "#00f5d4"
}
const isExpired = (row: any) => row.expired_at ? new Date(row.expired_at).getTime() < Date.now() : false

const dialogVisible = ref(false)
const editing = ref<UserRow | null>(null)
const formRef = ref<FormInstance>()
const form = reactive({
  username: "", email: "", password: "", plan_id: "",
  trafficLimitGB: 100, expireAt: "",
})
const rules: FormRules = {
  username: [{ required: true, message: "请输入用户名", trigger: "blur" }],
  email: [{ required: true, message: "请输入邮箱", trigger: "blur" }, { type: "email", message: "邮箱格式不正确", trigger: "blur" }],
  password: [{ required: true, message: "请输入密码", trigger: "blur" }],
}

const openDialog = (row?: any) => {
  editing.value = row || null
  if (row) {
    Object.assign(form, {
      username: row.username, email: row.email, password: "",
      plan_id: row.plan_id || "",
      trafficLimitGB: row.traffic_limit ? Math.round(row.traffic_limit / 1024 / 1024 / 1024) : 0,
      expireAt: row.expired_at || "",
    })
  } else {
    Object.assign(form, { username: "", email: "", password: "", plan_id: "", trafficLimitGB: 100, expireAt: "" })
  }
  dialogVisible.value = true
}

const handleSave = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    saving.value = true
    try {
      const payload: any = {
        username: form.username, email: form.email,
        traffic_limit: Math.floor(form.trafficLimitGB * 1024 * 1024 * 1024),
      }
      if (form.plan_id) payload.plan_id = form.plan_id
      if (form.expireAt) {
        const diffMs = new Date(form.expireAt).getTime() - Date.now()
        payload.expire_days = Math.max(1, Math.ceil(diffMs / (24 * 3600 * 1000)))
      }
      if (editing.value) {
        if (form.password) payload.password = form.password
        await request.put("/api/v1/admin/users/" + editing.value.id, payload)
        ElMessage.success("用户已更新")
      } else {
        payload.password = form.password
        const res: any = await request.post("/api/v1/admin/users", payload)
        if (res && res.code === 0) { ElMessage.success("用户已创建") }
        else { ElMessage.error((res as any)?.msg || "创建失败") }
      }
      dialogVisible.value = false
      await fetchList()
    } catch (e: any) { /* error handled by interceptor */ }
    finally { saving.value = false }
  })
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm("确定删除用户「" + row.username + "」吗？", "提示", { type: "warning" }).then(async () => {
    try { await request.delete("/api/v1/admin/users/" + row.id); ElMessage.success("用户已删除"); await fetchList() } catch { /* */ }
  }).catch(() => {})
}

const resetTraffic = (row: any) => {
  ElMessageBox.confirm("确定重置「" + row.username + "」的流量统计吗？", "提示", { type: "warning" }).then(async () => {
    try { await request.post("/api/v1/admin/users/" + row.id + "/reset-traffic"); ElMessage.success("流量已重置"); await fetchList() } catch { /* */ }
  }).catch(() => {})
}

const toggleStatus = async (row: any) => {
  const newStatus = row.status === "active" ? "disabled" : "active"
  try { await request.post("/api/v1/admin/users/" + row.id + "/status", { status: newStatus }); ElMessage.success(newStatus === "active" ? "已启用" : "已禁用"); await fetchList() } catch { /* */ }
}

// 开通套餐
const activateVisible = ref(false)
const activateRow = ref<UserRow | null>(null)
const activatePlanId = ref("")
const activating = ref(false)

const openActivateDialog = (row: any) => {
  activateRow.value = row
  activatePlanId.value = row.plan_id || ""
  activateVisible.value = true
}

const confirmActivate = async () => {
  if (!activateRow.value || !activatePlanId.value) {
    ElMessage.warning("请选择套餐")
    return
  }
  activating.value = true
  try {
    await request.post("/api/v1/admin/users/" + activateRow.value.id + "/activate-plan", { plan_id: activatePlanId.value })
    ElMessage.success("套餐已开通")
    activateVisible.value = false
    await fetchList()
  } catch { /* */ }
  finally { activating.value = false }
}

const fetchList = async () => {
  loading.value = true
  try {
    const res: any = await request.get("/api/v1/admin/users")
    let arr: any[] = []
    if (Array.isArray(res)) arr = res
    else if (res && Array.isArray(res.data)) arr = res.data
    else if (res && res.data && Array.isArray(res.data.list)) arr = res.data.list
    list.value = arr
  } catch { /* */ }
  finally { loading.value = false }
}

const fetchPlans = async () => {
  try {
    const res: any = await request.get("/api/v1/admin/plans")
    let arr: any[] = []
    if (res && res.data && Array.isArray(res.data.list)) arr = res.data.list
    else if (res && Array.isArray(res.data)) arr = res.data
    planList.value = arr
  } catch { /* */ }
}

const copySubUrl = (url: any) => { navigator.clipboard.writeText(url).then(() => { ElMessage.success("已复制订阅链接") }).catch(() => { ElMessage.error("复制失败") }) }

onMounted(async () => { await fetchPlans(); await fetchList() })
</script>

<style scoped>
.page-card { padding: 20px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px; flex-wrap: wrap; gap: 12px; }
.page-title { margin: 0; font-size: 18px; color: var(--np-text); }
.page-desc { margin: 6px 0 0; font-size: 13px; color: var(--np-text-secondary); }
.header-actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.traffic-cell { display: flex; flex-direction: column; gap: 4px; }
.traffic-text { font-size: 12px; color: var(--np-text-secondary); }
.expired { color: var(--np-danger); }
.form-tip { font-size: 12px; color: var(--np-text-muted); }
</style>
