<template>
  <el-dialog v-model="visible" title="清理并删除节点" :width="dialogWidth" top="5vh" class="cleanup-progress-dialog" :close-on-click-modal="false" :show-close="!running">
    <div class="cp-container">
      <!-- 未开始：SSH 凭据输入 -->
      <div v-if="!started" class="cp-pwd-bar">
        <el-alert type="warning" :closable="false" show-icon style="margin-bottom:12px">
          <template #title>
            面板会 SSH 到节点服务器, 停止 agent 容器, 删除部署目录(.env.node / 二进制 / xray-cache), 最后从面板 DB 删除. 全自动, 失败步骤会跳过但 DB 清理最终一定执行.
          </template>
        </el-alert>
        <!-- 认证方式切换 -->
        <div class="cp-auth-row">
          <span class="cp-auth-label">认证方式:</span>
          <el-radio-group v-model="authMode" size="small">
            <el-radio-button value="password">密码</el-radio-button>
            <el-radio-button value="key">SSH 密钥</el-radio-button>
          </el-radio-group>
          <el-input v-model="username" placeholder="用户" class="cp-input-user" />
          <el-input-number v-model="port" :min="1" :max="65535" controls-position="right" class="cp-input-port" />
          <el-checkbox v-model="removeImg">删除镜像</el-checkbox>
          <el-button type="danger" :disabled="!canStart" @click="start">
            <el-icon><Delete /></el-icon> 开始清理
          </el-button>
        </div>
        <!-- 密码模式 -->
        <div v-if="authMode === 'password'" class="cp-pwd-row">
          <el-input v-model="password" type="password" show-password placeholder="节点服务器密码" class="cp-input-pwd" @keyup.enter="start" autocomplete="new-password" name="cleanup-pwd" />
          <span class="cp-pwd-hint">输入 root 或其他 sudo 用户的密码</span>
        </div>
        <!-- SSH 密钥模式 -->
        <div v-if="authMode === 'key'" class="cp-key-col">
          <div class="cp-key-row">
            <el-upload
              :auto-upload="false"
              :show-file-list="false"
              :on-change="onKeyFileChange"
              accept="*"
              style="display:inline-flex"
            >
              <el-button size="small" type="primary" plain>选择私钥文件</el-button>
            </el-upload>
            <span class="cp-pwd-hint">或直接粘贴私钥内容</span>
            <el-button size="small" link type="primary" @click="showKeyHelp = !showKeyHelp">
              {{ showKeyHelp ? '收起' : '如何获取私钥?' }}
            </el-button>
          </div>
          <el-input
            v-model="privateKey"
            type="textarea"
            :rows="5"
            placeholder="粘贴 SSH 私钥内容 (PEM 格式)&#10;-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
            class="cp-key-textarea"
          />
          <el-alert v-if="showKeyHelp" type="info" :closable="false" style="margin-top:4px">
            <template #title>
              <div style="font-size:12px;line-height:1.6">
                1. 生成密钥对: <code>ssh-keygen -t ed25519 -f ~/.ssh/nexus_deploy -N ""</code><br/>
                2. 公钥写入服务器: <code>ssh-copy-id -i ~/.ssh/nexus_deploy.pub root@节点IP</code><br/>
                3. 查看私钥: <code>cat ~/.ssh/nexus_deploy</code> 并复制全部内容粘贴到上方<br/>
                4. 注意: 私钥不要设置密码 ( -N "" )，否则无法在此使用
              </div>
            </template>
          </el-alert>
        </div>
        <el-alert type="info" :closable="false" style="margin-top:12px">
          <template #title>不提供凭据关闭弹窗将仅从面板侧删除(节点服务器残留资源需手动清理).</template>
        </el-alert>
      </div>

      <!-- Progress: 5 步进度展示 -->
      <div v-else class="cp-progress">
        <!-- 5 步进度条 (已到达的步骤才显示, 配合 TransitionGroup 淡入) -->
        <div class="cp-steps-bar" :class="{ 'cp-steps-vertical': isMobile }">
          <TransitionGroup name="cp-step">
            <div
              v-for="step in visibleSteps"
              :key="step.key"
              class="cp-step-bar"
              :class="getStepBarClass(step.key)"
            >
              <div class="cp-step-num">{{ step.index + 1 }}</div>
              <div class="cp-step-name">{{ step.name }}</div>
            </div>
          </TransitionGroup>
        </div>

        <!-- 详细事件流 (渐进式展示, 每个事件独立渲染, 智能滚动) -->
        <div class="cp-events" ref="eventsContainer" @scroll="onEventsScroll">
          <TransitionGroup name="cp-ev" tag="div">
            <div v-for="(ev, i) in displayedEvents" :key="(ev as any)._id || i" class="cp-event" :class="ev.status">
              <div class="cp-ev-head">
                <span class="cp-ev-icon">
                  <span v-if="ev.status === 'running'" class="cp-spin">&#9679;</span>
                  <span v-else-if="ev.status === 'done'">&#10003;</span>
                  <span v-else-if="ev.status === 'error'">&#10007;</span>
                  <span v-else-if="ev.status === 'warning'">&#9888;</span>
                  <span v-else-if="ev.status === 'log'" class="cp-ev-dot">·</span>
                  <span v-else>·</span>
                </span>
                <span class="cp-ev-step">{{ stepName(ev.step) }}</span>
                <el-tag v-if="isStatus(ev, 'done')" size="small" type="success">完成</el-tag>
                <el-tag v-else-if="isStatus(ev, 'error')" size="small" type="danger">失败</el-tag>
                <el-tag v-else-if="isStatus(ev, 'running')" size="small" type="warning">进行中</el-tag>
                <el-tag v-else-if="isStatus(ev, 'warning')" size="small" type="warning" effect="dark">警告</el-tag>
              </div>
              <div v-if="ev.msg" class="cp-ev-msg">{{ ev.msg }}</div>
              <pre v-if="ev.output" class="cp-ev-output">{{ ev.output }}</pre>
            </div>
          </TransitionGroup>
        </div>

        <!-- 滚动到底部提示 (用户向上滚查看历史时显示) -->
        <Transition name="cp-fade">
          <div v-if="userScrolledUp && running" class="cp-scroll-hint" @click="scrollToBottom(true)">
            ↓ 新日志到达, 点击回到最新
          </div>
        </Transition>

        <div v-if="running" class="cp-loading">
          <el-icon class="is-loading"><Loading /></el-icon>
          清理中...
        </div>
        <div v-else-if="finished" class="cp-done">
          <el-button type="primary" @click="close">完成</el-button>
        </div>
      </div>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch, computed, nextTick, onMounted, onUnmounted } from 'vue'
