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
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from "vue"
import VChart from "vue-echarts"
import "@/utils/echarts"
import request from "@/utils/request"
import { formatTraffic, formatTime, formatSpeed, formatDuration } from "@/utils/format"
import { chartColors } from "@/utils/echarts"
import { mockDashboardStats } from "@/mock/data"

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
})

onBeforeUnmount(() => {
  if (sysTimer !== null) {
    clearInterval(sysTimer)
    sysTimer = null
  }
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
</style>
