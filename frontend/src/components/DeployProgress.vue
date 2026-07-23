<template>
  <el-dialog v-model="visible" title="一键自动部署" :width="dialogWidth" top="5vh" class="deploy-progress-dialog" :close-on-click-modal="false" :show-close="!running">
    <div class="dp-container">
      <!-- 未开始：认证方式选择 -->
      <div v-if="!started" class="dp-pwd-bar">
        <el-alert type="info" :closable="false" show-icon style="margin-bottom:12px">
          <template #title>面板将自动 SSH 连接节点服务器，推送文件、安装 Docker、启动 node-agent，全程无需手动操作。</template>
        </el-alert>

        <!-- 认证方式切换 -->
        <div class="dp-auth-row">
          <span class="dp-auth-label">认证方式:</span>
          <el-radio-group v-model="authMode" size="small">
            <el-radio-button value="password">密码</el-radio-button>
            <el-radio-button value="key">SSH 密钥</el-radio-button>
          </el-radio-group>
          <el-input v-model="username" placeholder="用户" class="dp-input-user" />
          <el-input-number v-model="port" :min="1" :max="65535" controls-position="right" class="dp-input-port" />
          <el-button type="primary" :disabled="!canStart" @click="start">
            <el-icon><VideoPlay /></el-icon> 开始部署
          </el-button>
        </div>

        <!-- 密码模式 -->
        <div v-if="authMode === 'password'" class="dp-pwd-row">
          <el-input v-model="password" type="password" show-password placeholder="节点服务器密码" class="dp-input-pwd" @keyup.enter="start" autocomplete="new-password" name="deploy-pwd" />
          <span class="dp-pwd-hint">输入 root 或其他 sudo 用户的密码</span>
        </div>

        <!-- SSH 密钥模式 -->
        <div v-if="authMode === 'key'" class="dp-key-col">
          <div class="dp-key-row">
            <el-upload
              :auto-upload="false"
              :show-file-list="false"
              :on-change="onKeyFileChange"
              accept="*"
              style="display:inline-flex"
            >
              <el-button size="small" type="primary" plain>选择私钥文件</el-button>
            </el-upload>
            <span class="dp-pwd-hint">或直接粘贴私钥内容</span>
            <el-button size="small" link type="primary" @click="showKeyHelp = !showKeyHelp">
              {{ showKeyHelp ? '收起' : '如何获取私钥?' }}
            </el-button>
          </div>
          <el-input
            v-model="privateKey"
            type="textarea"
            :rows="5"
            placeholder="粘贴 SSH 私钥内容 (PEM 格式)&#10;-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
            class="dp-key-textarea"
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
      </div>

      <!-- 进行中/完成：8 步进度展示 -->
      <div v-else class="dp-progress">
        <!-- 8 步进度条 (已到达的步骤才显示, 配合 Transition 淡入) -->
        <div class="dp-steps-bar" :class="{ 'dp-steps-vertical': isMobile }">
          <TransitionGroup name="dp-step">
            <div
              v-for="step in visibleSteps"
              :key="step.key"
              class="dp-step-bar"
              :class="getStepBarClass(step.key)"
            >
              <div class="dp-step-num">{{ step.index + 1 }}</div>
              <div class="dp-step-name">{{ step.name }}</div>
            </div>
          </TransitionGroup>
        </div>

        <!-- 详细事件流 (渐进式渲染, 智能滚动) -->
        <div
          ref="eventsRef"
          class="dp-events"
          @scroll="onEventsScroll"
        >
          <TransitionGroup name="dp-ev" tag="div">
            <div v-for="(ev, i) in displayedEvents" :key="(ev as any)._id || i" class="dp-event" :class="ev.status">
              <div class="dp-ev-head">
                <span class="dp-ev-icon">
                  <span v-if="ev.status === 'running'" class="dp-spin">⟳</span>
                  <span v-else-if="ev.status === 'done'">✓</span>
                  <span v-else-if="ev.status === 'error'">✗</span>
                  <span v-else-if="ev.status === 'warning'">⚠</span>
                  <span v-else-if="ev.status === 'log'">›</span>
                  <span v-else>·</span>
                </span>
                <span class="dp-ev-step">{{ stepName(ev.step) }}</span>
                <el-tag v-if="ev.status === 'done'" size="small" type="success">完成</el-tag>
                <el-tag v-else-if="ev.status === 'error'" size="small" type="danger">失败</el-tag>
                <el-tag v-else-if="ev.status === 'running'" size="small" type="warning">进行中</el-tag>
                <el-tag v-else-if="ev.status === 'warning'" size="small" type="warning" effect="dark">警告</el-tag>
                <el-tag v-else-if="ev.status === 'log'" size="small" type="info" effect="plain">日志</el-tag>
              </div>
              <div v-if="ev.msg" class="dp-ev-msg">{{ ev.msg }}</div>
              <pre v-if="ev.output" class="dp-ev-output">{{ ev.output }}</pre>
            </div>
          </TransitionGroup>
        </div>

        <!-- 滚动到底部提示 (用户向上滚查看历史时显示) -->
        <Transition name="dp-fade">
          <div v-if="userScrolledUp && running" class="dp-scroll-hint" @click="scrollToBottom(true)">
            ↓ 新日志到达, 点击回到最新
          </div>
        </Transition>

        <div v-if="running" class="dp-loading">
          <el-icon class="is-loading"><Loading /></el-icon>
          正在执行部署 (各阶段独立处理重试)...
        </div>
        <div v-else-if="finished" class="dp-done">
          <el-button type="primary" @click="close">完成</el-button>
        </div>
      </div>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch, computed, nextTick, onMounted, onUnmounted } from 'vue'