import { ElIcon, ElMessage } from 'element-plus'
import { Delete, Loading } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'

interface Ev { step: string; status: string; msg: string; output: string; _id?: number }

const props = defineProps<{
  modelValue: boolean
  nodeId: string
  nodeName?: string
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void; (e: 'done'): void }>()

const visible = ref(props.modelValue)
watch(() => props.modelValue, (v) => { visible.value = v; resetIfClosed(v) })
watch(visible, (v) => emit('update:modelValue', v))

// 响应式断点: 监听 resize, 横竖屏切换时更新 (修复 P1-6: 旧版只算一次)
const isMobile = ref(typeof window !== 'undefined' && window.innerWidth < 768)
const onResize = () => { isMobile.value = window.innerWidth < 768 }
onMounted(() => window.addEventListener('resize', onResize))
onUnmounted(() => window.removeEventListener('resize', onResize))

// 对话框宽度: PC 固定 720px, 手机 95vw
const dialogWidth = computed(() => (isMobile.value ? '95%' : '720px'))

// [FIX 2026-07-21] 与 DeployProgress.vue 对齐, 新增 authMode/privateKey/showKeyHelp
const authMode = ref<'password' | 'key'>('password')
const password = ref('')
const privateKey = ref('')
const showKeyHelp = ref(false)
const username = ref('root')
const port = ref(22)
const removeImg = ref(false)
const started = ref(false)
const running = ref(false)
const finished = ref(false)
const events = ref<Ev[]>([])

// 是否允许开始清理 (密码模式需密码, 密钥模式需私钥)
const canStart = computed(() => {
  if (authMode.value === 'key') return !!privateKey.value
  return !!password.value
})

// 选择私钥文件后, 读取内容填到 privateKey
const onKeyFileChange = (file: any) => {
  const raw = file?.raw
  if (!raw) return
  const reader = new FileReader()
  reader.onload = (e) => {
    privateKey.value = (e.target?.result as string) || ''
  }
  reader.onerror = () => {
    ElMessage.error('私钥文件读取失败')
  }
  reader.readAsText(raw)
}

// Progressive reveal: push displayedEvents one by one with CSS transition animation
const displayedEvents = ref<Ev[]>([])
let revealTimer: ReturnType<typeof setInterval> | null = null
let eventIdCounter = 0

