<template>
  <div class="admin-traffic">
    <!-- 时间范围筛选 -->
    <div class="np-card filter-card">
      <span class="filter-label">时间范围：</span>
      <el-radio-group v-model="timeRange" @change="loadData">
        <el-radio-button value="today">今日</el-radio-button>
        <el-radio-button value="week">本周</el-radio-button>
        <el-radio-button value="month">本月</el-radio-button>
        <el-radio-button value="year">全年</el-radio-button>
      </el-radio-group>
      <div class="filter-summary">
        <div class="summary-item">
          <span class="summary-label">总流量</span>
          <span class="summary-value">{{ formatTraffic(totalTraffic) }}</span>
        </div>
        <div class="summary-item">
          <span class="summary-label">上行</span>
          <span class="summary-value up">{{ formatTraffic(totalUpload) }}</span>
        </div>
        <div class="summary-item">
          <span class="summary-label">下行</span>
          <span class="summary-value down">{{ formatTraffic(totalDownload) }}</span>
        </div>
      </div>
    </div>

    <el-row :gutter="20">
      <!-- TOP 用户列表 -->
      <el-col :xs="24" :md="12">
        <div class="np-card chart-card">
          <div class="chart-header">
            <span class="chart-title">流量 TOP 用户</span>
          </div>
          <div v-loading="loading" class="top-list">
            <div v-for="(item, idx) in topUsers" :key="item.username" class="top-item">
              <div class="top-rank" :class="'rank-' + (idx + 1)">{{ idx + 1 }}</div>
              <div class="top-info">
                <div class="top-name">{{ item.username }}</div>
                <el-progress :percentage="topPercent(item.traffic)" :stroke-width="6" :color="rankColor(idx)" :show-text="false" />
              </div>
              <div class="top-value">{{ formatTraffic(item.traffic) }}</div>
            </div>
            <el-empty v-if="!loading && !topUsers.length" description="暂无流量数据" />
          </div>
        </div>
      </el-col>

      <!-- 节点流量分布饼图 -->
      <el-col :xs="24" :md="12">
        <div class="np-card chart-card">
          <div class="chart-header">
            <span class="chart-title">节点流量分布</span>
          </div>
          <v-chart v-if="nodeDist.length" class="chart" :option="pieOption" autoresize />
          <el-empty v-else description="暂无节点流量数据" />
        </div>
      </el-col>
    </el-row>

    <!-- 流量趋势 -->
    <div class="np-card chart-card trend-card">
      <div class="chart-header">
        <span class="chart-title">流量趋势（{{ trendTitle }}）</span>
      </div>
      <v-chart v-if="trendData.days.length" class="chart trend-chart" :option="trendOption" autoresize />
      <el-empty v-else description="暂无趋势数据" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import VChart from 'vue-echarts'
import '@/utils/echarts'
import { chartColors } from '@/utils/echarts'
import { formatTraffic } from '@/utils/format'
import request from '@/utils/request'

interface TopUser { username: string; traffic: number; upload: number; download: number }
interface NodeTraffic { name: string; value: number }

const timeRange = ref('week')
const loading = ref(false)
const topUsers = ref<TopUser[]>([])
const nodeDist = ref<NodeTraffic[]>([])
const trendData = ref<{ days: string[]; upload: number[]; download: number[]; total: number; up: number; down: number }>({ days: [], upload: [], download: [], total: 0, up: 0, down: 0 })

// 时间范围标题映射（与按钮文字保持一致）
const trendTitle = computed(() => {
  const map: Record<string, string> = { today: '今日', week: '本周', month: '本月', year: '全年' }
  return map[timeRange.value] || '本周'
})

// 修复：使用趋势接口返回的总流量（全部用户），而不是从 TOP 10 用户累加
const totalTraffic = computed(() => trendData.value.total)
const totalUpload = computed(() => trendData.value.up)
const totalDownload = computed(() => trendData.value.down)

const topPercent = (val: number) => {
  const max = Math.max(...topUsers.value.map((u) => u.traffic), 1)
  return Math.round((val / max) * 100)
}
const rankColor = (idx: number) => {
  const colors = ['#ffbe0b', '#c0c0c0', '#cd7f32', '#00f5d4', '#00f5d4']
  return colors[idx] || '#00f5d4'
}

// 饼图配置
const pieOption = computed(() => ({
  tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
  legend: { bottom: 0, textStyle: { color: '#8b98a9' } },
  series: [
    {
      type: 'pie',
      radius: ['45%', '70%'],
      center: ['50%', '45%'],
      avoidLabelOverlap: false,
      itemStyle: { borderColor: '#131822', borderWidth: 2 },
      label: { show: false },
      emphasis: { label: { show: true, fontSize: 14, color: '#e6edf3' } },
      data: nodeDist.value.map((n, i) => ({
        name: n.name, value: n.value, itemStyle: { color: chartColors[i % chartColors.length] },
      })),
    },
  ],
}))

