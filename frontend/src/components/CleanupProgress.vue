<template>
  <el-dialog v-model="visible" title="娓呯悊骞跺垹闄よ妭鐐? :width="dialogWidth" top="5vh" class="cleanup-progress-dialog" :close-on-click-modal="false" :show-close="!running">
    <div class="cp-container">
      <!-- 鏈紑濮嬶細瀵嗙爜杈撳叆 -->
      <div v-if="!started" class="cp-pwd-bar">
        <el-alert type="warning" :closable="false" show-icon style="margin-bottom:12px">
          <template #title>
            闈㈡澘灏嗚嚜鍔?SSH 杩炴帴鑺傜偣鏈嶅姟鍣紝鍋滄 agent 瀹瑰櫒銆佸垹闄ら儴缃茬洰褰曪紙鍚?.env.node/浜岃繘鍒?xray-cache锛夛紝鐒跺悗鎵ц闈㈡澘渚?DB 鍒犻櫎銆傛暣涓祦绋嬪叏鑷姩锛屽け璐ユ楠や細璺宠繃浣嗘渶缁?DB 鍒犻櫎涓€瀹氭墽琛屻€?
          </template>
        </el-alert>
        <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
          <el-input v-model="password" type="password" show-password placeholder="鑺傜偣鏈嶅姟鍣?root 瀵嗙爜" style="width:200px" @keyup.enter="start" autocomplete="new-password" name="cleanup-pwd" />
          <el-input v-model="username" placeholder="鐢ㄦ埛" style="width:90px" />
          <el-input-number v-model="port" :min="1" :max="65535" controls-position="right" style="width:110px" />
          <el-checkbox v-model="removeImg">鍚屾椂鍒犻暅鍍?/el-checkbox>
          <el-button type="danger" :disabled="!password" @click="start">
            <el-icon><Delete /></el-icon> 寮€濮嬫竻鐞嗗苟鍒犻櫎
          </el-button>
        </div>
        <el-alert type="info" :closable="false" style="margin-top:12px">
          <template #title>涓嶅～瀵嗙爜涔熷彲鐐瑰嚮鍏抽棴鎸夐挳锛屽皢浠呮墽琛岄潰鏉夸晶鍒犻櫎锛堣妭鐐规湇鍔″櫒娈嬬暀璧勬簮闇€鎵嬪姩娓呯悊锛夈€?/template>
        </el-alert>
      </div>

      <!-- 杩涜涓?瀹屾垚锛? 姝ヨ繘搴﹀睍绀?-->
      <div v-else class="cp-progress">
        <!-- 5 姝ヨ繘搴︽潯 -->
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

        <!-- 璇︾粏浜嬩欢娴?(娓愯繘寮忓睍绀? 姣忎釜浜嬩欢鐙珛娓叉煋, 涓嶄細涓€娆℃€ц烦鍑? -->
        <div class="cp-events" ref="eventsContainer">
          <TransitionGroup name="cp-ev" tag="div">
            <div v-for="(ev, i) in displayedEvents" :key="i" class="cp-event" :class="ev.status">
              <div class="cp-ev-head">
                <span class="cp-ev-icon">
                  <span v-if="ev.status === 'running'" class="cp-spin">鉄?/span>
                  <span v-else-if="ev.status === 'done'">鉁?/span>
                  <span v-else-if="ev.status === 'error'">鉁?/span>
                  <span v-else-if="ev.status === 'warning'">鈿?/span>
                  <span v-else-if="ev.status === 'log'" class="cp-ev-dot">路</span>
                  <span v-else>路</span>
                </span>
                <span class="cp-ev-step">{{ stepName(ev.step) }}</span>
                <el-tag v-if="ev.status === 'done'" size="small" type="success">瀹屾垚</el-tag>
                <el-tag v-else-if="ev.status === 'error'" size="small" type="danger">澶辫触</el-tag>
                <el-tag v-else-if="ev.status === 'running'" size="small" type="warning">杩涜涓?/el-tag>
                <el-tag v-else-if="ev.status === 'warning'" size="small" type="warning" effect="dark">璀﹀憡</el-tag>
              </div>
              <div v-if="ev.msg" class="cp-ev-msg">{{ ev.msg }}</div>
              <pre v-if="ev.output" class="cp-ev-output">{{ ev.output }}</pre>
            </div>
          </TransitionGroup>
        </div>

        <div v-if="running" class="cp-loading">
          <el-icon class="is-loading"><Loading /></el-icon>
          姝ｅ湪鎵ц娓呯悊...
        </div>
        <div v-else-if="finished" class="cp-done">
          <el-button type="primary" @click="close">瀹屾垚</el-button>
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

// 娓愯繘寮忓睍绀? 鐢?displayedEvents 閫愭潯娓叉煋, 閰嶅悎 CSS transition 瀹炵幇鍔ㄧ敾
const displayedEvents = ref<Array<{step: string; status: string; msg: string; output: string}>>([])
let revealTimer: ReturnType<typeof setInterval> | null = null

const eventsContainer = ref<HTMLElement>()

// 5 姝ラ樁娈靛畾涔?
const phaseSteps = [
  { key: 'connect', name: 'SSH杩炴帴' },
  { key: 'stop', name: '鍋滃鍣? },
  { key: 'dir', name: '鍒犵洰褰? },
  { key: 'image', name: '鍒犻暅鍍? },
  { key: 'finalize', name: 'DB娓呯悊' },
]

const activePhase = ref<string>('')

const stepNames: Record<string, string> = {
  connect: '1. SSH 杩炴帴鑺傜偣鏈嶅姟鍣?,
  stop: '2. 鍋滄骞跺垹闄?agent 瀹瑰櫒',
  dir: '3. 鍒犻櫎閮ㄧ讲鐩綍',
  image: '4. 鍒犻櫎 docker 闀滃儚',
  finalize: '5. 闈㈡澘渚?DB+Redis 娓呯悊',
  finish: '娓呯悊瀹屾垚',
}
const stepName = (s: string) => stepNames[s] || s

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

// 娓愯繘灞曠ず瀹氭椂鍣? 閫愭潯灏?events 鎺ㄥ叆 displayedEvents, 姣忔潯闂撮殧 ~80ms
const startReveal = () => {
  stopReveal()
  let idx = 0
  revealTimer = setInterval(() => {
    if (idx < events.value.length) {
      displayedEvents.value.push(events.value[idx])
      idx++
      // 鑷姩婊氬姩鍒板簳閮?
      nextTick(() => {
        if (eventsContainer.value) {
          eventsContainer.value.scrollTop = eventsContainer.value.scrollHeight
        }
      })
    } else {
      stopReveal()
      // 鍏滃簳: 纭繚 running 缁撴潫鏃舵墍鏈変簨浠堕兘灞曠ず浜?
      if (!running.value) {
        // 濡傛灉 stopped, 绔嬪嵆灞曠ず鍓╀綑
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

// SSE 娴佸紡娑堣垂娓呯悊鎺ュ彛
const start = async () => {
  if (!password.value) {
    ElMessage.warning('璇疯緭鍏ヨ妭鐐规湇鍔″櫒瀵嗙爜')
    return
  }
  started.value = true
  running.value = true
  finished.value = false
  events.value = []
  displayedEvents.value = []
  activePhase.value = ''

  // 鍚姩娓愯繘灞曠ず瀹氭椂鍣?
  startReveal()

  const auth = useAuthStore()
  const token = auth.token

  // 鐢?fetch + ReadableStream 娑堣垂 SSE (涓嶈兘鐢?axios, 鍥?axios 涓嶆敮鎸佹祦寮?
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
      // 绔嬪嵆灞曠ず鎵€鏈変簨浠?
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
      // SSE 浜嬩欢浠?\n\n 鍒嗛殧
      let idx
      while ((idx = buffer.indexOf('\n\n')) >= 0) {
        const raw = buffer.slice(0, idx)
        buffer = buffer.slice(idx + 2)
        if (!raw.startsWith('data: ')) continue // 璺宠繃蹇冭烦娉ㄩ噴
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
      addEvent('finalize', 'warning', 'SSE 娴佹彁鍓嶇粨鏉?鍙兘缃戠粶涓柇)锛屼絾鍚庣娓呯悊鍙兘宸插畬鎴?)
    }
  } catch (e: any) {
    addEvent('finalize', 'error', '璇锋眰澶辫触: ' + (e?.message || String(e)))
  } finally {
    running.value = false
    finished.value = true
    // 绛夋笎杩涘睍绀哄畬鎴愬悗鍐嶅仠
    setTimeout(() => {
      stopReveal()
      // 纭繚鎵€鏈変簨浠堕兘宸插睍绀?
      if (displayedEvents.value.length < events.value.length) {
        displayedEvents.value = [...events.value]
      }
    }, 500)
    // 閫氱煡鐖剁粍浠跺埛鏂板垪琛?
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
  max-height: 360px; overflow-y: auto;
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

/* 娓愯繘灞曠ず鍔ㄧ敾: 姣忎釜浜嬩欢娣″叆婊戝叆 */
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