const eventsContainer = ref<HTMLElement>()

// 智能滚动: 用户向上滚查看历史时, 不强制拉回底部 (修复 P2-4)
const userScrolledUp = ref(false)

const onEventsScroll = () => {
  if (!eventsContainer.value) return
  const { scrollTop, scrollHeight, clientHeight } = eventsContainer.value
  userScrolledUp.value = scrollHeight - scrollTop - clientHeight > 50
}

const scrollToBottom = async (force = false) => {
  if (!force && userScrolledUp.value) return
  await nextTick()
  if (eventsContainer.value) {
    eventsContainer.value.scrollTop = eventsContainer.value.scrollHeight
    userScrolledUp.value = false
  }
}

// 5-step phase definition
const phaseSteps = [
  { key: 'connect', name: 'SSH连接' },
  { key: 'stop', name: '停容器' },
  { key: 'dir', name: '删目录' },
  { key: 'image', name: '删镜像' },
  { key: 'finalize', name: 'DB清理' },
]

const activePhase = ref<string>('')

// 只渲染已到达的步骤, 配合 TransitionGroup 淡入 (修复 P3-4)
const visibleSteps = computed(() => {
  const currentIdx = phaseSteps.findIndex(s => s.key === activePhase.value)
  if (currentIdx < 0) return []
  return phaseSteps.slice(0, currentIdx + 1).map((s, i) => ({ ...s, index: i }))
})

// [FIX 2026-07-21] 中文化 step 名称 (与项目其他界面一致)
const stepNames: Record<string, string> = {
  connect: '1. SSH 连接节点服务器',
  stop: '2. 停止并删除 agent 容器',
  dir: '3. 删除部署目录',
  image: '4. 删除 docker 镜像',
  finalize: '5. 面板 DB + Redis 清理',
  finish: '清理完成',
}
const stepName = (s: string) => stepNames[s] || s

const isStatus = (ev: Ev, s: string) => ev.status === s

const getStepBarClass = (key: string) => {
  const currentIdx = phaseSteps.findIndex(s => s.key === activePhase.value)
  const thisIdx = phaseSteps.findIndex(s => s.key === key)
  if (thisIdx < 0) return ''
  if (currentIdx > thisIdx) return 'done'
  if (currentIdx === thisIdx) return 'active'
  return ''
}

const resetIfClosed = (v: boolean) => {
  if (!v) {
    setTimeout(() => {
      started.value = false
      running.value = false
      finished.value = false
      events.value = []
      displayedEvents.value = []
      activePhase.value = ''
      userScrolledUp.value = false
      // [FIX 2026-07-21] 关闭弹窗时同时清空密钥, 避免缓存残留导致下次清理用错凭证
      password.value = ''
      privateKey.value = ''
      showKeyHelp.value = false
      removeImg.value = false
      stopReveal()
    }, 300)
  }
}

// 渐进式 reveal timer: 每 60ms push 一条事件到 displayedEvents
// 修复 P3-5: 终止时不再一次性 dump 剩余, 保持原节奏推完
// 修复 P2-5: 60ms 比 80ms 更快, 减少积压
const startReveal = () => {
  stopReveal()
  let idx = 0
  revealTimer = setInterval(() => {
    if (idx < events.value.length) {
      displayedEvents.value.push(events.value[idx])
      idx++
      scrollToBottom()
    } else {
      // 当前已无积压
      if (!running.value) {
        // 已结束且无积压, 停止 timer
        stopReveal()
      }
      // 还在运行就等下一轮 (60ms 后再检查)
    }
  }, 60)
}

const stopReveal = () => {
  if (revealTimer) {
    clearInterval(revealTimer)
    revealTimer = null
  }
}

const addEvent = (step: string, status: string, msg: string, output: string = '') => {
  const ev: Ev = { step, status, msg, output, _id: ++eventIdCounter }
  events.value.push(ev)
  if (phaseSteps.find(s => s.key === step)) {
    activePhase.value = step
  }
}

