<template>
  <div class="user-profile">
    <el-row :gutter="20">
      <!-- 个人信息 + 修改密码 -->
      <el-col :xs="24" :md="12">
        <div class="np-card profile-card">
          <div class="card-title"><el-icon><User /></el-icon> 个人信息</div>
          <div class="profile-avatar">
            <el-avatar :size="64" class="np-avatar">{{ avatarText }}</el-avatar>
            <div>
              <div class="profile-name">{{ userInfo.username }}</div>
              <div class="profile-email">{{ userInfo.email }}</div>
            </div>
          </div>

          <div class="profile-stats">
            <div class="stat-item">
              <span class="stat-label">流量使用</span>
              <span class="stat-value">{{ formatTraffic(userInfo.traffic_used) }}</span>
            </div>
            <div class="stat-item">
              <span class="stat-label">流量配额</span>
              <span class="stat-value">{{ userInfo.traffic_limit ? formatTraffic(userInfo.traffic_limit) : '不限' }}</span>
            </div>
            <div class="stat-item">
              <span class="stat-label">到期时间</span>
              <span class="stat-value">{{ userInfo.expired_at ? formatTime(userInfo.expired_at, 'YYYY-MM-DD') : '永久' }}</span>
            </div>
          </div>

          <el-divider />

          <div class="card-title"><el-icon><Lock /></el-icon> 修改密码</div>
          <el-form ref="pwdFormRef" :model="pwdForm" :rules="pwdRules" label-width="100px">
            <el-form-item label="原密码" prop="oldPwd">
              <el-input v-model="pwdForm.oldPwd" type="password" show-password />
            </el-form-item>
            <el-form-item label="新密码" prop="newPwd">
              <el-input v-model="pwdForm.newPwd" type="password" show-password />
            </el-form-item>
            <el-form-item label="确认密码" prop="confirmPwd">
              <el-input v-model="pwdForm.confirmPwd" type="password" show-password />
            </el-form-item>
            <el-form-item>
              <el-button type="primary" @click="changePwd" :loading="changing">确认修改</el-button>
            </el-form-item>
          </el-form>
        </div>
      </el-col>

      <!-- 登录记录 + 注销设备 -->
      <el-col :xs="24" :md="12">
        <div class="np-card profile-card">
          <div class="card-title">
            <span><el-icon><Monitor /></el-icon> 登录设备记录</span>
            <el-button type="danger" size="small" plain @click="logoutAll">注销所有设备</el-button>
          </div>
          <div class="device-list">
            <div v-for="(d, i) in recentDevices" :key="i" class="device-item">
              <div class="device-icon">
                <el-icon><Monitor v-if="d.type === 'pc'" /><Iphone v-else /></el-icon>
              </div>
              <div class="device-info">
                <div class="device-name">
                  {{ d.name }}
                  <el-tag v-if="i === 0" size="small" type="success" effect="dark">最近</el-tag>
                </div>
                <div class="device-meta">{{ d.ip }} · {{ d.location || '未知' }} · {{ formatRelative(d.lastActive) }}</div>
              </div>
            </div>
            <el-empty v-if="!recentDevices.length" description="暂无登录记录" :image-size="60" />
          </div>

          <el-divider />

          <div class="card-title"><el-icon><Clock /></el-icon> 最近登录记录</div>
          <div class="table-wrap">
            <el-table :data="loginRecords" stripe size="small">
              <el-table-column prop="ip" label="IP" min-width="120" />
              <el-table-column prop="location" label="位置" min-width="100">
                <template #default="{ row }">{{ row.location || '未知' }}</template>
              </el-table-column>
              <el-table-column label="状态" width="80">
                <template #default="{ row }">
                  <el-tag size="small" :type="row.success ? 'success' : 'danger'" effect="plain">
                    {{ row.success ? '成功' : '失败' }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="时间" width="160">
                <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import request from '@/utils/request'
import { useAuthStore } from '@/stores/auth'
import { formatTime, formatRelative, formatTraffic } from '@/utils/format'

interface ApiResponse<T> { code: number; msg: string; data: T }
interface UserInfoResp {
  id: string
  username: string
  email: string
  traffic_limit: number
  traffic_used: number
  expired_at: string
  status: string
}
interface LoginLog {
  ip: string
  location: string
  success: boolean
  user_agent?: string
  created_at: string
}

const auth = useAuthStore()
const userInfo = ref({
  username: auth.userInfo?.username || '',
  email: auth.userInfo?.email || '',
  traffic_limit: 0,
  traffic_used: 0,
  expired_at: '',
})

// 从后端加载最新用户信息
// 修复 P1-FE9: 旧版只更新本地 userInfo ref, 不同步到 auth store, 导致侧边栏/导航中
// 显示的用户名/邮箱仍是登录时的旧值, 用户改完资料看不到效果以为没保存。
// 现在拉取成功后同步到 auth store.setUserInfo, 全局感知。
const fetchUserInfo = async () => {
  try {
    const res = await request.get<ApiResponse<UserInfoResp>>('/api/v1/user/info')
    if (res && res.code === 0 && res.data) {
      userInfo.value.username = res.data.username || ''
      userInfo.value.email = res.data.email || ''
      userInfo.value.traffic_limit = res.data.traffic_limit || 0
      userInfo.value.traffic_used = res.data.traffic_used || 0
      userInfo.value.expired_at = res.data.expired_at || ''
      // 同步到 auth store, 让 Header/侧边栏等全局组件感知最新用户信息
      // 仅在已有 userInfo 基础上更新字段, 避免覆盖 role 等其他字段
      if (auth.userInfo) {
        auth.setUserInfo({
          ...auth.userInfo,
          username: userInfo.value.username,
          email: userInfo.value.email,
          trafficUsed: userInfo.value.traffic_used,
          trafficLimit: userInfo.value.traffic_limit,
          expireAt: userInfo.value.expired_at,
        })
      }
    }
  } catch { /* 拦截器处理 */ }
}

// 从后端加载登录记录
const loginRecords = ref<LoginLog[]>([])
const fetchLoginLogs = async () => {
  try {
    const res = await request.get<ApiResponse<{ list: LoginLog[] }>>('/api/v1/user/login-logs')
    if (res && res.code === 0 && res.data) {
      const arr = res.data.list || (res.data as any)
      loginRecords.value = Array.isArray(arr) ? arr.slice(0, 10) : []
    }
  } catch { /* 拦截器处理 */ }
}

// 基于登录记录派生"登录设备"列表(取最近成功的登录, 按 IP 去重)
const recentDevices = computed(() => {
  const seen = new Set<string>()
  const devices: { name: string; type: string; ip: string; location: string; lastActive: number }[] = []
  for (const r of loginRecords.value) {
    if (!r.ip || seen.has(r.ip)) continue
    seen.add(r.ip)
    // 根据 user_agent 简单判断设备类型
    const ua = (r as any).user_agent || ''
    let name = '未知设备'
    let type = 'pc'
    if (/mobile|android|iphone/i.test(ua)) {
      type = 'mobile'
      name = /iphone/i.test(ua) ? 'iPhone' : /android/i.test(ua) ? 'Android' : '移动设备'
    } else if (/windows/i.test(ua)) {
      name = 'Windows'
    } else if (/mac/i.test(ua)) {
      name = 'macOS'
    } else if (/linux/i.test(ua)) {
      name = 'Linux'
    }
    // 提取浏览器
    if (/chrome/i.test(ua)) name += ' · Chrome'
    else if (/firefox/i.test(ua)) name += ' · Firefox'
    else if (/safari/i.test(ua)) name += ' · Safari'
    else if (/edge/i.test(ua)) name += ' · Edge'
    devices.push({
      name,
      type,
      ip: r.ip,
      location: r.location || '',
      lastActive: new Date(r.created_at).getTime(),
    })
    if (devices.length >= 5) break
  }
  return devices
})

onMounted(() => {
  fetchUserInfo()
  fetchLoginLogs()
})

const avatarText = computed(() => (userInfo.value.username || 'U').charAt(0).toUpperCase())

// 修改密码
const pwdFormRef = ref<FormInstance>()
const changing = ref(false)
const pwdForm = reactive({ oldPwd: '', newPwd: '', confirmPwd: '' })
const pwdRules: FormRules = {
  oldPwd: [{ required: true, message: '请输入原密码', trigger: 'blur' }],
  newPwd: [{ required: true, message: '请输入新密码', trigger: 'blur' }, { min: 6, message: '密码至少6位', trigger: 'blur' }],
  confirmPwd: [{ required: true, message: '请确认密码', trigger: 'blur' }, {
    validator: (_r, v, cb) => { v !== pwdForm.newPwd ? cb(new Error('两次密码不一致')) : cb() }, trigger: 'blur',
  }],
}

const changePwd = async () => {
  if (!pwdFormRef.value) return
  await pwdFormRef.value.validate(async (valid) => {
    if (!valid) return
    changing.value = true
    try {
      await request.post('/api/v1/auth/change-password', { old_password: pwdForm.oldPwd, new_password: pwdForm.newPwd })
      ElMessage.success('密码修改成功')
      Object.assign(pwdForm, { oldPwd: '', newPwd: '', confirmPwd: '' })
    } catch { /* 错误由 request 拦截器自动提示 */ } finally {
      changing.value = false
    }
  })
}

const logoutAll = () => {
  ElMessageBox.confirm('确定注销所有设备吗？您需要重新登录。', '危险操作', { type: 'warning' }).then(async () => {
    try { await request.post('/api/v1/auth/logout-all') } catch { /* */ }
    await auth.logout()
    ElMessage.success('所有设备已注销')
    location.href = '/login'
  }).catch(() => {})
}
</script>

<style scoped>
.profile-card { padding: 20px; height: 100%; box-sizing: border-box; }
.card-title { display: flex; align-items: center; justify-content: space-between; gap: 8px; font-size: 15px; font-weight: 600; color: var(--np-text); margin-bottom: 16px; }
.card-title span { display: flex; align-items: center; gap: 8px; }
.profile-avatar { display: flex; align-items: center; gap: 16px; }
.np-avatar { background: var(--np-primary-dim); color: var(--np-primary); border: 2px solid var(--np-primary); font-size: 24px; }
.profile-name { font-size: 20px; font-weight: 700; color: var(--np-text); }
.profile-email { font-size: 13px; color: var(--np-text-secondary); margin-top: 4px; }
.profile-stats { display: flex; gap: 24px; margin-top: 16px; padding: 12px 0; border-top: 1px solid var(--np-border); }
.stat-item { display: flex; flex-direction: column; gap: 4px; }
.stat-label { font-size: 12px; color: var(--np-text-muted); }
.stat-value { font-size: 14px; font-weight: 600; color: var(--np-text); }

.device-list { display: flex; flex-direction: column; gap: 12px; }
.device-item { display: flex; align-items: center; gap: 12px; padding: 12px; background: var(--np-bg-soft); border-radius: 8px; }
.device-icon { width: 40px; height: 40px; border-radius: 8px; background: var(--np-card); border: 1px solid var(--np-border); display: flex; align-items: center; justify-content: center; color: var(--np-primary); font-size: 18px; }
.device-info { flex: 1; }
.device-name { font-size: 14px; color: var(--np-text); display: flex; align-items: center; gap: 8px; }
.device-meta { font-size: 12px; color: var(--np-text-muted); margin-top: 4px; }

@media (max-width: 768px) {
  .profile-card { padding: 14px; height: auto; }
  .profile-avatar { flex-direction: column; align-items: flex-start; gap: 12px; }
  .profile-name { font-size: 18px; }
  .profile-stats { flex-direction: column; gap: 12px; }
  .stat-item { flex-direction: row; justify-content: space-between; }
  .card-title { flex-wrap: wrap; gap: 10px; }
  .device-item { padding: 10px; }
  .device-name { flex-wrap: wrap; }
}
</style>
