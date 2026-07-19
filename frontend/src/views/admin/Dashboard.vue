<template>
  <div class="admin-dashboard">
    <el-row :gutter="20" class="stat-row">
      <el-col :xs="12" :sm="12" :md="6" v-for="card in statCards" :key="card.label">
        <div class="stat-card np-card">
          <div class="stat-icon" :style="{ background: card.bg, color: card.color }">
            <el-icon :size="24"><component :is="card.icon" /></el-icon>
          </div>
          <div class="stat-info">
            <div class="stat-label">{{ card.label }}</div>
            <div class="stat-value">{{ card.value }}</div>
          </div>
        </div>
      </el-col>
    </el-row>

    <!-- 面板监控：CPU/内存/磁盘/网络速度(整合) -->
    <el-row :gutter="20" class="chart-row">
      <el-col :span="24">
        <div class="chart-card np-card">
          <div class="chart-header">
            <span class="chart-title">面板监控</span>
            <el-tag size="small" type="info" effect="dark">{{ sysStats.hostname || 'panel' }} · 已运行 {{ formatDuration(sysStats.uptime_sec) }}</el-tag>
          </div>
          <el-row :gutter="16">
            <el-col :xs="24" :sm="6">
              <div class="sys-block">
                <div class="sys-label">CPU 负载（1/5/15 min）</div>
                <div class="sys-value">{{ sysStats.load1?.toFixed(2) }} / {{ sysStats.load5?.toFixed(2) }} / {{ sysStats.load15?.toFixed(2) }}</div>
                <el-progress :percentage="loadPct" :status="loadPct >= 80 ? 'exception' : (loadPct >= 60 ? 'warning' : 'success')" :stroke-width="8" />
              </div>
            </el-col>
            <el-col :xs="24" :sm="6">
              <div class="sys-block">
                <div class="sys-label">内存使用率</div>
                <div class="sys-value">{{ formatTraffic(sysStats.mem_used) }} / {{ formatTraffic(sysStats.mem_total) }} · {{ sysStats.mem_pct?.toFixed(1) }}%</div>
                <el-progress :percentage="memPct" :status="memPct >= 85 ? 'exception' : (memPct >= 70 ? 'warning' : 'success')" :stroke-width="8" />
              </div>
            </el-col>
            <el-col :xs="24" :sm="6">
              <div class="sys-block">
                <div class="sys-label">磁盘使用率</div>
                <div class="sys-value">{{ formatTraffic(sysStats.disk_used) }} / {{ formatTraffic(sysStats.disk_total) }} · {{ sysStats.disk_pct?.toFixed(1) }}%</div>
                <el-progress :percentage="diskPct" :status="diskPct >= 90 ? 'exception' : (diskPct >= 75 ? 'warning' : 'success')" :stroke-width="8" />
              </div>
            </el-col>
            <el-col :xs="24" :sm="6">
              <div class="sys-block">
                <div class="sys-label">网络速度（↑上 / ↓下）</div>
                <div class="sys-value">
                  <span class="net-inline up">{{ formatSpeed(sysStats.net_out_bps) }}</span>
                  <span class="net-sep">/</span>
                  <span class="net-inline down">{{ formatSpeed(sysStats.net_in_bps) }}</span>
                </div>
                <div class="sys-sub">在线节点 {{ sysStats.online_nodes }}/{{ sysStats.total_nodes }} · 活跃用户 {{ sysStats.online_users }}/{{ sysStats.total_users }}</div>
              </div>
            </el-col>
          </el-row>
        </div>
      </el-col>
    </el-row>

    <el-row :gutter="20" class="chart-row">
      <el-col :xs="24" :md="14">
        <div class="chart-card np-card">
          <div class="chart-header">
            <span class="chart-title">流量趋势（最近7天）</span>
            <el-tag size="small" type="info" effect="dark">单位: 自动</el-tag>
          </div>
          <v-chart class="chart" :option="trafficOption" autoresize />
        </div>
      </el-col>
      <el-col :xs="24" :md="10">
        <div class="chart-card np-card">
          <div class="chart-header">
            <span class="chart-title">用户增长趋势</span>
          </div>
          <v-chart class="chart" :option="growthOption" autoresize />
        </div>
      </el-col>
    </el-row>
    <el-row :gutter="20" class="chart-row">
      <el-col :span="24">
        <div class="chart-card np-card">
          <div class="chart-header">
            <span class="chart-title">节点运行状态</span>
            <el-button text @click="$router.push('/admin/nodes')">查看全部</el-button>
          </div>
          <el-table :data="nodeList" stripe size="default" v-loading="nodeLoading">
            <el-table-column prop="name" label="节点名称" min-width="140" />
            <el-table-column prop="protocol" label="协议" width="120">
              <template #default="{ row }">
                <el-tag size="small" effect="dark">{{ (row.protocol || "").toUpperCase() }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="地址" min-width="180">
              <template #default="{ row }">{{ row.server_address }}:{{ row.port }}</template>
            </el-table-column>
            <el-table-column label="状态" width="100">
              <template #default="{ row }">
                <span class="np-flex" style="gap:6px;align-items:center;">
                  <i class="np-dot" :class="row.online ? 'online' : 'offline'"></i>
                  {{ row.online ? '在线' : '离线' }}
                </span>
              </template>
            </el-table-column>
            <el-table-column label="流量" min-width="140">
              <template #default="{ row }">{{ formatTraffic(row.traffic_used) }}</template>
            </el-table-column>
            <el-table-column label="最后在线" width="160">
              <template #default="{ row }">{{ row.last_seen_at ? formatTime(row.last_seen_at) : "-" }}</template>
            </el-table-column>
          </el-table>
          <el-empty v-if="!nodeLoading && nodeList.length === 0" description="暂无节点" :image-size="80" />
        </div>
      </el-col>
    </el-row>

    <!-- 服务日志监控: 容器列表 + 报错日志(每 30 分钟轮询一次) -->
    <el-row :gutter="20" class="chart-row">
      <el-col :span="24">
        <div class="chart-card np-card">
          <div class="chart-header">
            <span class="chart-title">服务日志监控</span>
            <el-tag v-if="logMonitor.available" size="small" type="success" effect="dark">
              ERROR {{ logStats.error_count }} · WARN {{ logStats.warn_count }} · 共 {{ logStats.total_lines }} 行
            </el-tag>
            <el-tag v-else size="small" type="info" effect="plain">docker 不可用</el-tag>
          </div>
          <div class="log-toolbar">
            <el-select v-model="logMonitor.selectedContainer" placeholder="选择容器" size="small" style="width: 240px" @change="onContainerChange">
              <el-option
                v-for="c in logMonitor.containers"
                :key="c.name"
                :label="`${c.name}  [${c.state}]`"
                :value="c.name"
              />
            </el-select>
            <el-radio-group v-model="logMonitor.level" size="small" @change="onFilterChange">
              <el-radio-button label="all">全部</el-radio-button>
              <el-radio-button label="error">仅报错</el-radio-button>
              <el-radio-button label="warn">警告+</el-radio-button>
            </el-radio-group>
            <el-select v-model="logMonitor.since" placeholder="时间窗口" size="small" style="width: 120px" @change="onFilterChange">
              <el-option label="最近 30 分钟" value="30m" />
              <el-option label="最近 1 小时" value="1h" />
              <el-option label="最近 6 小时" value="6h" />
              <el-option label="最近 24 小时" value="24h" />
            </el-select>
            <el-input-number v-model="logMonitor.tail" :min="50" :max="2000" :step="100" size="small" style="width: 130px" @change="onFilterChange" />
            <span class="log-tail-label">行数</span>
            <div class="log-toolbar-spacer"></div>
            <el-button size="small" type="primary" @click="fetchContainerLogs" :loading="logMonitor.loading">
              <el-icon><Refresh /></el-icon><span>刷新</span>
            </el-button>
            <el-button size="small" :type="logMonitor.autoRefresh ? 'success' : 'info'" @click="toggleAutoRefresh">
              <el-icon><Timer /></el-icon><span>{{ logMonitor.autoRefresh ? `自动(${logMonitor.intervalLabel})` : '自动刷新' }}</span>
            </el-button>
            <el-button size="small" @click="copyLogs">
              <el-icon><CopyDocument /></el-icon><span>复制</span>
            </el-button>
          </div>
          <div class="log-stats-bar" v-if="logMonitor.lastFetch">
            <span class="log-stats-text">
              最后拉取: {{ formatTime(logMonitor.lastFetch) }}
              <span v-if="logStats.cached" class="log-stats-cached">(命中缓存)</span>
            </span>
            <span class="log-stats-text" v-if="logStats.failed" style="color: var(--np-danger)">
              ⚠ 拉取失败(容器可能已停止或 docker 不可用)
            </span>
          </div>
          <pre class="log-view" ref="logViewRef">{{ logMonitor.filteredLogs || '请选择容器查看日志…' }}</pre>
        </div>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, reactive, onMounted, onBeforeUnmount } from "vue"