// SSE 流式消费清理接口
const start = async () => {
  // [FIX 2026-07-21] 密钥模式校验私钥, 密码模式校验密码
  if (authMode.value === 'key') {
    if (!privateKey.value) {
      ElMessage.warning('请粘贴 SSH 私钥内容')
      return
    }
  } else {
    if (!password.value) {
      ElMessage.warning('请输入节点服务器密码')
      return
    }
  }
  started.value = true
  running.value = true
  finished.value = false
  events.value = []
  displayedEvents.value = []
  activePhase.value = ''
  userScrolledUp.value = false

  // Start progressive reveal timer
  startReveal()

  const auth = useAuthStore()
  const token = auth.token

  // Use fetch + ReadableStream for SSE (axios doesn't support streaming)
  const url = `/api/v1/admin/nodes/${props.nodeId}/cleanup`
  try {
    const resp = await fetch(url, {
      method: 'DELETE',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': token ? 'Bearer ' + token : '',
      },
      body: JSON.stringify({
        password: authMode.value === 'password' ? password.value : '',
        // 修复 P1-FE4: 旧版字段名 privateKey(camelCase), 后端 Go struct JSON tag 是 private_key(snake_case),
        // 导致 SSH 密钥部署时后端 ShouldBindJSON 解析不出私钥, 走密码分支报错"认证失败"。
        private_key: authMode.value === 'key' ? privateKey.value : '',
        username: username.value,
        port: port.value,
        removeImg: removeImg.value,
      }),
    })
    if (!resp.ok && !resp.body) {
      const txt = await resp.text()
      addEvent('finalize', 'error', `HTTP ${resp.status}: ${txt}`)
      running.value = false
      finished.value = true
      return
    }
    const reader = resp.body!.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let sawFinish = false
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      // SSE events separated by \n\n
      let idx
      while ((idx = buffer.indexOf('\n\n')) >= 0) {
        const raw = buffer.slice(0, idx)
        buffer = buffer.slice(idx + 2)
        if (!raw.startsWith('data: ')) continue // 跳过心跳注释
        const jsonStr = raw.slice(6)
        try {
          const ev = JSON.parse(jsonStr)
          addEvent(ev.step, ev.status, ev.msg || '', ev.output || '')
          if (ev.step === 'finish') {
            sawFinish = true
          }
        } catch { /* ignore parse error */ }
      }
    }
    if (!sawFinish) {
      addEvent('finalize', 'warning', 'SSE 流提前结束(网络中断), 但清理可能已完成')
    }
  } catch (e: any) {
    addEvent('finalize', 'error', '请求失败: ' + (e?.message || String(e)))
  } finally {
    running.value = false
    finished.value = true
    // 不再一次性 dump 剩余事件, 让 reveal timer 按原节奏推完
    // timer 会在 idx >= events.length 且 running=false 时自动停止
    // Notify parent to refresh list
    emit('done')
  }
}

const close = () => {
  visible.value = false
}
</script>

<style scoped>
.cleanup-progress-dialog :deep(.el-dialog__body) { padding: 16px 20px; }
.cp-container { min-height: 200px; min-width: 0; width: 100%; }
.cp-pwd-bar { padding: 8px 0; }
.cp-progress { max-height: 80vh; display: flex; flex-direction: column; }