// 趋势图配置
const trendOption = computed(() => ({
  tooltip: { trigger: 'axis' },
  legend: { data: ['上行', '下行'], textStyle: { color: '#8b98a9' }, top: 0 },
  grid: { left: 40, right: 20, top: 40, bottom: 30 },
  xAxis: {
    type: 'category', data: trendData.value.days,
    axisLine: { lineStyle: { color: '#1e2a3a' } }, axisLabel: { color: '#8b98a9' },
  },
  yAxis: {
    type: 'value', splitLine: { lineStyle: { color: '#1e2a3a' } }, axisLabel: { color: '#8b98a9' },
  },
  series: [
    {
      name: '上行', type: 'bar', data: trendData.value.upload,
      itemStyle: { color: 'rgba(0,245,212,0.6)', borderRadius: [4, 4, 0, 0] },
    },
    {
      name: '下行', type: 'bar', data: trendData.value.download,
      itemStyle: { color: 'rgba(157,78,221,0.6)', borderRadius: [4, 4, 0, 0] },
    },
  ],
}))

const loadData = async () => {
  loading.value = true
  try {
    // 加载流量TOP用户
    const res: any = await request.get('/api/v1/admin/traffic/top', { params: { range: timeRange.value, limit: 10 } })
    const data = res?.data || res
    const list = data?.users || []
    topUsers.value = list.map((u: any) => ({
      username: u.username || '未知',
      traffic: u.total_bytes || 0,
      upload: u.upload_bytes || 0,
      download: u.download_bytes || 0,
    })).sort((a: TopUser, b: TopUser) => b.traffic - a.traffic)

    // 节点流量分布 - 从后端返回的 nodes 字段获取
    const nodeList = data?.nodes || []
    nodeDist.value = nodeList.map((n: any) => ({
      name: n.node_name || n.name || '未知节点',
      value: n.total_bytes || 0,
    })).filter((n: NodeTraffic) => n.value > 0)

    // 趋势数据（后端接受 range 字符串或 days 整数）
    try {
      const trendRes: any = await request.get('/api/v1/admin/traffic/trend', { params: { range: timeRange.value } })
      const td = trendRes?.data || trendRes
      // 后端返回 { items: [{ day, up, down, total }], days, total, up, down }
      if (td?.items && Array.isArray(td.items)) {
        trendData.value = {
          days: td.items.map((p: any) => p.day),
          upload: td.items.map((p: any) => p.up || 0),
          download: td.items.map((p: any) => p.down || 0),
          total: td.total || 0,
          up: td.up || 0,
          down: td.down || 0,
        }
      } else {
        trendData.value = { days: [], upload: [], download: [], total: 0, up: 0, down: 0 }
      }
    } catch {
      trendData.value = { days: [], upload: [], download: [], total: 0, up: 0, down: 0 }
    }
  } catch {
    topUsers.value = []
    nodeDist.value = []
  } finally {
    loading.value = false
  }
}

loadData()
</script>

<style scoped>
.filter-card { padding: 16px 20px; margin-bottom: 20px; display: flex; align-items: center; gap: 20px; flex-wrap: wrap; }
.filter-label { color: var(--np-text-secondary); font-size: 14px; }
.filter-summary { display: flex; gap: 32px; margin-left: auto; }
.summary-item { display: flex; flex-direction: column; gap: 4px; }
.summary-label { font-size: 12px; color: var(--np-text-muted); }
.summary-value { font-size: 18px; font-weight: 700; color: var(--np-text); }
.summary-value.up { color: var(--np-primary); }
.summary-value.down { color: var(--np-purple); }

.chart-card { padding: 20px; margin-bottom: 20px; }
.chart-header { margin-bottom: 16px; }
.chart-title { font-size: 15px; font-weight: 600; color: var(--np-text); }
.chart { height: 320px; width: 100%; }
.trend-chart { height: 280px; }

.top-list { display: flex; flex-direction: column; gap: 16px; padding: 8px 0; }
.top-item { display: flex; align-items: center; gap: 14px; }
.top-rank {
  width: 28px; height: 28px; border-radius: 50%; display: flex; align-items: center; justify-content: center;
  font-weight: 700; font-size: 13px; flex-shrink: 0;
  background: var(--np-bg-soft); color: var(--np-text-secondary);
}
.top-rank.rank-1 { background: rgba(255,190,11,0.2); color: #ffbe0b; }
.top-rank.rank-2 { background: rgba(192,192,192,0.2); color: #c0c0c0; }
.top-rank.rank-3 { background: rgba(205,127,51,0.2); color: #cd7f32; }
.top-info { flex: 1; display: flex; flex-direction: column; gap: 6px; }
.top-name { font-size: 14px; color: var(--np-text); }
.top-value { font-size: 13px; color: var(--np-primary); font-weight: 600; min-width: 100px; text-align: right; }

@media (max-width: 768px) {
  .filter-card { flex-direction: column; align-items: stretch; gap: 14px; padding: 14px; }
  .filter-label { font-size: 13px; }
  .filter-summary { margin-left: 0; width: 100%; justify-content: space-between; gap: 12px; }
  .summary-item { flex: 1; }
  .summary-value { font-size: 16px; }
  .chart-card { padding: 14px; }
  .chart { height: 260px; }
  .trend-chart { height: 220px; }
  .top-list { gap: 12px; }
  .top-item { gap: 10px; }
  .top-rank { width: 24px; height: 24px; font-size: 12px; }
  .top-value { min-width: 70px; font-size: 12px; }
}
</style>