import VChart from "vue-echarts"
import "@/utils/echarts"
import request from "@/utils/request"
import { formatTraffic, formatTime, formatSpeed, formatDuration } from "@/utils/format"
import { chartColors } from "@/utils/echarts"
import { mockDashboardStats } from "@/mock/data"
import { ElMessage } from "element-plus"
import { Refresh, Timer, CopyDocument } from "@element-plus/icons-vue"

interface NodeRow {
  id: string; name: string; protocol: string; server_address: string; port: number
}

interface SysStats {
  load1: number; load5: number; load15: number
  mem_total: number; mem_used: number; mem_pct: number
  disk_total: number; disk_used: number; disk_pct: number
  net_in_bps: number; net_out_bps: number
  online_nodes: number; total_nodes: number; enabled_nodes: number
  online_users: number; total_users: number
  uptime_sec: number; hostname: string; sampled_at: number
}

const stats = ref({ ...mockDashboardStats })
// 修复 H2: 趋势图初始为空数组, 由 fetchTrafficTrend 从 /traffic/trend 拉真实数据填充
const trafficTrend = ref<{ days: string[]; upload: number[]; download: number[] }>({ days: [], upload: [], download: [] })
const userGrowth = ref<{ days: string[]; total: number[]; new: number[] }>({ days: [], total: [], new: [] })
const nodeList = ref<NodeRow[]>([])
const nodeLoading = ref(false)
const sysStats = ref<SysStats>({
  load1: 0, load5: 0, load15: 0,
  mem_total: 0, mem_used: 0, mem_pct: 0,
  disk_total: 0, disk_used: 0, disk_pct: 0,
  net_in_bps: 0, net_out_bps: 0,
  online_nodes: 0, total_nodes: 0, enabled_nodes: 0,
  online_users: 0, total_users: 0,
  uptime_sec: 0, hostname: '', sampled_at: 0,
})

