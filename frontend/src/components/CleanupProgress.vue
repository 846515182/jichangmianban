<template>
  <el-dialog v-model="visible" title="Cleanup Node" :width="dialogWidth" top="5vh" class="cleanup-progress-dialog" :close-on-click-modal="false" :show-close="!running">
    <div class="cp-container">
      <!-- 未开始：密码输入 -->
      <div v-if="!started" class="cp-pwd-bar">
        <el-alert type="warning" :closable="false" show-icon style="margin-bottom:12px">
          <template #title>
            Panel will SSH to node server, stop agent container, delete deploy dir (.env.node/binary/xray-cache), then delete from Panel DB. Fully automated, failed steps are skipped but DB cleanup always runs.
          </template>
        </el-alert>
        <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
          <el-input v-model="password" type="password" show-password placeholder="Node root password" style="width:200px" @keyup.enter="start" autocomplete="new-password" name="cleanup-pwd" />
          <el-input v-model="username" placeholder="用户" style="width:90px" />
          <el-input-number v-model="port" :min="1" :max="65535" controls-position="right" style="width:110px" />
          <el-checkbox v-model="removeImg">Remove image</el-checkbox>
          <el-button type="danger" :disabled="!password" @click="start">
            <el-icon><Delete /></el-icon> Start Cleanup
          </el-button>
        </div>
        <el-alert type="info" :closable="false" style="margin-top:12px">
          <template #title>Close without password to only delete from panel (node resources need manual cleanup).</template>
        </el-alert>
      </div>

      <!-- Progress: 5-step display -->
      <div v-else class="cp-progress">
        <!-- 5 步进度条 -->
        <div class="cp-steps-bar">
          <div
            v-for="(step, i) in phaseSteps"
            :key="step.key"
            class="cp-step-bar"
            :class="getStepBarClass(step.key)"
          >
            <div class="cp-step-num">{{ i + 1 }}</div>
            <div class="cp-step-name">{{ step.name }}</div>
          </div>
        </div>

        <!-- 详细事件�?(渐进式展�? 每个事件独立渲染, 不会一次性跳�? -->
        <div class="cp-events" ref="eventsContainer">
          <TransitionGroup name="cp-ev" tag="div">
            <div v-for="(ev, i) in displayedEvents" :key="i" class="cp-event" :class="ev.status">
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
                <el-tag v-if="isStatus(ev, 'done')" size="small" type="success">Done</el-tag>
                <el-tag v-else-if="isStatus(ev, 'error')" size="small" type="danger">Failed</el-tag>
                <el-tag v-else-if="isStatus(ev, 'running')" size="small" type="warning">Running</el-tag>
                <el-tag v-else-if="isStatus(ev, 'warning')" size="small" type="warning" effect="dark">Warning</el-tag>
              </div>
              <div v-if="ev.msg" class="cp-ev-msg">{{ ev.msg }}</div>
              <pre v-if="ev.output" class="cp-ev-output">{{ ev.output }}</pre>
            </div>
          </TransitionGroup>
        </div>

        <div v-if="running" class="cp-loading">
          <el-icon class="is-loading"><Loading /></el-icon>
          Cleaning up...
        </div>
        <div v-else-if="finished" class="cp-done">
          <el-button type="primary" @click="close">Done</el-button>
        </div>
      </div>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch, computed, nextTick } from 'vue'
import { ElIcon, ElMessage } from 'element-plus'
import { Delete, Loading } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{
  modelValue: boolean
  nodeId: string
  nodeName?: string
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void; (e: 'done'): void }>()

const visible = ref(props.modelValue)
watch(() => props.modelValue, (v) => { visible.value = v; resetIfClosed(v) })
watch(visible, (v) => emit('update:modelValue', v))

const dialogWidth = computed(() => (window.innerWidth < 768 ? '95%' : '780px'))

const password = ref('')
const username = ref('root')
const port = ref(22)
const removeImg = ref(false)
const started = ref(false)
const running = ref(false)
const finished = ref(false)
const events = ref<Array<{step: string; status: string; msg: string; output: string}>>([])

// Progressive reveal: push displayedEvents one by one with CSS transition animation
const displayedEvents = ref<Array<{step: string; status: string; msg: string; output: string}>>([])
let revealTimer: ReturnType<typeof setInterval> | null = null

const eventsContainer = ref<HTMLElement>()

// 5-step phase definition
const phaseSteps = [
  { key: 'connect', name: 'SSH连接' },
  { key: 'stop', name: '停容器' },
  { key: 'dir', name: '删目录' },
  { key: 'image', name: '删镜像' },
  { key: 'finalize', name: 'DB清理' },
]

const activePhase = ref<string>('')

const stepNames: Record<string, string> = {
  connect: '1. SSH connect to node server',
  stop: '2. Stop and remove agent container',
  dir: '3. Remove deploy directory',
  image: '4. Remove docker image',
  finalize: '5. Panel DB + Redis cleanup',
  finish: 'Cleanup complete',
}
const stepName = (s: string) => stepNames[s] || s

