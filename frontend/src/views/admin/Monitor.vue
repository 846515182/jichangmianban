<template>
  <div class="admin-monitor">
    <!-- 顶部：标题 + 刷新控制 -->
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">节点负载监控</h2>
          <p class="page-desc">实时监控所有节点负载情况，一眼识别空闲、繁忙与满载节点</p>
        </div>
        <div class="header-actions">
          <el-switch v-model="autoRefresh" active-text="自动刷新" />
          <span class="refresh-hint">
            <el-icon><Timer /></el-icon>
            {{ autoRefresh ? `每 ${refreshInterval}s 刷新` : '已暂停' }}
          </span>
          <span class="refresh-hint" v-if="lastUpdate">
            <el-icon><Clock /></el-icon>
            {{ formatRelative(lastUpdate) }}
          </span>
          <el-button type="primary" :loading="loading" @click="manualRefresh">
            <el-icon><Refresh /></el-icon>刷新
          </el-button>
        </div>
      </div>

      <!-- 顶部汇总卡片（4 个横排） -->
      <el-row :gutter="16" class="summary-row">
        <el-col :xs="24" :sm="12" :md="6">
          <el-card shadow="hover" class="summary-card summary-total" v-loading="loading">
            <div class="sc-body">
              <div class="sc-icon sc-icon-total">
                <el-icon :size="22"><Connection /></el-icon>
              </div>
              <div class="sc-info">
                <div class="sc-value">{{ summary.total || 0 }}</div>
                <div class="sc-label">节点总数</div>
                <div class="sc-sub">
                  <span class="dot online"></span>在线 {{ summary.online || 0 }}
                  <span class="dot offline" style="margin-left:10px"></span>离线 {{ summary.offline || 0 }}
                </div>
              </div>
            </div>
          </el-card>
        </el-col>
        <el-col :xs="24" :sm="12" :md="6">
          <el-card shadow="hover" class="summary-card summary-idle" v-loading="loading">
            <div class="sc-body">
              <div class="sc-icon sc-icon-idle">
                <el-icon :size="22"><CircleCheck /></el-icon>
              </div>
              <div class="sc-info">
                <div class="sc-value">{{ summary.idle || 0 }}</div>
                <div class="sc-label">空闲节点</div>
                <div class="sc-sub">资源充足，可承接流量</div>
              </div>
            </div>
          </el-card>
        </el-col>
        <el-col :xs="24" :sm="12" :md="6">
          <el-card shadow="hover" class="summary-card summary-busy" v-loading="loading">
            <div class="sc-body">
              <div class="sc-icon sc-icon-busy">
                <el-icon :size="22"><Warning /></el-icon>
              </div>
              <div class="sc-info">
                <div class="sc-value">{{ (summary.busy || 0) + (summary.full || 0) }}</div>
                <div class="sc-label">繁忙 / 满载（需关注）</div>
                <div class="sc-sub">
                  繁忙 <span class="num-busy">{{ summary.busy || 0 }}</span>
                  · 满载 <span class="num-full">{{ summary.full || 0 }}</span>
                </div>
              </div>
            </div>
          </el-card>
        </el-col>
        <el-col :xs="24" :sm="12" :md="6">
          <el-card shadow="hover" class="summary-card summary-conn" v-loading="loading">
            <div class="sc-body">
              <div class="sc-icon sc-icon-conn">
                <el-icon :size="22"><User /></el-icon>
              </div>
              <div class="sc-info">
                <div class="sc-value">{{ totalConnections }}</div>
                <div class="sc-label">当前总连接数</div>
                <div class="sc-sub">在线节点连接数之和</div>
              </div>
            </div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 动态限速说明 -->
      <el-alert
        type="info"
        :closable="false"
        show-icon
        class="dynamic-limit-tip"
      >
        <template #title>系统根据节点用途+实时负载自动动态限速，无需手动配置</template>
      </el-alert>
    </div>

    <!-- 节点负载热力图 -->
    <div class="np-card page-card heatmap-card">
      <div class="page-header">
        <div>
          <h3 class="page-title">节点负载热力图</h3>
          <p class="page-desc">按负载状态着色：空闲(绿) / 正常(蓝) / 繁忙(橙) / 满载(红) / 离线(灰)</p>
        </div>
        <div class="legend-bar">
          <span class="legend-item"><i class="lg-dot lg-idle"></i>空闲</span>
          <span class="legend-item"><i class="lg-dot lg-normal"></i>正常</span>
          <span class="legend-item"><i class="lg-dot lg-busy"></i>繁忙</span>
          <span class="legend-item"><i class="lg-dot lg-full"></i>满载</span>
          <span class="legend-item"><i class="lg-dot lg-offline"></i>离线</span>
        </div>
      </div>

      <div class="heatmap-grid" v-loading="loading">
        <div
          v-for="node in nodes"
          :key="node.id"
          class="node-heat-card"
          :class="`st-${statusKey(node)}`"
        >
          <!-- 卡片头部：名称 + IP + 状态标签 -->
          <div class="nh-header">
            <div class="nh-title-wrap">
              <span class="nh-name">{{ node.name }}</span>
              <span class="nh-ip">{{ node.server_address }}</span>
            </div>
            <el-tag size="small" :type="statusTagType(statusKey(node))" effect="dark">
              {{ statusText(statusKey(node)) }}
            </el-tag>
          </div>

          <!-- CPU 使用率 -->
          <div class="nh-metric">
            <div class="nh-metric-head">
              <span class="nh-metric-label">CPU</span>
              <span class="nh-metric-value">{{ rtCpu(node).toFixed(1) }}%</span>
            </div>
            <el-progress
              :percentage="Math.min(100, rtCpu(node))"
              :color="usageColors"
              :stroke-width="8"
              :show-text="false"
            />
          </div>

          <!-- 内存使用率 -->
          <div class="nh-metric">
            <div class="nh-metric-head">
              <span class="nh-metric-label">内存</span>
              <span class="nh-metric-value">
                {{ rtMem(node).toFixed(1) }}%
                <span class="nh-mem-total" v-if="rtMemTotal(node) > 0">/ {{ formatTraffic(rtMemTotal(node)) }}</span>
              </span>
            </div>
            <el-progress
              :percentage="Math.min(100, rtMem(node))"
              :color="usageColors"
              :stroke-width="8"
              :show-text="false"
            />
          </div>

          <!-- 连接数 / 最大用户数 + 速度 + 用途 -->
          <div class="nh-footer">
            <div class="nh-footer-row">
              <span class="nh-foot-label">连接</span>
              <span class="nh-foot-value">
                {{ rtConns(node) }}
                <span class="nh-foot-max" v-if="node.max_clients > 0">/ {{ node.max_clients }}</span>
                <span class="nh-foot-max" v-else>/ 不限</span>
              </span>
            </div>
            <div class="nh-footer-row">
              <span class="nh-foot-label">速度</span>
              <span class="nh-foot-value nh-speed">{{ formatSpeed(rtSpeed(node)) }}</span>
            </div>
            <div class="nh-footer-row">
              <span class="nh-foot-label">限速</span>
              <span class="nh-foot-value">{{ node.usage_type === 'download' ? '不限速' : (node.dynamic_limit_mbps || '-') + ' Mbps' }}</span>
            </div>
            <div class="nh-footer-row">
              <span class="nh-foot-label">用途</span>
              <el-tag size="small" :type="usageTagType(node.usage_type)" effect="plain">
                {{ usageText(node.usage_type) }}
              </el-tag>
            </div>
          </div>

          <!-- 离线遮罩提示 -->
          <div class="nh-offline-tip" v-if="!node.online">
            节点离线，无实时数据
          </div>
        </div>

        <el-empty v-if="!loading && !nodes.length" description="暂无节点数据" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { ElMessage } from 'element-plus'