let sysTimer: number | null = null

// ============================================================
// 服务日志监控(容器列表 + 报错日志 + 30 分钟轮询)
// ============================================================
interface ContainerInfo {
  id: string; name: string; image: string
  status: string; state: string; ports: string; created: string
}

const logMonitor = reactive({
  containers: [] as ContainerInfo[],
  selectedContainer: '',
  available: true,
  // 等级筛选: all / error / warn
  level: 'all' as 'all' | 'error' | 'warn',
  // 时间窗口(对应 docker logs --since)
  since: '30m',
  // 拉取行数(对应 docker logs --tail)
  tail: 500,
  // 原始日志文本
  rawLogs: '',
  // 当前过滤后展示的日志
  filteredLogs: '',
  loading: false,
  // 自动刷新(默认开启, 30 分钟轮询)
  autoRefresh: true,
  intervalLabel: '30min',
  lastFetch: 0 as number | 0,
})

const logStats = reactive({
  error_count: 0,
  warn_count: 0,
  total_lines: 0,
  cached: false,
  failed: false,
})

const logViewRef = ref<HTMLElement | null>(null)
let logTimer: number | null = null

// 报错/警告正则(与后端 analyzeContainerLogs 保持一致)
const LOG_ERROR_RE = /\b(error|err\b|fatal|panic|exception|failed|failure|nil pointer|undefined|out of memory|oom-killer|segmentation fault|segfault)\b/i
const LOG_WARN_RE = /\b(warn(ing)?|deprecat(ed|ion)|slow query|retry|backoff)\b/i

