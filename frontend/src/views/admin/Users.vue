<template>
  <div class="admin-users">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">用户管理</h2>
          <p class="page-desc">管理用户账号、流量配额与状态</p>
        </div>
        <div class="header-actions">
          <el-input v-model="keyword" placeholder="搜索用户名/邮箱" :prefix-icon="Search" clearable style="width: 220px" @keyup.enter="onKeywordChange" @clear="onKeywordChange" />
          <el-button type="primary" @click="openDialog()"><el-icon><Plus /></el-icon>新增用户</el-button>
        </div>
      </div>

      <div class="table-wrap">
        <el-table :data="filteredList" stripe v-loading="loading">
          <el-table-column prop="username" label="用户名" min-width="100" />
          <el-table-column prop="email" label="邮箱" min-width="160" show-overflow-tooltip />
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
          <el-table-column label="操作" width="420" fixed="right">
            <template #default="{ row }">
              <el-button size="small" link type="primary" @click="openDialog(row)">编辑</el-button>
              <el-button size="small" link type="success" @click="openActivateDialog(row)">开通套餐</el-button>
              <el-button size="small" link @click="resetTraffic(row)">重置流量</el-button>
              <el-button size="small" link :type="row.status === 'active' ? 'warning' : 'success'" @click="toggleStatus(row)">
                {{ row.status === 'active' ? '禁用' : '启用' }}
              </el-button>
              <el-button size="small" link type="danger" @click="handleDelete(row)">删除</el-button>
              <el-button size="small" link type="danger" @click="handleHardDelete(row)">彻底删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>

      <div class="pagination-wrap">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[10, 20, 50, 100]"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @current-change="fetchList"
          @size-change="onSizeChange"
        />
      </div>
    </div>

    <el-dialog v-model="dialogVisible" :title="editing ? '编辑用户' : '新增用户'" width="520px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" :disabled="!!editing" />
        </el-form-item>
        <el-form-item label="邮箱" prop="email">
          <el-input v-model="form.email" :disabled="!!editing" placeholder="user@example.com" />
        </el-form-item>
        <el-form-item v-if="!editing" label="密码" prop="password">
          <el-input v-model="form.password" type="password" show-password />
        </el-form-item>
        <el-form-item label="流量配额(GB)">
          <el-input-number v-model="form.trafficLimitGB" :min="0" controls-position="right" style="width:100%" />
          <span class="form-tip">0 表示无限</span>
        </el-form-item>
        <el-form-item label="到期时间">
          <el-date-picker v-model="form.expireAt" type="datetime" placeholder="选择到期时间(留空=清除/不限)" style="width:100%" value-format="YYYY-MM-DD HH:mm:ss" clearable />
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

// 修复 P1: 分页状态。旧版无分页组件, 后端默认 size=20, 第 21 条之后不可见。
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)

// 修复 P1: 旧版 keyword 只在前端过滤当前页, 第 21 条之后搜不到。
// 现改为后端搜索(后端 UserRepo.List 支持 keyword 模糊匹配 username/email)。
const filteredList = computed(() => list.value)

