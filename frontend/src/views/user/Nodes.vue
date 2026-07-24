<template>
  <div class="user-nodes">
    <div class="page-header">
      <h2 class="page-title">节点列表</h2>
      <el-button @click="loadData" :loading="loading"><el-icon><Refresh /></el-icon>刷新</el-button>
    </div>

    <el-row :gutter="20">
      <el-col :xs="24" :sm="12" :md="8" v-for="node in nodeList" :key="node.id">
        <div class="node-card np-card" :class="{ offline: !node.online }">
          <div class="node-top">
            <div class="node-name">
              <i class="np-dot" :class="node.online ? 'online' : 'offline'"></i>
              <span>{{ node.name }}</span>
            </div>
            <el-tag size="small" effect="dark" :type="protocolType(node.protocol)">
              {{ node.protocol.toUpperCase() }}
            </el-tag>
          </div>
          <div class="node-addr">{{ node.server_address }}:{{ node.port }}</div>
          <div class="node-meta">
            <div class="meta-item">
              <span class="meta-label">状态</span>
              <span class="meta-value" :class="node.online ? 'good' : 'slow'">
                {{ node.online ? '在线' : '离线' }}
              </span>
            </div>
            <div class="meta-item">
              <span class="meta-label">地区</span>
              <span class="meta-value">{{ node.country_code || 'XX' }}</span>
            </div>
            <div class="meta-item">
              <span class="meta-label">协议</span>
              <span class="meta-value">{{ node.protocol.toUpperCase() }}</span>
            </div>
          </div>
          <!-- 节点近 5 分钟流量（按 5min 折算成瞬时速度） -->
          <div class="node-load" v-if="node.online">
            <div class="load-row">
              <span class="load-label">近 5 分钟下行</span>
              <span class="load-value down">{{ formatSpeed(node.recent5m_dn * 8 / 300) }}</span>
            </div>
            <div class="load-row">
              <span class="load-label">近 5 分钟上行</span>
              <span class="load-value up">{{ formatSpeed(node.recent5m_up * 8 / 300) }}</span>
            </div>
          </div>
        </div>
      </el-col>
    </el-row>

    <el-empty v-if="!loading && !nodeList.length" description="暂无可用节点" />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import request from '@/utils/request'
import { formatSpeed } from '@/utils/format'

interface NodeItem {
  id: string
  name: string
  country_code: string
  protocol: string
  server_address: string
  port: number
  online: boolean
  recent5m_up?: number
  recent5m_dn?: number
}

const loading = ref(false)
const nodeList = ref<NodeItem[]>([])

type TagType = 'primary' | 'success' | 'info' | 'warning' | 'danger'
const protocolType = (p: string): TagType => {
  const map: Record<string, TagType> = { vless: 'success', vmess: 'primary', trojan: 'warning', shadowsocks: 'info' }
  return map[p] || 'primary'
}

const loadData = async () => {
  loading.value = true
  try {
    const res = await request.get<{ code: number; data: { list: NodeItem[] } }>('/api/v1/nodes/list')
    if (res && res.code === 0 && res.data) {
      nodeList.value = res.data.list || []
    }
  } catch {
    nodeList.value = []
  } finally {
    loading.value = false
  }
}

let timer: number | null = null
let isVisible = true
// 标签页隐藏时暂停轮询，节省资源（切回自动恢复+立即刷新一次）
const handleVisibility = () => {
  const nowVisible = !document.hidden
  if (nowVisible === isVisible) return
  isVisible = nowVisible
  if (isVisible) {
    loadData()
    timer = window.setInterval(loadData, 10000)
  } else if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
}

onMounted(() => {
  loadData()
  // 每 10s 自动刷新（含节点在线状态与近 5min 流量速率）
  timer = window.setInterval(loadData, 10000)
  document.addEventListener('visibilitychange', handleVisibility)
})

onBeforeUnmount(() => {
  if (timer !== null) {
    clearInterval(timer)
    timer = null
  }
  document.removeEventListener('visibilitychange', handleVisibility)
})
</script>

<style scoped>
.user-nodes { overflow-x: hidden; }
.page-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 20px; }
.page-title { margin: 0; font-size: 20px; color: var(--np-text); }
.node-card { padding: 18px; margin-bottom: 20px; transition: all 0.3s ease; overflow: hidden; }
.node-card:hover { transform: translateY(-2px); border-color: var(--np-primary-dim); box-shadow: 0 0 24px var(--np-primary-dim); }
.node-card.offline { opacity: 0.6; }
.node-top { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
.node-name { display: flex; align-items: center; gap: 8px; font-size: 16px; font-weight: 600; color: var(--np-text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.node-addr { font-size: 13px; color: var(--np-text-secondary); margin-bottom: 16px; font-family: monospace; overflow: hidden; text-overflow: ellipsis; }
.node-meta { display: flex; justify-content: space-between; padding: 12px 0; border-top: 1px solid var(--np-border); border-bottom: 1px solid var(--np-border); }
.meta-item { display: flex; flex-direction: column; gap: 4px; align-items: center; }
.meta-label { font-size: 11px; color: var(--np-text-muted); }
.meta-value { font-size: 13px; color: var(--np-text); font-weight: 600; }
.meta-value.good { color: var(--np-primary); }
.meta-value.normal { color: var(--np-warning); }
.meta-value.slow { color: var(--np-danger); }
.node-load { margin-top: 12px; display: flex; flex-direction: column; gap: 6px; }
.load-row { display: flex; align-items: center; justify-content: space-between; }
.load-label { font-size: 11px; color: var(--np-text-muted); }
.load-value { font-size: 12px; color: var(--np-text); font-weight: 600; font-family: monospace; }
.load-value.up { color: #ffbe0b; }
.load-value.down { color: var(--np-primary); }

@media (max-width: 768px) {
  .page-header { flex-direction: column; align-items: flex-start; gap: 12px; }
  .node-card { padding: 14px; }
  .node-top { flex-wrap: wrap; gap: 8px; }
  .node-name { font-size: 14px; }
  .node-meta { gap: 8px; }
  .meta-item { flex: 1; }
}
</style>