import {
  Refresh,
  Timer,
  Clock,
  Connection,
  CircleCheck,
  Warning,
  User,
} from '@element-plus/icons-vue'
import request from '@/utils/request'
import { formatSpeed, formatTraffic, formatRelative } from '@/utils/format'

// ---- 类型定义 ----
interface NodeRuntime {
  cpu_usage: number
  memory_usage: number
  memory_total: number
  online_connections: number
  speed_bps: number
  uptime_seconds: number
  updated_at: number
}
interface MonitorNode {
  id: string
  name: string
  server_address: string
  online: boolean
  load_status: string // idle / normal / busy / full
  usage_type: string // general / browsing / video / download
  max_clients: number
  runtime: NodeRuntime
  [k: string]: any
}
interface MonitorSummary {
  total: number
  online: number
  offline: number
  idle: number
  normal: number
  busy: number
  full: number
}
interface MonitorData {
  nodes: MonitorNode[]
  summary: MonitorSummary
}

// ---- 响应式状态 ----
const loading = ref(false)
const autoRefresh = ref(true)
const refreshInterval = 5 // 秒
const lastUpdate = ref<number>(0)
const nodes = ref<MonitorNode[]>([])
const summary = ref<Partial<MonitorSummary>>({})

let refreshTimer: number | null = null

// CPU / 内存进度条颜色：< 50% 绿，50-80% 黄，> 80% 红
const usageColors = [
  { color: '#67c23a', percentage: 50 },
  { color: '#e6a23c', percentage: 80 },
  { color: '#f56c6c', percentage: 100 },
]

// ---- 计算属性 ----
// 当前总连接数：所有在线节点的 online_connections 之和
const totalConnections = computed(() => {
  return nodes.value
    .filter((n) => n.online && n.runtime)
    .reduce((sum, n) => sum + (n.runtime.online_connections || 0), 0)
})