// 拉取容器列表
const fetchContainers = async () => {
  try {
    const res: any = await request.get('/api/v1/admin/system/containers')
    const d = res?.data || res
    logMonitor.available = d?.available !== false
    logMonitor.containers = d?.containers || []
    // 默认选中第一个 running 的容器(优先 nexus-panel 本身)
    if (!logMonitor.selectedContainer && logMonitor.containers.length) {
      const np = logMonitor.containers.find(c => c.name === 'nexus-panel' || c.name.includes('panel'))
      logMonitor.selectedContainer = (np || logMonitor.containers[0]).name
    }
  } catch { /* 拦截器处理 */ }
}

// 拉取选中容器的日志
const fetchContainerLogs = async () => {
  if (!logMonitor.selectedContainer) {
    ElMessage.warning('请先选择容器')
    return
  }
  logMonitor.loading = true
  try {
    const res: any = await request.get(
      `/api/v1/admin/system/containers/${encodeURIComponent(logMonitor.selectedContainer)}/logs`,
      { params: { tail: logMonitor.tail, since: logMonitor.since }, silent: true }
    )
    const d = res?.data || res
    if (d) {
      logMonitor.rawLogs = d.logs || ''
      logStats.error_count = d.error_count || 0
      logStats.warn_count = d.warn_count || 0
      logStats.total_lines = d.total_lines || 0
      logStats.cached = !!d.cached
      logStats.failed = !!d.failed
      logMonitor.lastFetch = Date.now()
      applyLogFilter()
    }
  } catch { /* silent */ } finally {
    logMonitor.loading = false
  }
}

// 根据等级筛选过滤日志
const applyLogFilter = () => {
  if (!logMonitor.rawLogs) {
    logMonitor.filteredLogs = ''
    return
  }
  if (logMonitor.level === 'all') {
    logMonitor.filteredLogs = logMonitor.rawLogs
    return
  }
  const lines = logMonitor.rawLogs.split('\n')
  const filtered = lines.filter(line => {
    if (logMonitor.level === 'error') return LOG_ERROR_RE.test(line)
    if (logMonitor.level === 'warn') return LOG_ERROR_RE.test(line) || LOG_WARN_RE.test(line)
    return true
  })
  logMonitor.filteredLogs = filtered.length ? filtered.join('\n') : '(当前等级无匹配日志)'
}

const onContainerChange = () => {
  fetchContainerLogs()
}

const onFilterChange = () => {
  // 等级筛选纯前端过滤(不需要重新请求)
  applyLogFilter()
  // 但 tail/since 变化需要重新拉取
  fetchContainerLogs()
}

const toggleAutoRefresh = () => {
  logMonitor.autoRefresh = !logMonitor.autoRefresh
  if (logMonitor.autoRefresh) {
    startLogTimer()
    ElMessage.success('已开启自动刷新(每 30 分钟)')
  } else {
    stopLogTimer()
    ElMessage.info('已关闭自动刷新')
  }
}

const startLogTimer = () => {
  stopLogTimer()
  // 30 分钟轮询一次(用户要求"每隔半个小时查询一次")
  logTimer = window.setInterval(fetchContainerLogs, 30 * 60 * 1000)
}

const stopLogTimer = () => {
  if (logTimer !== null) {
    clearInterval(logTimer)
    logTimer = null
  }
}

// 复制日志到剪贴板(三层兜底: clipboard API → execCommand → 选中)
const copyLogs = async () => {
  const text = logMonitor.filteredLogs || logMonitor.rawLogs
  if (!text) {
    ElMessage.warning('暂无日志可复制')
    return
  }
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      ElMessage.success(`已复制 ${text.length} 字符`)
      return
    }
  } catch { /* fall through */ }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    if (ok) {
      ElMessage.success(`已复制 ${text.length} 字符`)
      return
    }
  } catch { /* fall through */ }
  // 最后兜底: 选中日志框让用户 Ctrl+C
  if (logViewRef.value) {
    const range = document.createRange()
    range.selectNodeContents(logViewRef.value)
    const sel = window.getSelection()
    sel?.removeAllRanges()
    sel?.addRange(range)
    ElMessage.info('已选中日志, 请按 Ctrl+C 复制')
  }
}

