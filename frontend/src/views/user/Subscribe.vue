<template>
  <div class="user-subscribe">
    <!-- 顶部统计卡片 -->
    <el-row :gutter="16" class="stat-row">
      <el-col :xs="12" :sm="6">
        <div class="np-card stat-card">
          <div class="stat-icon traffic"><el-icon><DataLine /></el-icon></div>
          <div class="stat-info">
            <div class="stat-label">流量使用</div>
            <div class="stat-value">{{ formatTraffic(trafficUsed) }}<span class="stat-unit" v-if="trafficLimit"> / {{ formatTraffic(trafficLimit) }}</span></div>
          </div>
        </div>
      </el-col>
      <el-col :xs="12" :sm="6">
        <div class="np-card stat-card">
          <div class="stat-icon expire"><el-icon><Timer /></el-icon></div>
          <div class="stat-info">
            <div class="stat-label">到期时间</div>
            <div class="stat-value">{{ formatDate(expiredAt) }}</div>
          </div>
        </div>
      </el-col>
      <el-col :xs="12" :sm="6">
        <div class="np-card stat-card">
          <div class="stat-icon node"><el-icon><Cpu /></el-icon></div>
          <div class="stat-info">
            <div class="stat-label">可用节点</div>
            <div class="stat-value">{{ nodes.length }} 个</div>
          </div>
        </div>
      </el-col>
      <el-col :xs="12" :sm="6">
        <div class="np-card stat-card">
          <div class="stat-icon status"><el-icon><CircleCheck /></el-icon></div>
          <div class="stat-info">
            <div class="stat-label">账号状态</div>
            <div class="stat-value" :class="statusClass">{{ statusText }}</div>
          </div>
        </div>
      </el-col>
    </el-row>

    <el-row :gutter="20">
      <!-- 订阅链接 + 二维码 -->
      <el-col :xs="24" :md="14">
        <div class="np-card sub-card">
          <div class="card-header">
            <div class="card-title">订阅链接</div>
            <div class="header-actions">
              <el-radio-group v-model="format" @change="updateUrl" size="small">
                <el-radio-button value="clash">Clash</el-radio-button>
                <el-radio-button value="sing-box">Sing-Box</el-radio-button>
                <el-radio-button value="v2ray">V2Ray</el-radio-button>
              </el-radio-group>
              <!-- 修复 P0-FE8: 刷新订阅链接 -->
              <el-button size="small" text type="primary" @click="fetchUserInfo" :icon="Refresh" :loading="!qrReady && !!subscribeError">
                刷新
              </el-button>
            </div>
          </div>

          <el-input :model-value="subscribeUrl" readonly size="large" class="sub-input" :disabled="!!subscribeError">
            <template #append>
              <el-button @click="copyLink" :icon="CopyDocument" :disabled="!!subscribeError">复制链接</el-button>
            </template>
          </el-input>

          <!-- 修复 P0-FE8: 错误提示 -->
          <div v-if="subscribeError" class="sub-error">
            <el-icon><Warning /></el-icon>
            <span>{{ subscribeError }}</span>
          </div>

          <div class="qr-section">
            <div class="qr-box">
              <canvas ref="qrCanvas"></canvas>
              <div v-if="!qrReady" class="qr-placeholder">
                <el-icon v-if="!subscribeError" class="is-loading"><Loading /></el-icon>
                <el-icon v-else><Warning /></el-icon>
                <span>{{ subscribeError ? '订阅链接异常' : '生成中...' }}</span>
              </div>
            </div>
            <div class="qr-tip">
              <p class="tip-title">扫码导入订阅</p>
              <p class="tip-desc">使用 Clash / V2RayN / Shadowrocket 等客户端扫码即可导入全部节点</p>
              <div class="qr-actions">
                <el-button text type="primary" @click="downloadQr" :icon="Download">下载二维码</el-button>
                <el-button text type="primary" @click="copyLink" :icon="CopyDocument">复制链接</el-button>
              </div>
            </div>
          </div>
        </div>
      </el-col>

      <!-- 客户端导入指南 -->
      <el-col :xs="24" :md="10">
        <div class="np-card sub-card">
          <div class="card-title">客户端导入指南</div>
          <el-collapse v-model="activeGuide">
            <el-collapse-item v-for="g in guides" :key="g.name" :title="g.name" :name="g.name">
              <ol class="guide-steps">
                <li v-for="(step, i) in g.steps" :key="i">{{ step }}</li>
              </ol>
            </el-collapse-item>
          </el-collapse>
        </div>
      </el-col>
    </el-row>

    <!-- 节点列表 + 单节点分享 -->
    <div class="np-card sub-card" style="margin-top: 20px">
      <div class="card-header">
        <div class="card-title">节点列表与分享</div>
        <el-button text type="primary" @click="refreshNodes" :icon="Refresh" :loading="nodesLoading">刷新</el-button>
      </div>

      <div v-if="nodes.length === 0 && !nodesLoading" class="empty-state">
        <el-empty description="暂无可用节点" />
      </div>

      <div v-else class="node-grid">
        <div v-for="n in nodes" :key="n.id" class="node-item" :class="{ offline: !n.online }">
          <div class="node-header">
            <div class="node-name">
              <span class="node-dot" :class="n.online ? 'online' : 'offline'"></span>
              {{ n.name }}
            </div>
            <el-tag size="small" :type="n.online ? 'success' : 'info'" effect="dark">
              {{ n.online ? '在线' : '离线' }}
            </el-tag>
          </div>
          <div class="node-info">
            <div class="info-row">
              <span class="info-label">地址</span>
              <span class="info-value">{{ n.server_address }}:{{ n.port }}</span>
            </div>
            <div class="info-row">
              <span class="info-label">协议</span>
              <span class="info-value">{{ (n.protocol || 'vless').toUpperCase() }}</span>
            </div>
            <div class="info-row" v-if="n.connect && n.connect.sni">
              <span class="info-label">SNI</span>
              <span class="info-value">{{ n.connect.sni }}</span>
            </div>
          </div>
          <div class="node-actions" v-if="n.connect">
            <el-button size="small" type="primary" @click="copyNodeShare(n)" :icon="CopyDocument">
              复制分享
            </el-button>
            <el-button size="small" @click="showNodeQr(n)" :icon="FullScreen">
              二维码
            </el-button>
          </div>
        </div>
      </div>
    </div>

    <!-- 节点二维码弹窗 -->
    <el-dialog v-model="qrDialogVisible" :title="qrDialogTitle" width="360px" center top="8vh" append-to-body>
      <div class="dialog-qr-container">
        <canvas ref="nodeQrCanvas"></canvas>
        <div v-if="!nodeQrReady" class="qr-placeholder">
          <el-icon class="is-loading"><Loading /></el-icon>
          <span>生成中...</span>
        </div>
      </div>
      <div class="dialog-share-url">{{ currentShareUrl }}</div>
      <template #footer>
        <el-button @click="qrDialogVisible = false">关闭</el-button>
        <el-button type="primary" @click="copyCurrentShare" :icon="CopyDocument">复制链接</el-button>
        <el-button type="success" @click="downloadNodeQr" :icon="Download">下载二维码</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, nextTick, watch } from 'vue'
