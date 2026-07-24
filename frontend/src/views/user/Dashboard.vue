<template>
  <div class="user-dashboard">
    <!-- 节点实时流量（用户真正关心的：自己订阅节点的流量） -->
    <el-row :gutter="20" style="margin-bottom: 20px">
      <el-col :span="24">
        <div class="np-card dash-card">
          <div class="card-title">
            <span>节点实时流量</span>
            <el-tag v-if="nodesLoading" size="small" type="info" effect="plain">刷新中...</el-tag>
            <el-tag v-else size="small" :type="onlineNodes > 0 ? 'success' : 'danger'" effect="plain">
              {{ onlineNodes }} 个在线
            </el-tag>
          </div>
          <el-row :gutter="16">
            <el-col :xs="12" :sm="6">
              <div class="mini-stat">
                <div class="mini-label">在线节点</div>
                <div class="mini-value">{{ onlineNodes }} / {{ nodes.length }}</div>
              </div>
            </el-col>
            <el-col :xs="12" :sm="6">
              <div class="mini-stat">
                <div class="mini-label">总下行速率</div>
                <div class="mini-value down">{{ formatSpeed(totalDownBps) }}</div>
              </div>
            </el-col>
            <el-col :xs="12" :sm="6">
              <div class="mini-stat">
                <div class="mini-label">总上行速率</div>
                <div class="mini-value up">{{ formatSpeed(totalUpBps) }}</div>
              </div>
            </el-col>
            <el-col :xs="12" :sm="6">
              <div class="mini-stat">
                <div class="mini-label">近 5 分钟用量</div>
                <div class="mini-value">{{ formatTraffic(recent5mTotal) }}</div>
              </div>
            </el-col>
          </el-row>
          <!-- 节点流速明细（紧凑列表） -->
          <div class="node-speed-list" v-if="nodes.length">
            <div v-for="n in nodes" :key="n.id" class="speed-row" :class="{ offline: !n.online }">
              <span class="speed-name">
                <i class="np-dot" :class="n.online ? 'online' : 'offline'"></i>
                {{ n.name }}
              </span>
              <span class="speed-val down">{{ formatSpeed(nodeDownBps(n)) }}</span>
              <span class="speed-val up">{{ formatSpeed(nodeUpBps(n)) }}</span>
            </div>
          </div>
          <el-empty v-else description="暂无可用节点" :image-size="50" />
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
import { copyToClipboard } from '@/utils/clipboard'
import request from '@/utils/request'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

// 用户信息（从 auth store 读，避免重复请求）
const username = computed(() => auth.userInfo?.username || '')
const email = computed(() => auth.userInfo?.email || '')
const trafficLimit = computed(() => auth.userInfo?.trafficLimit || 0)
const trafficUsed = computed(() => auth.userInfo?.trafficUsed || 0)
const expiredAt = computed(() => auth.userInfo?.expireAt || '')
const status = computed(() => auth.userInfo?.status || 'active')
const subscribeUrl = computed(() => auth.subscribeUrl || '')

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

// === 节点实时流量 ===
interface NodeItem {
  id: string
  name: string
  online: boolean
  recent5m_up?: number
  recent5m_dn?: number
}
const nodes = ref<NodeItem[]>([])
const nodesLoading = ref(false)

const onlineNodes = computed(() => nodes.value.filter((n) => n.online).length)
// recent5m_up/dn 是近 5 分钟字节数，×8/300 转 bps
const nodeUpBps = (n: NodeItem) => (n.recent5m_up || 0) * 8 / 300
const nodeDownBps = (n: NodeItem) => (n.recent5m_dn || 0) * 8 / 300
const totalUpBps = computed(() => nodes.value.reduce((s, n) => s + nodeUpBps(n), 0))
const totalDownBps = computed(() => nodes.value.reduce((s, n) => s + nodeDownBps(n), 0))
const recent5mTotal = computed(() => nodes.value.reduce((s, n) => s + (n.recent5m_up || 0) + (n.recent5m_dn || 0), 0))