import { ElIcon, ElMessage } from 'element-plus'
import { VideoPlay, Loading } from '@element-plus/icons-vue'
import type { UploadFile } from 'element-plus'
import request from '@/utils/request'
import { useAuthStore } from '@/stores/auth'

interface Ev { step: string; status: string; msg: string; output: string; errCode?: string; _id?: number }

const props = defineProps<{
  modelValue: boolean
  nodeId: string
  prePassword?: string
  preUsername?: string
  prePort?: number
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void; (e: 'done'): void }>()

const visible = ref(props.modelValue)
watch(() => props.modelValue, (v) => { visible.value = v; resetIfClosed(v) })
watch(visible, (v) => emit('update:modelValue', v))

// 响应式断点: 监听 resize, 横竖屏切换时更新
const isMobile = ref(typeof window !== 'undefined' && window.innerWidth < 768)
const onResize = () => { isMobile.value = window.innerWidth < 768 }
onMounted(() => window.addEventListener('resize', onResize))
onUnmounted(() => window.removeEventListener('resize', onResize))

// 对话框宽度: PC 固定 720px, 手机 95vw
const dialogWidth = computed(() => (isMobile.value ? '95%' : '720px'))

// 认证模式: password | key
const authMode = ref<'password' | 'key'>('password')
const password = ref(props.prePassword || '')
const privateKey = ref('')
const username = ref(props.preUsername || 'root')
const port = ref(props.prePort || 22)
const started = ref(false)
const showKeyHelp = ref(false)

// 是否可以开始部署
const canStart = computed(() => {
  if (authMode.value === 'key') return !!privateKey.value
  return !!password.value
})

// 文件选择: 读取私钥文件内容
const onKeyFileChange = (file: UploadFile) => {
  const reader = new FileReader()
  reader.onload = (e) => {
    privateKey.value = (e.target?.result as string) || ''
  }
  reader.onerror = () => {
    ElMessage.error('读取私钥文件失败')
  }
  if (file.raw) {
    reader.readAsText(file.raw)
  }
}
const running = ref(false)
const finished = ref(false)
const events = ref<Ev[]>([])
const displayedEvents = ref<Ev[]>([])
const eventsRef = ref<HTMLElement | null>(null)

// 智能滚动: 用户向上滚查看历史时, 不强制拉回底部
const userScrolledUp = ref(false)
let revealTimer: ReturnType<typeof setInterval> | null = null
let eventIdCounter = 0

const onEventsScroll = () => {
  if (!eventsRef.value) return
  const { scrollTop, scrollHeight, clientHeight } = eventsRef.value
  // 距离底部 > 50px 视为"用户在查看历史"
  userScrolledUp.value = scrollHeight - scrollTop - clientHeight > 50
}

const scrollToBottom = async (force = false) => {
  if (!force && userScrolledUp.value) return  // 不打断用户查看历史
  await nextTick()
  if (eventsRef.value) {
    eventsRef.value.scrollTop = eventsRef.value.scrollHeight
    userScrolledUp.value = false
  }
}