import QRCode from 'qrcode'
import { ElMessage } from 'element-plus'
import { CopyDocument, Download, Refresh, Loading, DataLine, Timer, Cpu, CircleCheck, FullScreen, Warning } from '@element-plus/icons-vue'
import { formatTime, formatTraffic } from '@/utils/format'
import { copyToClipboard, utf8ToBase64 } from '@/utils/clipboard'
import request from '@/utils/request'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

interface ApiResponse<T> { code: number; msg: string; data: T }
interface ConnectInfo {
  uuid?: string
  sni?: string
  public_key?: string
  short_id?: string
  flow?: string
  security?: string
  fingerprint?: string
  password?: string
  method?: string
}
interface NodeItem {
  id: string
  name: string
  country_code: string
  protocol: string
  server_address: string
  port: number
  online: boolean
  connect?: ConnectInfo
}
interface UserInfo {
  subscribe_url: string
  username: string
  traffic_limit: number
  traffic_used: number
  expired_at: string
  status: string
}

const format = ref('clash')
const qrCanvas = ref<HTMLCanvasElement>()
const nodeQrCanvas = ref<HTMLCanvasElement>()
const qrReady = ref(false)
const nodeQrReady = ref(false)
const activeGuide = ref('Clash')

const rawSubscribeUrl = ref('')
const subscribeUrl = ref('')
// 修复 P0-FE8: 订阅链接为空/获取失败时的错误提示与重试
const subscribeError = ref('')