const statCards = computed(() => [
  {
    label: "用户总数", value: stats.value.total_users,
    icon: "User", color: "#00f5d4", bg: "rgba(0,245,212,0.12)",
  },
  {
    label: "节点总数", value: stats.value.total_nodes,
    icon: "Connection", color: "#9d4edd", bg: "rgba(157,78,221,0.12)",
  },
  {
    label: "在线节点", value: stats.value.online_nodes,
    icon: "CircleCheck", color: "#00f5d4", bg: "rgba(0,245,212,0.12)",
  },
  {
    label: "今日流量", value: formatTraffic((stats.value.today_upload || 0) + (stats.value.today_download || 0)),
    icon: "TrendCharts", color: "#ffbe0b", bg: "rgba(255,190,11,0.12)",
  },
])

// 限制 load/进度条 0-100（cpu 核数 * 100 为 100% 满载，保守按负载 * 25 估算）
const loadPct = computed(() => {
  const l = sysStats.value.load1 || 0
  return Math.min(100, Math.round(l * 25))
})
const memPct = computed(() => Math.round(sysStats.value.mem_pct || 0))
const diskPct = computed(() => Math.round(sysStats.value.disk_pct || 0))

const trafficOption = computed(() => ({
  tooltip: { trigger: "axis" },
  legend: { data: ["上行", "下行"], textStyle: { color: "#8b98a9" }, top: 0 },
  grid: { left: 40, right: 20, top: 40, bottom: 30 },
  xAxis: { type: "category", data: trafficTrend.value.days, axisLine: { lineStyle: { color: "#1e2a3a" } }, axisLabel: { color: "#8b98a9" } },
  yAxis: { type: "value", splitLine: { lineStyle: { color: "#1e2a3a" } }, axisLabel: { color: "#8b98a9" } },
  series: [
    { name: "上行", type: "line", smooth: true, data: trafficTrend.value.upload, itemStyle: { color: chartColors[0] },
      areaStyle: { color: { type: "linear", x: 0, y: 0, x2: 0, y2: 1, colorStops: [{ offset: 0, color: "rgba(0,245,212,0.4)" }, { offset: 1, color: "rgba(0,245,212,0)" }] } } },
    { name: "下行", type: "line", smooth: true, data: trafficTrend.value.download, itemStyle: { color: chartColors[1] },
      areaStyle: { color: { type: "linear", x: 0, y: 0, x2: 0, y2: 1, colorStops: [{ offset: 0, color: "rgba(157,78,221,0.4)" }, { offset: 1, color: "rgba(157,78,221,0)" }] } } },
  ],
}))

const growthOption = computed(() => ({
  tooltip: { trigger: "axis" },
  legend: { data: ["总用户", "新增"], textStyle: { color: "#8b98a9" }, top: 0 },
  grid: { left: 40, right: 20, top: 40, bottom: 30 },
  xAxis: { type: "category", data: userGrowth.value.days, axisLine: { lineStyle: { color: "#1e2a3a" } }, axisLabel: { color: "#8b98a9" } },
  yAxis: [{ type: "value", splitLine: { lineStyle: { color: "#1e2a3a" } }, axisLabel: { color: "#8b98a9" } }],
  series: [
    { name: "总用户", type: "bar", data: userGrowth.value.total, itemStyle: { color: "rgba(0,245,212,0.6)", borderRadius: [4, 4, 0, 0] } },
    { name: "新增", type: "line", smooth: true, data: userGrowth.value.new, itemStyle: { color: chartColors[3] }, lineStyle: { color: chartColors[3] } },
  ],
}))

const fetchSysStats = async () => {
  try {
    const res: any = await request.get("/api/v1/admin/system/stats")
    if (res && res.code === 0 && res.data) {
      sysStats.value = { ...sysStats.value, ...res.data }
    }
  } catch { /* fallback */ }
}

// 修复 H2: 从 /traffic/trend 拉真实流量趋势(后端返回 {items:[{day,up,down,total}]}),
// 映射为前端图表所需的 days/upload/download 数组。原代码错调 /traffic/top 取 trend 字段,
// 该接口仅返回 {Users,Nodes} 无 trend, 导致趋势图永远显示 mock 数据。
const fetchTrafficTrend = async () => {
  try {
    const res: any = await request.get("/api/v1/admin/traffic/trend", { params: { days: 7 } })
    const td = res?.data || res
    if (td && Array.isArray(td.items) && td.items.length) {
      trafficTrend.value = {
        days: td.items.map((p: any) => p.day),
        upload: td.items.map((p: any) => Number(p.up) || 0),
        download: td.items.map((p: any) => Number(p.down) || 0),
      }
    }
  } catch { /* 拦截器处理 */ }
}