// 8 步阶段定义 (每步仅做一件事)
const phaseSteps = [
  { key: 'connect_server', name: '连接服务器' },
  { key: 'env_check', name: '环境检测' },
  { key: 'install_docker', name: '安装 Docker' },
  { key: 'prepare_files', name: '准备文件' },
  { key: 'build', name: '编译程序' },
  { key: 'grpc_precheck', name: 'gRPC 预检' },
  { key: 'start', name: '启动服务' },
  { key: 'verify', name: '验证完成' },
]

// 当前活跃的阶段 (用于进度条高亮 + "已到达才显示"判断)
const activePhase = ref<string>('')

// 只渲染已到达的步骤 (currentIdx >= thisIdx), 配合 TransitionGroup 淡入
const visibleSteps = computed(() => {
  const currentIdx = phaseSteps.findIndex(s => s.key === activePhase.value)
  if (currentIdx < 0) return []
  return phaseSteps.slice(0, currentIdx + 1).map((s, i) => ({ ...s, index: i }))
})

// 步骤名称映射 (兼容旧名 + 新名)
const stepNames: Record<string, string> = {
  connect_server: '1. 连接节点服务器',
  env_check: '2. 环境检测',
  install_docker: '3. 安装 Docker',
  prepare_files: '4. 准备部署文件',
  build: '5. 编译传输',
  grpc_precheck: '6. gRPC 预检',
  start: '7. 启动服务',
  verify: '8. 验证完成',
  // 兼容旧名
  connect: '1. 连接节点服务器',
  prepare: '3. 准备部署',
  preflight: '2. 环境检测',
  cleanup: '3. 清理旧容器',
  mkdir: '3. 创建远程目录',
  upload: '3. 推送文件',
  docker: '3. 安装 Docker',
  env: '3. 创建配置文件',
  transfer: '4. 传输二进制',
  healthcheck: '6. 启动检测',
  finish: '部署完成',
}
const stepName = (s: string) => stepNames[s] || s

// step → phase 映射 (旧 step 名 → 新 phase 名, 用于推进 activePhase)
const stepToPhase: Record<string, string> = {
  connect: 'connect_server',
  preflight: 'env_check',
  prepare: 'prepare_files',
  cleanup: 'prepare_files',
  mkdir: 'prepare_files',
  upload: 'prepare_files',
  env: 'prepare_files',
  docker: 'install_docker',
  transfer: 'build',
  healthcheck: 'verify',
}

// 根据 phase 顺序确定步骤条状态
const getStepBarClass = (key: string) => {
  const currentIdx = phaseSteps.findIndex(s => s.key === activePhase.value)
  const thisIdx = phaseSteps.findIndex(s => s.key === key)
  if (thisIdx < 0) return ''
  // 已完成
  if (currentIdx > thisIdx) return 'done'
  // 进行中
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
      // 安全: 关闭弹窗时清除密码/密钥, 避免缓存残留导致下次部署用错凭证
      password.value = ''
      privateKey.value = ''
      showKeyHelp.value = false
      stopReveal()
    }, 300)
  }
}

// 弹窗打开时如果预填了密码，自动开始
watch(visible, (v) => {
  if (v && props.prePassword && !started.value) {
    password.value = props.prePassword
    start()
  }
})

// 渐进式渲染: 每 60ms push 一条事件到 displayedEvents, 避免"刷"地一下全蹦出来
const startReveal = () => {
  stopReveal()
  let idx = 0
  revealTimer = setInterval(() => {
    if (idx < events.value.length) {
      displayedEvents.value.push(events.value[idx])
      idx++
      scrollToBottom()
    } else {
      // 当前已无积压, 检查是否已结束
      if (!running.value) {
        // 结束后保持原节奏推完剩余 (不再一次性 dump, 避免最后一批"刷"地蹦出)
        if (idx >= events.value.length) {
          stopReveal()
        }
      }
    }
  }, 60)
}

const stopReveal = () => {
  if (revealTimer) {
    clearInterval(revealTimer)
    revealTimer = null
  }
}

const addEvent = (ev: Ev) => {
  ev._id = ++eventIdCounter
  events.value.push(ev)
  // 推进 activePhase (兼容旧 step 名)
  if (ev.step) {
    let phase = ''
    if (phaseSteps.some(s => s.key === ev.step)) {
      phase = ev.step
    } else if (stepToPhase[ev.step]) {
      phase = stepToPhase[ev.step]
    }
    if (phase) activePhase.value = phase
  }
}