const isStatus = (ev: { step: string; status: string; msg: string; output: string }, s: string) => ev.status === s

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
      password.value = ''
      removeImg.value = false
      stopReveal()
    }, 300)
  }
}

// Progressive reveal timer: push events to displayedEvents one by one, ~80ms interval
const startReveal = () => {
  stopReveal()
  let idx = 0
  revealTimer = setInterval(() => {
    if (idx < events.value.length) {
      displayedEvents.value.push(events.value[idx])
      idx++
      // Auto scroll to bottom
      nextTick(() => {
        if (eventsContainer.value) {
          eventsContainer.value.scrollTop = eventsContainer.value.scrollHeight
        }
      })
    } else {
      stopReveal()
      // Ensure all events are displayed when running stops
      if (!running.value) {
        // If stopped, display remaining immediately
        while (idx < events.value.length) {
          displayedEvents.value.push(events.value[idx])
          idx++
        }
      }
    }
  }, 80)
}

const stopReveal = () => {
  if (revealTimer) {
    clearInterval(revealTimer)
    revealTimer = null
  }
}

const addEvent = (step: string, status: string, msg: string, output: string = '') => {
  events.value.push({ step, status, msg, output })
  if (phaseSteps.find(s => s.key === step)) {
    activePhase.value = step
  }
}

// SSE 流式消费清理接口
const start = async () => {
  if (!password.value) {
    ElMessage.warning('请输入节点服务器密码')
    return
  }
  started.value = true
  running.value = true
  finished.value = false
  events.value = []
  displayedEvents.value = []
  activePhase.value = ''

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
        password: password.value,
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
      stopReveal()
      // Show all events immediately
      displayedEvents.value = [...events.value]
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
      addEvent('finalize', 'warning', 'SSE stream ended early (network interrupted), but cleanup may have completed')
    }
  } catch (e: any) {
    addEvent('finalize', 'error', 'Request failed: ' + (e?.message || String(e)))
  } finally {
    running.value = false
    finished.value = true
    // Wait for progressive reveal to finish before stopping
    setTimeout(() => {
      stopReveal()
      // Ensure all events are displayed
      if (displayedEvents.value.length < events.value.length) {
        displayedEvents.value = [...events.value]
      }
    }, 500)
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
.cp-container { min-height: 200px; }
.cp-pwd-bar { padding: 8px 0; }

.cp-steps-bar {
  display: flex; gap: 4px; margin-bottom: 16px;
  background: var(--np-card, #f5f7fa); padding: 8px;
  border-radius: 8px;
}
.cp-step-bar {
  flex: 1; text-align: center; padding: 8px 4px;
  border-radius: 6px; background: transparent;
  transition: all 0.3s; min-width: 0;
}
.cp-step-bar.active {
  background: var(--np-primary-dim, #ecf5ff);
  box-shadow: 0 0 0 1px var(--np-primary, #409eff);
}
.cp-step-bar.done {
  background: #f0f9eb;
}
.cp-step-num {
  width: 24px; height: 24px; line-height: 24px;
  border-radius: 50%; background: #dcdfe6; color: #fff;
  margin: 0 auto 4px; font-size: 12px; font-weight: 600;
}
.cp-step-bar.active .cp-step-num { background: var(--np-primary, #409eff); }
.cp-step-bar.done .cp-step-num { background: #67c23a; }
.cp-step-name {
  font-size: 12px; color: var(--np-text, #606266);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}

.cp-events {
  height: 360px; overflow-y: auto;
  background: var(--np-bg, #1a1a1a);
  border: 1px solid var(--np-border, #303030);
  border-radius: 8px; padding: 8px;
  font-family: 'JetBrains Mono', Consolas, monospace; font-size: 12px;
}
.cp-event { padding: 6px 0; border-bottom: 1px dashed var(--np-border, #303030); }
.cp-event:last-child { border-bottom: none; }
.cp-ev-head { display: flex; align-items: center; gap: 8px; }
.cp-ev-icon { width: 16px; text-align: center; font-weight: bold; }
.cp-ev-step { color: var(--np-text-muted, #909399); flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cp-ev-msg { color: var(--np-text, #303133); margin: 4px 0 0 24px; word-break: break-all; }
.cp-ev-output {
  margin: 4px 0 0 24px; padding: 6px 8px;
  background: var(--np-bg-soft, #000);
  border-radius: 4px; color: #aab0b8;
  max-height: 120px; overflow: auto; white-space: pre-wrap;
}
.cp-event.done .cp-ev-icon { color: #67c23a; }
.cp-event.error .cp-ev-icon { color: #f56c6c; }
.cp-event.warning .cp-ev-icon { color: #e6a23c; }
.cp-event.running .cp-ev-icon { color: #409eff; }
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
.cp-ev-leave-active {
  transition: all 0.2s ease-in;
}
.cp-ev-enter-from {
  opacity: 0;
  transform: translateY(-10px);
}
.cp-ev-leave-to {
  opacity: 0;
}
.cp-ev-dot {
  color: var(--np-text-muted, #909399);
}
</style>