/* 认证行: flex-wrap, 窄屏自然换行 */
.cp-auth-row {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 10px;
}
.cp-auth-label { font-size: 13px; color: var(--np-text-secondary, #606266); }
.cp-input-user { width: 90px; flex: 0 0 auto; }
.cp-input-port { width: 110px; flex: 0 0 auto; }
.cp-pwd-row {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
}
.cp-input-pwd { width: 260px; flex: 1 1 220px; min-width: 0; }
.cp-pwd-hint { font-size: 12px; color: var(--np-text-muted, #909399); }
.cp-key-col { display: flex; flex-direction: column; gap: 8px; }
.cp-key-row {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
}
.cp-key-textarea {
  font-family: 'JetBrains Mono', Consolas, monospace; font-size: 12px;
}

.cp-steps-bar {
  display: flex; gap: 4px; margin-bottom: 16px;
  background: var(--np-card, #131822); padding: 8px;
  border-radius: 8px;
  border: 1px solid var(--np-border, #1e2a3a);
  min-height: 60px;
}
.cp-step-bar {
  flex: 1; text-align: center; padding: 8px 4px;
  border-radius: 6px; background: transparent;
  transition: all 0.3s; min-width: 0;
}
.cp-step-bar.active {
  background: var(--np-primary-dim, rgba(0, 245, 212, 0.15));
  box-shadow: 0 0 0 1px var(--np-primary, #00f5d4);
}
.cp-step-bar.done {
  background: rgba(0, 245, 212, 0.08);
}
.cp-step-num {
  width: 24px; height: 24px; line-height: 24px;
  border-radius: 50%; background: var(--np-border-strong, #2a3a4f); color: var(--np-text, #e6edf3);
  margin: 0 auto 4px; font-size: 12px; font-weight: 600;
}
.cp-step-bar.active .cp-step-num { background: var(--np-primary, #00f5d4); color: var(--np-bg, #0a0e17); }
.cp-step-bar.done .cp-step-num { background: var(--np-success, #00f5d4); color: var(--np-bg, #0a0e17); }
.cp-step-name {
  font-size: 12px; color: var(--np-text-secondary, #8b98a9);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.cp-step-bar.active .cp-step-name { color: var(--np-primary, #00f5d4); font-weight: 600; }
.cp-step-bar.done .cp-step-name { color: var(--np-success, #00f5d4); }

/* 步骤淡入动画 (已到达才显示) */
.cp-step-enter-active { transition: all 0.4s ease-out; }
.cp-step-enter-from { opacity: 0; transform: translateY(-8px) scale(0.8); }

.cp-events {
  height: 50vh; min-height: 200px; max-height: 480px;
  overflow-y: auto;
  background: var(--np-bg-soft, #0e1320);
  border: 1px solid var(--np-border, #1e2a3a);
  border-radius: 8px; padding: 8px;
  font-family: 'JetBrains Mono', Consolas, monospace; font-size: 12px;
}
.cp-event { padding: 6px 0; border-bottom: 1px dashed var(--np-border, #1e2a3a); }
.cp-event:last-child { border-bottom: none; }
.cp-ev-head { display: flex; align-items: center; gap: 8px; }
.cp-ev-icon { width: 16px; text-align: center; font-weight: bold; color: var(--np-text-secondary, #8b98a9); }
.cp-ev-step { color: var(--np-text-secondary, #8b98a9); flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
.cp-ev-msg { color: var(--np-text, #e6edf3); margin: 4px 0 0 24px; word-break: break-all; }
.cp-ev-output {
  margin: 4px 0 0 24px; padding: 6px 8px;
  background: var(--np-bg, #0a0e17);
  border: 1px solid var(--np-border, #1e2a3a);
  border-radius: 4px; color: var(--np-text, #e6edf3);
  max-height: 120px; overflow: auto; white-space: pre-wrap;
}
.cp-event.done .cp-ev-icon { color: var(--np-success, #00f5d4); }
.cp-event.error .cp-ev-icon { color: var(--np-danger, #ff006e); }
.cp-event.warning .cp-ev-icon { color: var(--np-warning, #ffbe0b); }
.cp-event.running .cp-ev-icon { color: var(--np-primary, #00f5d4); }
.cp-spin { display: inline-block; animation: cp-spin 1s linear infinite; }
@keyframes cp-spin { to { transform: rotate(360deg); } }

.cp-loading {
  margin-top: 12px; text-align: center; color: var(--np-text-muted, #909399);
  display: flex; align-items: center; justify-content: center; gap: 8px;
}
.cp-done { margin-top: 12px; text-align: center; }

/* 渐进展示动画: 每个事件淡入滑入 */
.cp-ev-enter-active {
  transition: all 0.35s ease-out;
}
.cp-ev-enter-from {
  opacity: 0;
  transform: translateY(-10px);
}
.cp-ev-dot {
  color: var(--np-text-muted, #5a6878);
}

/* 滚动提示条 */
.cp-scroll-hint {
  position: sticky; bottom: 0; left: 0; right: 0;
  background: var(--np-primary, #00f5d4);
  color: var(--np-bg, #0a0e17);
  padding: 6px 12px; text-align: center;
  font-size: 12px; font-weight: 600;
  cursor: pointer; border-radius: 4px; margin-top: 4px;
  z-index: 10;
}
.cp-fade-enter-active, .cp-fade-leave-active { transition: opacity 0.2s; }
.cp-fade-enter-from, .cp-fade-leave-to { opacity: 0; }

/* 窄屏: 步骤条改为竖向, 避免挤成一团 */
@media (max-width: 768px) {
  .cp-steps-vertical {
    flex-direction: column;
    align-items: stretch;
    gap: 4px;
    padding: 8px;
  }
  .cp-steps-vertical .cp-step-bar {
    text-align: left;
    padding: 4px 8px;
    display: flex; align-items: center; gap: 8px;
  }
  .cp-steps-vertical .cp-step-num {
    margin: 0;
  }
  .cp-events {
    height: 40vh; min-height: 160px;
  }
  .cp-input-pwd { width: 100%; }
  .cp-auth-row .el-button { width: 100%; }
}
</style>