const fetchNodes = async () => {
  nodesLoading.value = true
  try {
    const res = await request.get<{ code: number; data: { list: NodeItem[] } }>('/api/v1/nodes/list')
    if (res && res.code === 0 && res.data) {
      nodes.value = res.data.list || []
    }
  } catch { /* */ }
  nodesLoading.value = false
}

const clients = [
  { name: 'Clash', icon: 'Link' },
  { name: 'Sing-Box', icon: 'Connection' },
  { name: 'V2RayN', icon: 'Monitor' },
  { name: 'Shadowrocket', icon: 'Iphone' },
]

const copyLink = async () => {
  if (!subscribeUrl.value) {
    ElMessage.warning('订阅链接为空')
    return
  }
  const ok = await copyToClipboard(subscribeUrl.value)
  ok ? ElMessage.success('订阅链接已复制') : ElMessage.warning('复制失败，请手动复制')
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

// 轮询 + visibilitychange 优化（标签页隐藏时暂停轮询，节省资源）
let nodeTimer: number | null = null
let isVisible = true
const handleVisibility = () => {
  const nowVisible = !document.hidden
  if (nowVisible === isVisible) return
  isVisible = nowVisible
  if (isVisible) {
    fetchNodes()
    nodeTimer = window.setInterval(fetchNodes, 10000)
  } else if (nodeTimer !== null) {
    clearInterval(nodeTimer)
    nodeTimer = null
  }
}

onMounted(() => {
  // 强制刷新用户信息（Dashboard 是首页，确保拿到最新数据）
  auth.fetchUserInfo(true)
  fetchNodes()
  nodeTimer = window.setInterval(fetchNodes, 10000)
  document.addEventListener('visibilitychange', handleVisibility)
})

onBeforeUnmount(() => {
  if (nodeTimer !== null) {
    clearInterval(nodeTimer)
    nodeTimer = null
  }
  document.removeEventListener('visibilitychange', handleVisibility)
})
</script>

<style scoped>
.dash-card { padding: 20px; height: 100%; box-sizing: border-box; }
.card-title { font-size: 15px; font-weight: 600; color: var(--np-text); margin-bottom: 16px; display: flex; align-items: center; gap: 10px; }
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

/* 节点流速列表 */
.node-speed-list { margin-top: 16px; display: flex; flex-direction: column; gap: 6px; }
.speed-row { display: grid; grid-template-columns: 1fr auto auto; gap: 16px; align-items: center; padding: 8px 12px; background: rgba(255,255,255,0.03); border-radius: 8px; }
.speed-row.offline { opacity: 0.5; }
.speed-name { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--np-text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.speed-val { font-size: 13px; font-weight: 600; font-family: monospace; min-width: 90px; text-align: right; }
.speed-val.up { color: #ffbe0b; }
.speed-val.down { color: var(--np-primary); }
.np-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.np-dot.online { background: #67c23a; box-shadow: 0 0 6px #67c23a; }
.np-dot.offline { background: #909399; }

@media (max-width: 768px) {
  .dash-card { padding: 14px; }
  .card-title { font-size: 14px; margin-bottom: 12px; }
  .mini-stat { padding: 8px 0; }
  .mini-value { font-size: 15px; }
  .gauge-chart { height: 180px; }
  .traffic-detail { gap: 8px; }
  .detail-value { font-size: 14px; }
  .info-grid { grid-template-columns: 1fr; gap: 14px; }
  .info-value { flex-wrap: wrap; }
  .import-guide { flex-direction: column; align-items: flex-start; gap: 10px; }
  .client-btns { width: 100%; }
  .client-btns .el-button { flex: 1; min-width: 0; }
  .speed-row { gap: 8px; padding: 6px 8px; }
  .speed-name { font-size: 12px; }
  .speed-val { min-width: 60px; font-size: 11px; }
}
</style>