// 检查节点实际在线状态（SSE断开时调用）
const checkNodeStatus = async (): Promise<boolean> => {
  try {
    const res: any = await request.get('/api/v1/admin/nodes')
    const list = res?.data?.list || res?.data || []
    const node = list.find((n: any) => n.id === props.nodeId)
    if (node && node.online) {
      return true
    }
  } catch { /* */ }
  return false
}

// SSE 断开后轮询节点状态，最多等待 180 秒(后端部署不因断开终止，仍会完成)
const waitForNodeOnline = async () => {
  addEvent({ step: 'verify', status: 'running', msg: '连接断开，但部署仍在后台继续执行，正在检查节点实际状态...', output: '' })
  for (let i = 0; i < 36; i++) {
    await new Promise(r => setTimeout(r, 5000))
    const online = await checkNodeStatus()
    if (online) {
      const last = events.value[events.value.length - 1]
      if (last) {
        last.status = 'done'
        last.msg = '节点已在线，部署成功！'
      }
      addEvent({ step: 'finish', status: 'done', msg: '一键部署完成！请返回节点列表查看在线状态', output: '' })
      running.value = false
      finished.value = true
      emit('done')
      ElMessage.success('部署完成')
      return true
    }
  }
  // 180秒后仍未在线
  const last = events.value[events.value.length - 1]
  if (last) {
    last.status = 'warning'
    last.msg = '连接断开且 180 秒内节点未上线。部署可能仍在后台执行，请稍后刷新节点列表查看在线状态，或查看面板日志确认部署进度。'
  }
  running.value = false
  finished.value = true
  return false
}

const start = async () => {
  if (!canStart.value || !props.nodeId) return
  started.value = true
  running.value = true
  finished.value = false
  events.value = []
  displayedEvents.value = []
  activePhase.value = 'connect_server'
  userScrolledUp.value = false
  startReveal()

  const auth = useAuthStore()

  // 部署前主动刷新 token，确保凭证在有效期内（避免被动依赖 axios 401 拦截器）
  let bearerToken = auth.token
  try {
    if (auth.refreshToken) {
      bearerToken = await auth.refresh()
    }
  } catch {
    // refresh 失败说明凭证已完全失效，需要用户重新登录
    running.value = false
    finished.value = true
    addEvent({
      step: 'connect_server',
      status: 'error',
      msg: '登录状态已过期，请关闭窗口后刷新页面重新登录',
      output: ''
    })
    return
  }

  // 二次确认：用新 token 校验节点是否存在
  try {
    await request.get('/api/v1/admin/nodes/' + props.nodeId)
  } catch {
    // 非预期错误（如节点不存在、服务器错误），继续尝试部署，让 SSE 返回具体错误
  }

  const url = `/api/v1/admin/nodes/${props.nodeId}/auto-deploy`

  try {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + bearerToken },
      body: JSON.stringify({
        password: authMode.value === 'password' ? password.value : '',
        private_key: authMode.value === 'key' ? privateKey.value : '',
        username: username.value,
        port: port.value,
      }),
    })

    if (!resp.ok) {
      const txt = await resp.text()
      if (resp.status === 401) {
        addEvent({ step: 'connect_server', status: 'error', msg: '登录状态已过期，请关闭窗口后刷新页面重新登录', output: '' })
      } else if (resp.status === 403) {
        addEvent({ step: 'connect_server', status: 'error', msg: '无权限执行部署操作（需要超级管理员权限）', output: '' })
      } else {
        addEvent({ step: 'connect_server', status: 'error', msg: '请求失败: ' + resp.status + ' ' + txt, output: '' })
      }
      running.value = false
      finished.value = true
      return
    }

    const reader = resp.body?.getReader()
    if (!reader) {
      addEvent({ step: 'connect_server', status: 'error', msg: '无法读取响应流', output: '' })
      running.value = false
      finished.value = true
      return
    }

    const decoder = new TextDecoder()
    let buffer = ''
    let gotFinishOrError = false
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      // SSE 事件以 \n\n 分隔, 用 indexOf 循环 slice (比 split 更标准)
      let sepIdx
      while ((sepIdx = buffer.indexOf('\n\n')) >= 0) {
        const raw = buffer.slice(0, sepIdx)
        buffer = buffer.slice(sepIdx + 2)
        const line = raw.trim()
        if (!line.startsWith('data: ')) continue
        try {
          const ev = JSON.parse(line.slice(6))
          addEvent(ev)
          if (ev.step === 'finish' || ev.status === 'error') {
            gotFinishOrError = true
            running.value = false
            finished.value = true
            if (ev.status === 'done' || ev.step === 'finish') {
              emit('done')
              ElMessage.success('部署完成')
            } else {
              // 错误提示 (含错误码)
              const errCode = ev.errCode ? ` [${ev.errCode}]` : ''
              ElMessage.error(`部署失败${errCode}: ${ev.msg || '请查看日志'}`)
            }
          }
        } catch { /* ignore */ }
      }
    }
    // 正常结束（reader done），如果没有收到 finish/error，检查节点状态
    if (!gotFinishOrError) {
      await waitForNodeOnline()
    }
    running.value = false
    finished.value = true
  } catch (e: any) {
    // 网络错误时不直接显示失败，而是检查节点实际状态
    // 部署可能已完成后端继续执行，只是SSE连接断了
    await waitForNodeOnline()
  }
}