const trafficUsed = ref(0)
const trafficLimit = ref(0)
const expiredAt = ref('')
const statusText = ref('正常')
const statusClass = ref('ok')

const nodes = ref<NodeItem[]>([])
const nodesLoading = ref(false)

// 节点二维码弹窗
const qrDialogVisible = ref(false)
const qrDialogTitle = ref('')
const currentShareUrl = ref('')

const buildUrlByFormat = (url: string, fmt: string): string => {
  try {
    const u = new URL(url)
    u.searchParams.set('type', fmt)
    return u.toString()
  } catch {
    return url
  }
}

const updateUrl = () => {
  if (!rawSubscribeUrl.value) return
  subscribeUrl.value = buildUrlByFormat(rawSubscribeUrl.value, format.value)
  renderQr()
}

// 通用复制函数已抽到 @/utils/clipboard

const fetchUserInfo = async () => {
  subscribeError.value = ''
  try {
    const res = await request.get<ApiResponse<UserInfo>>('/api/v1/user/info')
    if (res && res.code === 0 && res.data) {
      if (res.data.subscribe_url) {
        rawSubscribeUrl.value = res.data.subscribe_url
        subscribeUrl.value = buildUrlByFormat(res.data.subscribe_url, format.value)
        await renderQr()
      } else {
        // 修复 P0-FE8: 订阅链接为空时给出明确提示, 便于用户重试或联系管理员
        subscribeError.value = '订阅链接为空，请刷新重试或联系管理员'
        qrReady.value = false
      }
      trafficUsed.value = res.data.traffic_used || 0
      trafficLimit.value = res.data.traffic_limit || 0
      expiredAt.value = res.data.expired_at || ''
      // 状态判断
      if (res.data.status === 'disabled') {
        statusText.value = '已禁用'
        statusClass.value = 'disabled'
      } else if (res.data.expired_at && new Date(res.data.expired_at) < new Date()) {
        statusText.value = '已到期'
        statusClass.value = 'expired'
      } else if (res.data.traffic_limit > 0 && res.data.traffic_used >= res.data.traffic_limit) {
        statusText.value = '流量耗尽'
        statusClass.value = 'expired'
      } else {
        statusText.value = '正常'
        statusClass.value = 'ok'
      }
    }
  } catch {
    subscribeError.value = '订阅信息获取失败，请刷新重试'
    qrReady.value = false
  }
}

const refreshNodes = async () => {
  nodesLoading.value = true
  try {
    const res = await request.get<ApiResponse<{ list: NodeItem[]; total: number }>>('/api/v1/nodes/list')
    if (res && res.code === 0 && res.data) {
      nodes.value = res.data.list || []
    }
  } catch { /* */ }
  nodesLoading.value = false
}