// 搜索输入时回到第 1 页重新加载
const onKeywordChange = () => {
  currentPage.value = 1
  fetchList()
}

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
// P2-19: 移除 form.plan_id 死字段(编辑对话框无套餐选择控件, 套餐变更走单独"开通套餐"对话框)
const form = reactive({
  username: "", email: "", password: "",
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
      trafficLimitGB: row.traffic_limit ? Math.round(row.traffic_limit / 1024 / 1024 / 1024) : 0,
      expireAt: row.expired_at || "",
    })
  } else {
    Object.assign(form, { username: "", email: "", password: "", trafficLimitGB: 100, expireAt: "" })
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
        // 修复 P2: 旧版 Math.floor 会少算最多近 1 字节, 用 Math.round 四舍五入更精确
        traffic_limit: Math.round(form.trafficLimitGB * 1024 * 1024 * 1024),
      }
      // P2-19: plan_id 不在编辑表单暴露, 套餐变更走单独"开通套餐"对话框
      // 到期时间: 留空=清除到期(expire_days=0), 有值=按天数(允许负数设已过期)
      // 修复 P1: 旧版 Math.max(1, Math.ceil(...)) 把过去日期也变成+1天(无法设已过期),
      // 且无法清除到期时间(不发 expire_days=不改)。改用 round 提高精度 + 允许负数 + 空值发0。
      if (form.expireAt) {
        const diffMs = new Date(form.expireAt).getTime() - Date.now()
        payload.expire_days = Math.round(diffMs / (24 * 3600 * 1000))
      } else if (editing.value) {
        // 编辑时清空到期时间 → 发 0 清除(创建时不发, 默认不限)
        payload.expire_days = 0
      }
      if (editing.value) {
        if (form.password) payload.password = form.password
        await request.put("/api/v1/admin/users/" + editing.value.id, payload)
        ElMessage.success("用户已更新")
      } else {
        payload.password = form.password
        // 修复 P2: 旧版 if (res.code === 0) 是死代码, request 拦截器对 code !== 0 已 reject,
        // 不会走到 else 分支。直接弹成功即可。
        await request.post("/api/v1/admin/users", payload)
        ElMessage.success("用户已创建")
      }
      dialogVisible.value = false
      await fetchList()
    } catch (e: any) { /* error handled by interceptor */ }
    finally { saving.value = false }
  })
}

const handleDelete = (row: any) => {
  ElMessageBox.confirm("确定删除用户「" + row.username + "」吗？(软删除, 数据保留可审计)", "提示", { type: "warning" }).then(async () => {
    try { await request.delete("/api/v1/admin/users/" + row.id); ElMessage.success("用户已删除"); await fetchList() } catch { /* */ }
  }).catch(() => {})
}

// 彻底删除(物理删除, 释放 username/email 唯一索引, 重新注册不冲突)
// 仅用于测试数据清理, 数据不可恢复
const handleHardDelete = (row: any) => {
  ElMessageBox.confirm(
    "确定【彻底删除】用户「" + row.username + "」吗？\n\n" +
    "彻底删除会从数据库物理移除该用户及其关联数据(订阅/流量日志/user_nodes),\n" +
    "释放 username 和 email 唯一索引, 之后可用相同用户名/邮箱重新注册。\n\n" +
    "⚠️ 此操作不可恢复, 仅建议用于测试账号清理。",
    "彻底删除确认",
    { type: "error", confirmButtonText: "确认彻底删除", cancelButtonText: "取消" }
  ).then(async () => {
    try {
      await request.delete("/api/v1/admin/users/" + row.id + "/hard")
      ElMessage.success("已彻底删除, 该账号的 username/email 已释放可重新注册")
      await fetchList()
    } catch (e: any) {
      ElMessage.error(e?.response?.data?.msg || "彻底删除失败")
    }
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
    // 修复 P1: 加分页 + keyword 参数, 旧版无分页 + 仅前端过滤
    const res: any = await request.get("/api/v1/admin/users", {
      params: {
        page: currentPage.value,
        size: pageSize.value,
        keyword: keyword.value || undefined,
      },
    })
    // P2-18: 后端统一返回 {code,msg,data:{list,total}}, 移除永不触发的多分支死代码
    const arr = (res && res.data && Array.isArray(res.data.list)) ? res.data.list : []
    total.value = (res && res.data && Number(res.data.total)) || arr.length
    list.value = arr
  } catch { /* */ }
  finally { loading.value = false }
}

// 修复 P1: 分页切换处理
const onSizeChange = (size: number) => {
  pageSize.value = size
  currentPage.value = 1
  fetchList()
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
.pagination-wrap { margin-top: 16px; display: flex; justify-content: flex-end; }

@media (max-width: 768px) {
  .page-card { padding: 14px; }
  .page-header { flex-direction: column; align-items: stretch; }
  .header-actions { flex-direction: column; align-items: stretch; width: 100%; }
  .header-actions .el-input,
  .header-actions .el-button {
    width: 100% !important;
    margin-left: 0 !important;
  }
  .header-actions .el-button + .el-button {
    margin-left: 0 !important;
    margin-top: 8px;
  }
  .pagination-wrap { justify-content: center; }
}
</style>
