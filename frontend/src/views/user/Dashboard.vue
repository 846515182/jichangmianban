<template>
  <div class="user-dashboard">
    <!-- 服务器实时状态（精简版） -->
    <el-row :gutter="20" style="margin-bottom: 20px">
      <el-col :span="24">
        <div class="np-card dash-card">
          <div class="card-title">服务器状态</div>
          <el-row :gutter="16">
            <el-col :xs="12" :sm="6">
              <div class="mini-stat">
                <div class="mini-label">CPU 负载</div>
                <div class="mini-value">{{ sysStats.load1?.toFixed(2) ?? '0.00' }}</div>
                <el-progress :percentage="loadPct" :show-text="false" :stroke-width="4" :status="loadPct >= 80 ? 'exception' : 'success'" />
              </div>
            </el-col>
            <el-col :xs="12" :sm="6">
              <div class="mini-stat">
                <div class="mini-label">内存</div>
                <div class="mini-value">{{ sysStats.mem_pct?.toFixed(0) ?? 0 }}%</div>
                <el-progress :percentage="memPct" :show-text="false" :stroke-width="4" :status="memPct >= 85 ? 'exception' : (memPct >= 70 ? 'warning' : 'success')" />
              </div>
            </el-col>
            <el-col :xs="12" :sm="6">
              <div class="mini-stat">
                <div class="mini-label">实时下行</div>
                <div class="mini-value down">{{ formatSpeed(sysStats.net_in_bps) }}</div>
              </div>
            </el-col>
            <el-col :xs="12" :sm="6">
              <div class="mini-stat">
                <div class="mini-label">实时上行</div>
                <div class="mini-value up">{{ formatSpeed(sysStats.net_out_bps) }}</div>
              </div>
            </el-col>
          </el-row>
        </div>
      </el-col>
    </el-row>

    <el-row :gutter="20">
      <!-- 流量用量环形图 -->
      <el-col :xs="24" :md="10">
        <div class="np-card dash-card">
          <div class="card-title">流量用量</div>
          <v-chart class="gauge-chart" :option="trafficOption" autoresize />
          <div class="traffic-detail">
            <div class="detail-item">
              <span class="detail-label">已用</span>
              <span class="detail-value used">{{ formatTraffic(trafficUsed) }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">总量</span>
              <span class="detail-value">{{ trafficLimit ? formatTraffic(trafficLimit) : '不限' }}</span>
            </div>
            <div class="detail-item">
              <span class="detail-label">剩余</span>
              <span class="detail-value remain">{{ formatTraffic(remainTraffic) }}</span>
            </div>
          </div>
        </div>
      </el-col>

      <!-- 账号信息 -->
      <el-col :xs="24" :md="14">
        <div class="np-card dash-card">
          <div class="card-title">账号信息</div>
          <div class="info-grid">
            <div class="info-item">
              <div class="info-label"><el-icon><User /></el-icon> 用户名</div>
              <div class="info-value">{{ username }}</div>
            </div>
            <div class="info-item">
              <div class="info-label"><el-icon><Message /></el-icon> 邮箱</div>
              <div class="info-value">{{ email || '-' }}</div>
            </div>
            <div class="info-item">
              <div class="info-label"><el-icon><Calendar /></el-icon> 到期时间</div>
              <div class="info-value" :class="{ expired: isExpired }">
                {{ expiredAt ? formatTime(expiredAt) : '永久' }}
                <el-tag v-if="isExpired" size="small" type="danger" effect="dark">已过期</el-tag>
                <el-tag v-else-if="expiredAt" size="small" type="success" effect="dark">剩余{{ daysLeft }}天</el-tag>
              </div>
            </div>
            <div class="info-item">
              <div class="info-label"><el-icon><CircleCheck /></el-icon> 状态</div>
              <div class="info-value">
                <el-tag size="small" :type="status === 'active' ? 'success' : 'danger'" effect="plain">
                  {{ status === 'active' ? '正常' : '已禁用' }}
                </el-tag>
              </div>
            </div>
          </div>
        </div>
      </el-col>
    </el-row>

    <!-- 订阅链接 + 快速导入 -->
    <el-row :gutter="20" style="margin-top: 20px">
      <el-col :span="24">
        <div class="np-card dash-card">
          <div class="card-title">订阅链接 & 快速导入</div>
          <el-input :model-value="subscribeUrl" readonly size="large">
            <template #append>
              <el-button @click="copyLink"><el-icon><CopyDocument /></el-icon>复制</el-button>
            </template>
          </el-input>
          <div class="import-guide">
            <span class="guide-label">一键导入客户端：</span>
            <div class="client-btns">
              <el-button v-for="c in clients" :key="c.name" @click="quickImport(c)">
                <el-icon><component :is="c.icon" /></el-icon>{{ c.name }}
              </el-button>
            </div>
          </div>
        </div>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import VChart from 'vue-echarts'
import '@/utils/echarts'
import { ElMessage } from 'element-plus'
import { formatTraffic, formatTime, formatSpeed, daysUntil } from '@/utils/format'
import request from '@/utils/request'

interface ApiResponse<T> { code: number; msg: string; data: T }
interface UserInfoResp {
  id: string
  username: string
  email: string
  traffic_limit: number
  traffic_used: number
  expired_at: string
  status: string
  subscribe_url: string
}

const username = ref('')
const email = ref('')
const trafficLimit = ref(0)
const trafficUsed = ref(0)
const expiredAt = ref('')
const status = ref('active')
const subscribeUrl = ref('')

const remainTraffic = computed(() => {
  if (!trafficLimit.value) return Infinity
  return Math.max(0, trafficLimit.value - trafficUsed.value)
})

const isExpired = computed(() => {
  if (!expiredAt.value) return false
  return new Date(expiredAt.value).getTime() < Date.now()
})
const daysLeft = computed(() => daysUntil(expiredAt.value))

const trafficPercent = computed(() => {
  if (!trafficLimit.value) return 0
  if (trafficUsed.value === 0) return 0
  const pct = (trafficUsed.value / trafficLimit.value) * 100
  return Math.max(0.1, Math.min(100, Math.round(pct * 10) / 10))
})

// === 系统状态（精简版：用户端） ===
const sysStats = ref({
  load1: 0, load5: 0, load15: 0,
  mem_total: 0, mem_used: 0, mem_pct: 0,
  net_in_bps: 0, net_out_bps: 0,
  uptime_sec: 0, hostname: '',
})
const loadPct = computed(() => Math.min(100, Math.round((sysStats.value.load1 || 0) * 25)))
const memPct = computed(() => Math.round(sysStats.value.mem_pct || 0))

// 环形图配置
const trafficOption = computed(() => ({
  series: [
    {
      type: 'pie',
      radius: ['68%', '85%'],
      center: ['50%', '50%'],
      silent: true,
      label: {
        show: true,
        position: 'center',
        formatter: `{a|${trafficPercent.value.toFixed(1)}%}\n{b|已使用}`,
        rich: {
          a: { fontSize: 32, fontWeight: 700, color: '#00f5d4', lineHeight: 40 },
          b: { fontSize: 13, color: '#8b98a9' },
        },
      },
      data: [
        { value: trafficPercent.value, itemStyle: { color: trafficPercent.value >= 90 ? '#ff006e' : '#00f5d4' } },
        { value: 100 - trafficPercent.value, itemStyle: { color: '#1e2a3a' } },
      ],
    },
  ],
}))

const clients = [
  { name: 'Clash', icon: 'Link' },
  { name: 'Sing-Box', icon: 'Connection' },
  { name: 'V2RayN', icon: 'Monitor' },
  { name: 'Shadowrocket', icon: 'Iphone' },
]

// 兼容 HTTP（非安全上下文）环境的复制
const fallbackCopy = (text: string): boolean => {
  try {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.left = '-9999px'
    textarea.style.top = '0'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.focus()
    textarea.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(textarea)
    return ok
  } catch {
    return false
  }
}
const copyToClipboard = (text: string): Promise<boolean> => {
  if (window.isSecureContext && navigator.clipboard) {
    return navigator.clipboard.writeText(text).then(() => true).catch(() => fallbackCopy(text))
  }
  return Promise.resolve(fallbackCopy(text))
}

const copyLink = async () => {
  if (!subscribeUrl.value) {
    ElMessage.warning('订阅链接为空')
    return
  }
  const ok = await copyToClipboard(subscribeUrl.value)
  if (ok) {
    ElMessage.success('订阅链接已复制')
  } else {
    ElMessage.warning('复制失败，请手动复制')
  }
}

const quickImport = async (c: { name: string }) => {
  if (!subscribeUrl.value) {
    ElMessage.warning('订阅链接为空')
    return
  }
  const encoded = encodeURIComponent(subscribeUrl.value)
  const importLinks: Record<string, string> = {
    'Clash': `clash://install-config?url=${encoded}`,
    'Sing-Box': `sing-box://import-remote-profile?url=${encoded}`,
    'V2RayN': subscribeUrl.value,
    'Shadowrocket': `shadowrocket://add/sub?subscribe=${encoded}`,
  }
  const link = importLinks[c.name] || subscribeUrl.value
  const ok = await copyToClipboard(link)
  if (ok) {
    ElMessage.success(`已为 ${c.name} 复制导入链接，请打开客户端自动导入`)
  } else {
    ElMessage.success(`请复制订阅链接后手动导入 ${c.name}`)
  }
}

const fetchUserInfo = async () => {
  try {
    const res = await request.get<ApiResponse<UserInfoResp>>('/api/v1/user/info')
    if (res && res.code === 0 && res.data) {
      username.value = res.data.username || ''
      email.value = res.data.email || ''
      trafficLimit.value = res.data.traffic_limit || 0
      trafficUsed.value = res.data.traffic_used || 0
      expiredAt.value = res.data.expired_at || ''
      status.value = res.data.status || 'active'
      subscribeUrl.value = res.data.subscribe_url || ''
    }
  } catch { /* 拦截器处理 */ }
}

let sysTimer: number | null = null
const fetchSysStats = async () => {
  try {
    const res: any = await request.get('/api/v1/user/system/stats')
    if (res && res.code === 0 && res.data) {
      sysStats.value = { ...sysStats.value, ...res.data }
    }
  } catch { /* fallback */ }
}

onMounted(() => {
  fetchUserInfo()
  fetchSysStats()
  sysTimer = window.setInterval(fetchSysStats, 3000)
})

onBeforeUnmount(() => {
  if (sysTimer !== null) {
    clearInterval(sysTimer)
    sysTimer = null
  }
})
</script>

<style scoped>
.dash-card { padding: 20px; height: 100%; box-sizing: border-box; }
.card-title { font-size: 15px; font-weight: 600; color: var(--np-text); margin-bottom: 16px; }
.mini-stat { display: flex; flex-direction: column; gap: 6px; padding: 4px 0; }
.mini-label { font-size: 12px; color: var(--np-text-muted); }
.mini-value { font-size: 18px; font-weight: 700; color: var(--np-text); font-family: monospace; }
.mini-value.up { color: #ffbe0b; }
.mini-value.down { color: var(--np-primary); }
.gauge-chart { height: 240px; width: 100%; }
.traffic-detail { display: flex; justify-content: space-around; margin-top: 12px; }
.detail-item { text-align: center; }
.detail-label { font-size: 12px; color: var(--np-text-muted); margin-bottom: 6px; }
.detail-value { font-size: 16px; font-weight: 600; color: var(--np-text); }
.detail-value.used { color: var(--np-warning); }
.detail-value.remain { color: var(--np-primary); }

.info-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; }
.info-item { display: flex; flex-direction: column; gap: 8px; }
.info-label { display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--np-text-secondary); }
.info-value { font-size: 15px; color: var(--np-text); display: flex; align-items: center; gap: 8px; }
.expired { color: var(--np-danger); }

.import-guide { margin-top: 20px; display: flex; align-items: center; gap: 16px; flex-wrap: wrap; }
.guide-label { color: var(--np-text-secondary); font-size: 14px; }
.client-btns { display: flex; gap: 10px; flex-wrap: wrap; }
</style>