const renderQr = async () => {
  await nextTick()
  if (!qrCanvas.value || !subscribeUrl.value) return
  try {
    await QRCode.toCanvas(qrCanvas.value, subscribeUrl.value, {
      width: 256,
      margin: 3,
      errorCorrectionLevel: 'M',
      color: { dark: '#000000', light: '#ffffff' },
    })
    qrReady.value = true
  } catch {
    qrReady.value = false
  }
}

const copyLink = async () => {
  if (!subscribeUrl.value) {
    ElMessage.warning('订阅链接为空')
    return
  }
  const ok = await copyToClipboard(subscribeUrl.value)
  if (ok) {
    ElMessage.success('订阅链接已复制到剪贴板')
  } else {
    ElMessage.warning('复制失败，请手动选择链接复制')
  }
}

const downloadQr = () => {
  if (!qrCanvas.value) return
  const link = document.createElement('a')
  link.download = 'nexus-subscribe-qr.png'
  link.href = qrCanvas.value.toDataURL()
  link.click()
  ElMessage.success('二维码已下载')
}

// 生成单节点 V2Ray 分享链接
const buildNodeShareUrl = (n: NodeItem): string => {
  if (!n.connect) return ''
  const c = n.connect
  const proto = (n.protocol || 'vless').toLowerCase()
  if (proto === 'vless') {
    const params = new URLSearchParams()
    params.set('encryption', 'none')
    if (c.security === 'reality') {
      params.set('security', 'reality')
      // REALITY 必须有 SNI，否则 TLS 握手失败；后端已做默认，此处兜底
      params.set('sni', c.sni || 'gateway.icloud.com')
      if (c.public_key) params.set('pbk', c.public_key)
      if (c.short_id) params.set('sid', c.short_id)
      params.set('fp', c.fingerprint || 'chrome')
      if (c.flow) params.set('flow', c.flow)
    }
    params.set('type', 'tcp')
    return `vless://${c.uuid}@${n.server_address}:${n.port}?${params.toString()}#${encodeURIComponent(n.name)}`
  }
  if (proto === 'vmess') {
    const obj = {
      v: '2', ps: n.name, add: n.server_address, port: n.port,
      id: c.uuid, aid: 0, scy: c.method || 'auto', net: 'tcp',
    }
    return `vmess://${utf8ToBase64(JSON.stringify(obj))}`
  }
  if (proto === 'trojan') {
    return `trojan://${c.password}@${n.server_address}:${n.port}?sni=${c.sni || ''}#${encodeURIComponent(n.name)}`
  }
  if (proto === 'shadowsocks' || proto === 'ss') {
    const userinfo = btoa(`${c.method}:${c.password}`)
    return `ss://${userinfo}@${n.server_address}:${n.port}#${encodeURIComponent(n.name)}`
  }
  return ''
}

const copyNodeShare = async (n: NodeItem) => {
  const url = buildNodeShareUrl(n)
  if (!url) {
    ElMessage.warning('无法生成分享链接')
    return
  }
  const ok = await copyToClipboard(url)
  if (ok) {
    ElMessage.success(`${n.name} 分享链接已复制`)
  } else {
    ElMessage.warning('复制失败，请手动复制')
  }
}

const showNodeQr = async (n: NodeItem) => {
  const url = buildNodeShareUrl(n)
  if (!url) {
    ElMessage.warning('无法生成分享链接')
    return
  }
  currentShareUrl.value = url
  qrDialogTitle.value = `${n.name} - 节点二维码`
  qrDialogVisible.value = true
  nodeQrReady.value = false
  await nextTick()
  if (!nodeQrCanvas.value) return
  try {
    await QRCode.toCanvas(nodeQrCanvas.value, url, {
      width: 280,
      margin: 3,
      errorCorrectionLevel: 'M',
      color: { dark: '#000000', light: '#ffffff' },
    })
    nodeQrReady.value = true
  } catch {
    nodeQrReady.value = false
  }
}