const close = () => {
  visible.value = false
}
</script>

<style scoped>
.dp-container { min-height: 120px; min-width: 0; width: 100%; }
.dp-pwd-bar { padding: 8px 0; }
.dp-progress { max-height: 80vh; display: flex; flex-direction: column; }

/* 认证行: 使用 flex-wrap, 窄屏自然换行 */
.dp-auth-row {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-bottom: 10px;
}
.dp-auth-label { font-size: 13px; color: var(--np-text-secondary, #606266); }
.dp-input-user { width: 90px; flex: 0 0 auto; }
.dp-input-port { width: 110px; flex: 0 0 auto; }
.dp-pwd-row {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
}
.dp-input-pwd { width: 260px; flex: 1 1 220px; min-width: 0; }
.dp-pwd-hint { font-size: 12px; color: var(--np-text-muted, #909399); }
.dp-key-col { display: flex; flex-direction: column; gap: 8px; }
.dp-key-row {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
}
.dp-key-textarea {
  font-family: 'JetBrains Mono', Consolas, monospace; font-size: 12px;
}

/* 8 步进度条 */
.dp-steps-bar {
  display: flex; align-items: flex-start; justify-content: space-between;
  padding: 12px 8px 16px; margin-bottom: 12px;
  background: var(--np-card, #131822);
  border-radius: 8px;
  border: 1px solid var(--np-border, #1e2a3a);
  min-height: 76px;
}
.dp-step-bar {
  flex: 1; display: flex; flex-direction: column; align-items: center;
  position: relative; text-align: center; min-width: 0; padding: 0 2px;
}
.dp-step-bar:not(:last-child)::after {
  content: '';
  position: absolute;
  top: 14px;
  left: 60%;
  right: -40%;
  height: 2px;
  background: var(--np-border, #1e2a3a);
  z-index: 0;
}
.dp-step-bar.done:not(:last-child)::after { background: var(--np-success, #00f5d4); }
.dp-step-num {
  width: 28px; height: 28px; line-height: 28px;
  text-align: center; border-radius: 50%;
  background: var(--np-bg-soft, #0e1320);
  border: 2px solid var(--np-border-strong, #2a3a4f);
  color: var(--np-text-muted, #5a6878);
  font-size: 13px; font-weight: 600; z-index: 1; position: relative;
}
.dp-step-bar.active .dp-step-num {
  background: var(--np-primary, #00f5d4);
  border-color: var(--np-primary, #00f5d4);
  color: var(--np-bg, #0a0e17);
  box-shadow: 0 0 0 4px var(--np-primary-dim, rgba(0, 245, 212, 0.15));
}
.dp-step-bar.done .dp-step-num {
  background: var(--np-success, #00f5d4);
  border-color: var(--np-success, #00f5d4);
  color: var(--np-bg, #0a0e17);
}
.dp-step-name {
  margin-top: 6px; font-size: 12px;
  color: var(--np-text-secondary, #8b98a9);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  max-width: 100%;
}
.dp-step-bar.active .dp-step-name { color: var(--np-primary, #00f5d4); font-weight: 600; }
.dp-step-bar.done .dp-step-name { color: var(--np-success, #00f5d4); }

/* 步骤淡入动画 (已到达才显示) */
.dp-step-enter-active { transition: all 0.4s ease-out; }
.dp-step-enter-from { opacity: 0; transform: translateY(-8px) scale(0.8); }

/* 事件流: 使用 vh 自适应高度, 避免双层滚动 */
.dp-events {
  height: 50vh; min-height: 200px; max-height: 480px;
  overflow-y: auto; padding-right: 4px;
  background: var(--np-bg-soft, #0e1320);
  border: 1px solid var(--np-border, #1e2a3a);
  border-radius: 8px; padding: 8px;
}
.dp-event {
  padding: 10px 12px; margin-bottom: 8px; border-radius: 6px;
  border-left: 3px solid var(--np-border-strong, #2a3a4f);
  background: var(--np-card, #131822);
  transition: all 0.3s;
}
.dp-event.done { border-left-color: var(--np-success, #00f5d4); background: rgba(0, 245, 212, 0.05); }
.dp-event.error { border-left-color: var(--np-danger, #ff006e); background: rgba(255, 0, 110, 0.05); }
.dp-event.running { border-left-color: var(--np-warning, #ffbe0b); background: rgba(255, 190, 11, 0.05); }
.dp-event.warning { border-left-color: var(--np-warning, #ffbe0b); background: rgba(255, 190, 11, 0.08); }
.dp-event.log { border-left-color: var(--np-text-muted, #5a6878); background: var(--np-bg-soft, #0e1320); padding: 4px 12px; }
.dp-ev-head { display: flex; align-items: center; gap: 8px; }
.dp-ev-icon { font-size: 14px; width: 16px; text-align: center; color: var(--np-text-secondary, #8b98a9); }
.dp-event.done .dp-ev-icon { color: var(--np-success, #00f5d4); }
.dp-event.error .dp-ev-icon { color: var(--np-danger, #ff006e); }
.dp-event.running .dp-ev-icon { color: var(--np-warning, #ffbe0b); }
.dp-event.warning .dp-ev-icon { color: var(--np-warning, #ffbe0b); }
.dp-spin { display: inline-block; animation: dp-rot 1s linear infinite; }
@keyframes dp-rot { to { transform: rotate(360deg); } }
.dp-ev-step { font-size: 13px; font-weight: 600; color: var(--np-text, #e6edf3); flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dp-ev-msg { font-size: 12px; color: var(--np-text-secondary, #8b98a9); margin: 4px 0 0 24px; word-break: break-all; }
.dp-ev-output {
  margin: 6px 0 0 24px;
  background: var(--np-bg, #0a0e17);
  color: var(--np-text, #e6edf3);
  padding: 8px 10px; border-radius: 4px;
  border: 1px solid var(--np-border, #1e2a3a);
  font-size: 11px; font-family: 'JetBrains Mono', Consolas, monospace;
  white-space: pre-wrap; word-break: break-all; max-height: 180px; overflow-y: auto;
}
.dp-loading {
  padding: 12px; color: var(--np-warning, #ffbe0b);
  font-size: 13px; display: flex; align-items: center; gap: 6px;
}
.dp-done { padding: 12px; text-align: center; }

/* 事件淡入滑入动画 (渐进式渲染) */
.dp-ev-enter-active { transition: all 0.3s ease-out; }
.dp-ev-enter-from { opacity: 0; transform: translateY(-8px); }

/* 滚动提示条 */
.dp-scroll-hint {
  position: sticky; bottom: 0; left: 0; right: 0;
  background: var(--np-primary, #00f5d4);
  color: var(--np-bg, #0a0e17);
  padding: 6px 12px; text-align: center;
  font-size: 12px; font-weight: 600;
  cursor: pointer; border-radius: 4px; margin-top: 4px;
  z-index: 10;
}
.dp-fade-enter-active, .dp-fade-leave-active { transition: opacity 0.2s; }
.dp-fade-enter-from, .dp-fade-leave-to { opacity: 0; }

/* 窄屏: 步骤条改为竖向, 避免挤成一团 */
@media (max-width: 768px) {
  .dp-steps-vertical {
    flex-direction: column;
    align-items: stretch;
    gap: 4px;
    padding: 8px;
  }
  .dp-steps-vertical .dp-step-bar {
    flex-direction: row;
    align-items: center;
    gap: 8px;
    text-align: left;
    padding: 4px 8px;
  }
  .dp-steps-vertical .dp-step-bar:not(:last-child)::after {
    top: auto; bottom: -4px; left: 22px; right: auto;
    width: 2px; height: 4px;
  }
  .dp-steps-vertical .dp-step-name {
    margin-top: 0; white-space: nowrap;
  }
  .dp-events {
    height: 40vh; min-height: 160px;
  }
  .dp-input-pwd { width: 100%; }
  .dp-auth-row .el-button { width: 100%; }
}
</style>

<style>
.deploy-progress-dialog .el-dialog__body { padding: 16px 20px; }
</style>