// ---- 工具函数 ----
// 负载状态 key：离线节点统一为 offline，在线节点取 load_status（缺省 idle）
const statusKey = (node: MonitorNode): string => {
  if (!node.online) return 'offline'
  return node.load_status || 'idle'
}

const statusText = (s: string): string => {
  const map: Record<string, string> = {
    idle: '空闲',
    normal: '正常',
    busy: '繁忙',
    full: '满载',
    offline: '离线',
  }
  return map[s] || '空闲'
}

const statusTagType = (s: string): any => {
  const map: Record<string, string> = {
    idle: 'success',
    normal: '',
    busy: 'warning',
    full: 'danger',
    offline: 'info',
  }
  return map[s] || 'success'
}

const usageText = (t: string): string => {
  const map: Record<string, string> = {
    general: '通用',
    browsing: '仅浏览',
    video: '视频',
    download: '下载',
  }
  return map[t] || '通用'
}

const usageTagType = (t: string): any => {
  const map: Record<string, string> = {
    general: '',
    browsing: 'info',
    video: 'warning',
    download: 'success',
  }
  return map[t] || ''
}

// 安全读取 runtime 字段（离线节点可能无 runtime）
const rtCpu = (n: MonitorNode): number => n.runtime?.cpu_usage || 0
const rtMem = (n: MonitorNode): number => n.runtime?.memory_usage || 0
const rtMemTotal = (n: MonitorNode): number => n.runtime?.memory_total || 0
const rtConns = (n: MonitorNode): number => n.runtime?.online_connections || 0
const rtSpeed = (n: MonitorNode): number => n.runtime?.speed_bps || 0

// ---- 数据拉取 ----
// showLoading: 是否显示加载遮罩（自动刷新时不显示，避免闪烁）
// silent: 是否静默错误（自动刷新时静默，避免错误弹窗刷屏）
const fetchData = async (showLoading = true, silent = false) => {
  if (showLoading) loading.value = true
  try {
    const res: any = await request.get('/api/v1/admin/nodes/monitor', { silent })
    if (res && res.code === 0 && res.data) {
      nodes.value = res.data.nodes || []
      summary.value = res.data.summary || {}
      lastUpdate.value = Date.now()
    }
  } catch {
    // 错误由拦截器处理（silent 时不弹窗）
  } finally {
    if (showLoading) loading.value = false
  }
}

// 手动刷新
const manualRefresh = () => {
  fetchData(true, false)
}

// ---- 自动刷新定时器 ----
const startTimer = () => {
  stopTimer()
  if (autoRefresh.value) {
    refreshTimer = window.setInterval(() => {
      fetchData(false, true)
    }, refreshInterval * 1000)
  }
}

const stopTimer = () => {
  if (refreshTimer !== null) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
}

// 切换自动刷新开关
watch(autoRefresh, (val) => {
  if (val) {
    startTimer()
  } else {
    stopTimer()
  }
})

// 标签页可见性变化：隐藏时暂停刷新，可见时立即刷新并恢复定时器
const handleVisibilityChange = () => {
  if (document.hidden) {
    stopTimer()
  } else if (autoRefresh.value) {
    fetchData(false, true)
    startTimer()
  }
}

onMounted(() => {
  fetchData(true, false)
  startTimer()
  document.addEventListener('visibilitychange', handleVisibilityChange)
})

onUnmounted(() => {
  stopTimer()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})
</script>

<style scoped>
.page-card {
  padding: 20px;
}
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 20px;
  flex-wrap: wrap;
  gap: 12px;
}
.page-title {
  margin: 0;
  font-size: 18px;
  color: var(--np-text);
}
.heatmap-card .page-title {
  font-size: 16px;
}
.page-desc {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--np-text-secondary);
}
.header-actions {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}
.refresh-hint {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--np-text-secondary);
}
.refresh-hint .el-icon {
  font-size: 14px;
}