const copyCurrentShare = async () => {
  if (!currentShareUrl.value) {
    ElMessage.warning('分享链接为空')
    return
  }
  const ok = await copyToClipboard(currentShareUrl.value)
  if (ok) {
    ElMessage.success('分享链接已复制')
  } else {
    ElMessage.warning('复制失败，请手动选择链接复制')
  }
}

const downloadNodeQr = () => {
  if (!nodeQrCanvas.value) return
  const link = document.createElement('a')
  link.download = `nexus-node-${Date.now()}.png`
  link.href = nodeQrCanvas.value.toDataURL()
  link.click()
  ElMessage.success('二维码已下载')
}

const formatDate = (dateStr: string): string => {
  if (!dateStr) return '永久'
  try {
    const d = new Date(dateStr)
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  } catch {
    return dateStr
  }
}

const guides = [
  {
    name: 'Clash Meta',
    steps: [
      '下载并安装 Clash Meta 客户端',
      '打开客户端，进入订阅或Profiles页面',
      '粘贴上方订阅链接，点击导入',
      '选择导入的配置，启动系统代理即可使用',
    ],
  },
  {
    name: 'Sing-Box',
    steps: [
      '下载并安装 Sing-Box 客户端',
      '进入Profiles页面，点击新增',
      '类型选择 Remote，粘贴订阅链接',
      '保存后选中该配置，点击启动',
    ],
  },
  {
    name: 'V2RayN / V2RayNG',
    steps: [
      '下载 V2RayN（Windows）或 V2RayNG（Android）',
      '打开软件，点击订阅 → 订阅设置',
      '新增订阅，粘贴链接并保存',
      '返回主界面，更新订阅后选择节点',
    ],
  },
  {
    name: 'Shadowrocket',
    steps: [
      'App Store 下载 Shadowrocket',
      '点击右上角 + 号，类型选择 Subscribe',
      '粘贴订阅链接，保存',
      '选中订阅，开启连接开关',
    ],
  },
]

onMounted(() => {
  fetchUserInfo()
  refreshNodes()
})

watch(format, () => updateUrl())
</script>