onMounted(async () => {
  try {
    const res = await request.get("/api/v1/admin/dashboard")
    if (res && res.data) { stats.value = { ...stats.value, ...res.data } }
  } catch { /* fallback */ }
  fetchTrafficTrend()
  nodeLoading.value = true
  try {
    const res = await request.get("/api/v1/admin/nodes")
    if (res && res.data && res.data.list) { nodeList.value = res.data.list }
  } catch { /* fallback */ }
  nodeLoading.value = false
  // 立即拉一次系统状态，然后每 3s 刷新
  fetchSysStats()
  sysTimer = window.setInterval(fetchSysStats, 3000)

  // 服务日志监控: 先拉容器列表, 再拉选中容器的日志, 然后每 30 分钟轮询
  await fetchContainers()
  if (logMonitor.selectedContainer) {
    fetchContainerLogs()
  }
  if (logMonitor.autoRefresh) {
    startLogTimer()
  }
})

onBeforeUnmount(() => {
  if (sysTimer !== null) {
    clearInterval(sysTimer)
    sysTimer = null
  }
  stopLogTimer()
})
</script>

<style scoped>
.stat-row { margin-bottom: 20px; }
.stat-card { display: flex; align-items: center; gap: 16px; padding: 20px; margin-bottom: 20px; }
.stat-icon { width: 52px; height: 52px; border-radius: 12px; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
.stat-info { flex: 1; }
.stat-label { font-size: 13px; color: var(--np-text-secondary); margin-bottom: 6px; }
.stat-value { font-size: 24px; font-weight: 700; color: var(--np-text); }
.chart-row { margin-bottom: 20px; }
.chart-card { padding: 20px; height: 100%; box-sizing: border-box; }
.chart-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.chart-title { font-size: 15px; font-weight: 600; color: var(--np-text); }
.chart { height: 300px; width: 100%; }
.sys-block { display: flex; flex-direction: column; gap: 8px; padding: 4px 0; }
.sys-label { font-size: 12px; color: var(--np-text-muted); }
.sys-value { font-size: 14px; color: var(--np-text); font-weight: 600; font-family: monospace; }
.net-block { display: flex; flex-direction: column; gap: 14px; }
.net-row { display: flex; align-items: center; justify-content: space-between; padding: 10px 12px; background: rgba(0, 245, 212, 0.04); border-radius: 6px; }
.net-label { display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--np-text-secondary); }
.net-value { font-size: 16px; font-weight: 700; color: var(--np-text); font-family: monospace; }
.net-value.up { color: #ffbe0b; }
.net-value.down { color: var(--np-primary); }
.net-inline.up { color: #ffbe0b; }
.net-inline.down { color: var(--np-primary); }
.net-sep { color: var(--np-text-muted); margin: 0 4px; }
.sys-sub { font-size: 12px; color: var(--np-text-muted); margin-top: 4px; }

/* 服务日志监控卡片 */
.log-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.log-toolbar-spacer { flex: 1; }
.log-tail-label { font-size: 12px; color: var(--np-text-muted); }
.log-stats-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 4px 0 8px;
  font-size: 12px;
  color: var(--np-text-muted);
}
.log-stats-text { font-family: monospace; }
.log-stats-cached { color: var(--np-primary); margin-left: 4px; }
.log-view {
  margin: 0;
  padding: 12px;
  background: var(--np-bg-soft);
  border-radius: 6px;
  font-family: 'JetBrains Mono', 'Consolas', monospace;
  font-size: 12px;
  line-height: 1.5;
  color: var(--np-text-secondary);
  white-space: pre-wrap;
  word-break: break-all;
  /* 固定大小, 日志在框内滚动(与磁盘管理/一键更新日志框一致) */
  height: 360px;
  overflow-y: auto;
  border: 1px solid var(--np-border);
}
</style>