/* ---- 顶部汇总卡片 ---- */
.summary-row {
  margin-top: 4px;
}
.dynamic-limit-tip {
  margin-top: 12px;
}
.summary-card {
  border-radius: 10px;
  border: 1px solid var(--np-border);
  background: var(--np-card);
  transition: transform 0.2s, box-shadow 0.2s;
}
.summary-card:hover {
  transform: translateY(-2px);
}
.summary-card :deep(.el-card__body) {
  padding: 18px 20px;
}
.sc-body {
  display: flex;
  align-items: center;
  gap: 14px;
}
.sc-icon {
  width: 48px;
  height: 48px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.sc-icon-total {
  background: rgba(0, 245, 212, 0.12);
  color: var(--np-primary);
}
.sc-icon-idle {
  background: rgba(103, 194, 58, 0.15);
  color: #67c23a;
}
.sc-icon-busy {
  background: rgba(230, 162, 60, 0.15);
  color: #e6a23c;
}
.sc-icon-conn {
  background: rgba(64, 158, 255, 0.15);
  color: #409eff;
}
.sc-info {
  flex: 1;
  min-width: 0;
}
.sc-value {
  font-size: 28px;
  font-weight: 700;
  color: var(--np-text);
  line-height: 1.2;
}
.sc-label {
  font-size: 13px;
  color: var(--np-text-secondary);
  margin-top: 2px;
}
.sc-sub {
  font-size: 12px;
  color: var(--np-text-muted);
  margin-top: 4px;
  display: flex;
  align-items: center;
}
.sc-sub .dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-right: 4px;
}
.sc-sub .dot.online {
  background: #67c23a;
}
.sc-sub .dot.offline {
  background: #909399;
}
.num-busy {
  color: #e6a23c;
  font-weight: 600;
}
.num-full {
  color: #f56c6c;
  font-weight: 600;
}

/* ---- 热力图卡片网格 ---- */
.heatmap-card {
  margin-top: 16px;
}
.heatmap-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 14px;
}

/* 图例 */
.legend-bar {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}
.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--np-text-secondary);
}
.lg-dot {
  display: inline-block;
  width: 10px;
  height: 10px;
  border-radius: 3px;
}
.lg-idle {
  background: #67c23a;
}
.lg-normal {
  background: #409eff;
}
.lg-busy {
  background: #e6a23c;
}
.lg-full {
  background: #f56c6c;
}
.lg-offline {
  background: #909399;
}

/* 节点卡片 */
.node-heat-card {
  border: 1px solid var(--np-border);
  border-radius: 10px;
  padding: 14px 16px;
  background: var(--np-card);
  transition: transform 0.2s, box-shadow 0.2s, border-color 0.2s;
  position: relative;
}
.node-heat-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.3);
}

/* 按负载状态着色卡片背景（轻微着色） */
.node-heat-card.st-idle {
  border-left: 3px solid #67c23a;
}
.node-heat-card.st-normal {
  border-left: 3px solid #409eff;
  background: linear-gradient(90deg, rgba(64, 158, 255, 0.06), var(--np-card) 30%);
}
.node-heat-card.st-busy {
  border-left: 3px solid #e6a23c;
  background: linear-gradient(90deg, rgba(230, 162, 60, 0.08), var(--np-card) 30%);
}
.node-heat-card.st-full {
  border-left: 3px solid #f56c6c;
  background: linear-gradient(90deg, rgba(245, 108, 108, 0.10), var(--np-card) 30%);
}
.node-heat-card.st-offline {
  border-left: 3px solid #909399;
  background: linear-gradient(90deg, rgba(144, 147, 153, 0.06), var(--np-card) 30%);
  opacity: 0.85;
}

/* 卡片头部 */
.nh-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 12px;
  padding-bottom: 10px;
  border-bottom: 1px dashed var(--np-border);
}
.nh-title-wrap {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  flex: 1;
}
.nh-name {
  font-size: 15px;
  font-weight: 600;
  color: var(--np-text);
  word-break: break-all;
}
.nh-ip {
  font-size: 12px;
  color: var(--np-text-muted);
  font-family: 'JetBrains Mono', Consolas, monospace;
}

/* 指标行 */
.nh-metric {
  margin-bottom: 10px;
}
.nh-metric-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
}
.nh-metric-label {
  font-size: 12px;
  color: var(--np-text-secondary);
}
.nh-metric-value {
  font-size: 12px;
  font-weight: 600;
  color: var(--np-text);
}
.nh-mem-total {
  font-weight: 400;
  color: var(--np-text-muted);
  font-size: 11px;
}

/* 底部信息 */
.nh-footer {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed var(--np-border);
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.nh-footer-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
}
.nh-foot-label {
  color: var(--np-text-secondary);
}
.nh-foot-value {
  color: var(--np-text);
  font-weight: 500;
}
.nh-foot-max {
  color: var(--np-text-muted);
  font-weight: 400;
}
.nh-speed {
  font-family: 'JetBrains Mono', Consolas, monospace;
  color: var(--np-primary);
}

/* 离线提示 */
.nh-offline-tip {
  margin-top: 10px;
  padding: 6px 10px;
  border-radius: 6px;
  background: rgba(144, 147, 153, 0.1);
  color: var(--np-text-muted);
  font-size: 12px;
  text-align: center;
}

/* 移动端适配 */
@media (max-width: 768px) {
  .page-card {
    padding: 14px;
  }
  .page-header {
    flex-direction: column;
    align-items: stretch;
  }
  .header-actions {
    flex-wrap: wrap;
  }
  .legend-bar {
    gap: 10px;
  }
  .heatmap-grid {
    grid-template-columns: 1fr;
  }
}
</style>