<style scoped>
.stat-row { margin-bottom: 16px; }
.stat-card { display: flex; align-items: center; gap: 14px; padding: 16px 18px; }
.stat-icon { width: 44px; height: 44px; border-radius: 10px; display: flex; align-items: center; justify-content: center; font-size: 22px; }
.stat-icon.traffic { background: rgba(64, 158, 255, 0.12); color: #409eff; }
.stat-icon.expire { background: rgba(230, 162, 60, 0.12); color: #e6a23c; }
.stat-icon.node { background: rgba(103, 194, 58, 0.12); color: #67c23a; }
.stat-icon.status { background: rgba(0, 245, 212, 0.12); color: #00f5d4; }
.stat-info { flex: 1; min-width: 0; }
.stat-label { font-size: 12px; color: var(--np-text-secondary, #8b98a9); margin-bottom: 4px; }
.stat-value { font-size: 18px; font-weight: 600; color: var(--np-text, #e7ecf3); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.stat-value.ok { color: #67c23a; }
.stat-value.expired { color: #f56c6c; }
.stat-value.disabled { color: #909399; }
.stat-unit { font-size: 13px; font-weight: 400; color: var(--np-text-secondary, #8b98a9); }

.sub-card { padding: 20px; height: 100%; box-sizing: border-box; }
.card-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; flex-wrap: wrap; gap: 10px; }
.card-title { font-size: 15px; font-weight: 600; color: var(--np-text, #e7ecf3); }
.header-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }

.sub-error {
  margin-top: 10px;
  padding: 10px 12px;
  background: rgba(245, 108, 108, 0.12);
  border: 1px solid rgba(245, 108, 108, 0.3);
  border-radius: 8px;
  color: #f56c6c;
  font-size: 13px;
  display: flex;
  align-items: center;
  gap: 8px;
}

.sub-input :deep(.el-input__inner) { font-size: 13px; }

.qr-section { display: flex; align-items: center; gap: 24px; margin-top: 20px; flex-wrap: wrap; }
.qr-box { width: 256px; height: 256px; background: #fff; border-radius: 12px; padding: 8px; display: flex; align-items: center; justify-content: center; position: relative; box-shadow: 0 4px 12px rgba(0,0,0,0.15); }
.qr-box canvas { border-radius: 8px; }
.qr-placeholder { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; color: #8b98a9; font-size: 13px; }
.qr-tip { flex: 1; min-width: 200px; }
.tip-title { color: var(--np-text, #e7ecf3); font-size: 15px; font-weight: 600; margin: 0 0 8px; }
.tip-desc { color: var(--np-text-secondary, #8b98a9); font-size: 13px; line-height: 1.6; margin: 0 0 12px; }
.qr-actions { display: flex; gap: 8px; flex-wrap: wrap; }

.guide-steps { margin: 0; padding-left: 18px; color: var(--np-text-secondary, #8b98a9); font-size: 13px; line-height: 1.8; }
.guide-steps :deep(strong) { color: var(--np-primary, #00f5d4); }

.empty-state { padding: 40px 0; }
.node-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 16px; }
.node-item { background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.06); border-radius: 12px; padding: 16px; transition: all 0.2s; overflow: hidden; }
.node-item:hover { border-color: rgba(0, 245, 212, 0.3); transform: translateY(-2px); }
.node-item.offline { opacity: 0.6; }
.node-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.node-name { font-size: 14px; font-weight: 600; color: var(--np-text, #e7ecf3); display: flex; align-items: center; gap: 8px; }
.node-dot { width: 8px; height: 8px; border-radius: 50%; }
.node-dot.online { background: #67c23a; box-shadow: 0 0 6px #67c23a; }
.node-dot.offline { background: #909399; }
.node-info { margin-bottom: 12px; overflow: hidden; }
.info-row { display: flex; justify-content: space-between; font-size: 12px; padding: 3px 0; }
.info-label { color: var(--np-text-secondary, #8b98a9); }
.info-value { color: var(--np-text, #e7ecf3); font-family: monospace; }
.node-actions { display: flex; gap: 8px; }

.dialog-qr-container { display: flex; justify-content: center; align-items: center; width: 300px; height: 300px; margin: 0 auto; background: #fff; border-radius: 12px; padding: 8px; position: relative; overflow: hidden; box-sizing: border-box; }
.dialog-qr-container canvas { max-width: 100%; max-height: 100%; }
.dialog-share-url { margin-top: 16px; padding: 10px; background: rgba(255,255,255,0.04); border-radius: 8px; font-size: 11px; font-family: monospace; color: var(--np-text-secondary, #8b98a9); word-break: break-all; max-height: 80px; overflow-y: auto; }

/* 确保弹窗在视窗内完整显示 */
:deep(.el-dialog) { max-height: 84vh; margin-top: 8vh !important; }
:deep(.el-dialog__body) { max-height: calc(84vh - 110px - 60px); overflow-y: auto; padding: 16px 20px; }

@media (max-width: 768px) {
  .dash-card { padding: 14px; }
  .stat-card { padding: 14px; }
  .stat-icon { width: 40px; height: 40px; font-size: 18px; }
  .stat-value { font-size: 16px; }
  .card-header { flex-direction: column; align-items: flex-start; }
  .header-actions { width: 100%; }
  .header-actions .el-button,
  .header-actions .el-input {
    flex: 1;
    min-width: 0;
  }
  .qr-section { flex-direction: column; align-items: center; gap: 16px; }
  .qr-box { width: 200px; height: 200px; }
  .qr-tip { min-width: 0; width: 100%; text-align: center; }
  .qr-actions { justify-content: center; }
  .node-grid { grid-template-columns: 1fr; }
  .node-actions { flex-wrap: wrap; }
}
</style>
