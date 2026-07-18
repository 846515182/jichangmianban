<template>
  <el-dialog v-model="visible" title="一键自动部署" width="780px" top="5vh" class="deploy-progress-dialog" :close-on-click-modal="false" :show-close="!running">
    <div class="dp-container">
      <!-- 未开始：认证方式选择 -->
      <div v-if="!started" class="dp-pwd-bar">
        <el-alert type="info" :closable="false" show-icon style="margin-bottom:12px">
          <template #title>面板将自动 SSH 连接节点服务器，推送文件、安装 Docker、启动 node-agent，全程无需手动操作。</template>
        </el-alert>

        <!-- 认证方式切换 -->
        <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-bottom:10px">
          <span style="font-size:13px;color:#606266">认证方式:</span>
          <el-radio-group v-model="authMode" size="small">
            <el-radio-button value="password">密码</el-radio-button>
            <el-radio-button value="key">SSH 密钥</el-radio-button>
          </el-radio-group>
          <el-input v-model="username" placeholder="用户" style="width:90px" />
          <el-input-number v-model="port" :min="1" :max="65535" controls-position="right" style="width:110px" />
          <el-button type="primary" :disabled="!canStart" @click="start">
            <el-icon><VideoPlay /></el-icon> 开始部署
          </el-button>
        </div>

        <!-- 密码模式 -->
        <div v-if="authMode === 'password'" style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
          <el-input v-model="password" type="password" show-password placeholder="节点服务器密码" style="width:260px" @keyup.enter="start" autocomplete="new-password" name="deploy-pwd" />
          <span style="font-size:12px;color:#909399">输入 root 或其他 sudo 用户的密码</span>
        </div>

        <!-- SSH 密钥模式 -->
        <div v-if="authMode === 'key'" style="display:flex;flex-direction:column;gap:8px">
          <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
            <el-upload
              :auto-upload="false"
              :show-file-list="false"
              :on-change="onKeyFileChange"
              accept="*"
              style="display:inline-flex"
            >
              <el-button size="small" type="primary" plain>选择私钥文件</el-button>
            </el-upload>
            <span style="font-size:12px;color:#909399">或直接粘贴私钥内容</span>
            <el-button size="small" link type="primary" @click="showKeyHelp = !showKeyHelp">
              {{ showKeyHelp ? '收起' : '如何获取私钥?' }}
            </el-button>
          </div>
          <el-input
            v-model="privateKey"
            type="textarea"
            :rows="5"
            placeholder="粘贴 SSH 私钥内容 (PEM 格式)&#10;-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
            style="font-family:monospace;font-size:12px"
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

      <!-- 进行中/完成：6 步进度展示 -->
      <div v-else class="dp-progress">
        <!-- 6 步进度条 -->
        <div class="dp-steps-bar">
          <div
            v-for="(step, i) in phaseSteps"
            :key="step.key"
            class="dp-step-bar"
            :class="getStepBarClass(step.key)"
          >
            <div class="dp-step-num">{{ i + 1 }}</div>
            <div class="dp-step-name">{{ step.name }}</div>
          </div>
        </div>

        <!-- 详细事件流 -->
        <div class="dp-events">
          <div v-for="(ev, i) in events" :key="i" class="dp-event" :class="ev.status">
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
        </div>

        <div v-if="running" class="dp-loading">
          <el-icon class="is-loading"><Loading /></el-icon>
          正在执行部署 (失败将自动重试, 最多 3 次)...
        </div>
        <div v-else-if="finished" class="dp-done">
          <el-button type="primary" @click="close">完成</el-button>
        </div>
      </div>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { ElIcon, ElMessage } from 'element-plus'
import { VideoPlay, Loading } from '@element-plus/icons-vue'
import type { UploadFile } from 'element-plus'
import request from '@/utils/request'
import { useAuthStore } from '@/stores/auth'

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
const events = ref<Array<{step: string; status: string; msg: string; output: string; errCode?: string}>>([])

// 6 步阶段定义
const phaseSteps = [
  { key: 'connect_server', name: '连接服务器' },
  { key: 'env_check', name: '环境检测' },
  { key: 'prepare', name: '准备部署' },
  { key: 'build', name: '编译程序' },
  { key: 'start', name: '启动服务' },
  { key: 'verify', name: '验证完成' },
]

// 当前活跃的阶段 (用于进度条高亮)
const activePhase = ref<string>('')

// 步骤名称映射 (兼容旧名 + 新名)
const stepNames: Record<string, string> = {
  // 新 6 步
  connect_server: '1. 连接节点服务器',
  env_check: '2. 环境检测',
  prepare: '3. 准备部署',
  build: '4. 编译程序',
  start: '5. 启动服务',
  verify: '6. 验证完成',
  // 兼容旧名
  connect: '1. 连接节点服务器',
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
      activePhase.value = ''
      // 安全: 关闭弹窗时清除密码/密钥, 避免缓存残留导致下次部署用错凭证
      password.value = ''
      privateKey.value = ''
      showKeyHelp.value = false
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
  events.value.push({ step: 'verify', status: 'running', msg: '连接断开，但部署仍在后台继续执行，正在检查节点实际状态...', output: '' })
  for (let i = 0; i < 36; i++) {
    await new Promise(r => setTimeout(r, 5000))
    const online = await checkNodeStatus()
    if (online) {
      events.value[events.value.length - 1].status = 'done'
      events.value[events.value.length - 1].msg = '节点已在线，部署成功！'
      events.value.push({ step: 'finish', status: 'done', msg: '一键部署完成！请返回节点列表查看在线状态', output: '' })
      running.value = false
      finished.value = true
      emit('done')
      ElMessage.success('部署完成')
      return true
    }
  }
  // 180秒后仍未在线
  events.value[events.value.length - 1].status = 'warning'
  events.value[events.value.length - 1].msg = '连接断开且 180 秒内节点未上线。部署可能仍在后台执行，请稍后刷新节点列表查看在线状态，或查看面板日志确认部署进度。'
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
  activePhase.value = 'connect_server'

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
    events.value.push({
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

    if (!resp.ok && resp.status !== 200) {
      const txt = await resp.text()
      if (resp.status === 401) {
        events.value.push({ step: 'connect_server', status: 'error', msg: '登录状态已过期，请关闭窗口后刷新页面重新登录', output: '' })
      } else if (resp.status === 403) {
        events.value.push({ step: 'connect_server', status: 'error', msg: '无权限执行部署操作（需要超级管理员权限）', output: '' })
      } else {
        events.value.push({ step: 'connect_server', status: 'error', msg: '请求失败: ' + resp.status + ' ' + txt, output: '' })
      }
      running.value = false
      finished.value = true
      return
    }

    const reader = resp.body?.getReader()
    if (!reader) {
      events.value.push({ step: 'connect_server', status: 'error', msg: '无法读取响应流', output: '' })
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
      const blocks = buffer.split('\n\n')
      buffer = blocks.pop() || ''
      for (const block of blocks) {
        const line = block.trim()
        if (line.startsWith('data: ')) {
          try {
            const ev = JSON.parse(line.slice(6))
            events.value.push(ev)
            // 更新当前活跃 phase (用于进度条)
            if (ev.step && phaseSteps.some(s => s.key === ev.step)) {
              activePhase.value = ev.step
            }
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
.dp-container { min-height: 120px; }
.dp-pwd-bar { padding: 8px 0; }
.dp-progress { max-height: 60vh; overflow-y: auto; }

/* 6 步进度条 */
.dp-steps-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 8px 16px;
  margin-bottom: 12px;
  background: #fafbfc;
  border-radius: 6px;
  border: 1px solid #ebeef5;
}
.dp-step-bar {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  position: relative;
  text-align: center;
}
.dp-step-bar:not(:last-child)::after {
  content: '';
  position: absolute;
  top: 14px;
  left: 60%;
  right: -40%;
  height: 2px;
  background: #ebeef5;
  z-index: 0;
}
.dp-step-bar.done:not(:last-child)::after { background: #67c23a; }
.dp-step-num {
  width: 28px;
  height: 28px;
  line-height: 28px;
  text-align: center;
  border-radius: 50%;
  background: #fff;
  border: 2px solid #dcdfe6;
  color: #909399;
  font-size: 13px;
  font-weight: 600;
  z-index: 1;
  position: relative;
}
.dp-step-bar.active .dp-step-num {
  background: #409eff;
  border-color: #409eff;
  color: #fff;
  box-shadow: 0 0 0 4px rgba(64, 158, 255, 0.2);
}
.dp-step-bar.done .dp-step-num {
  background: #67c23a;
  border-color: #67c23a;
  color: #fff;
}
.dp-step-name {
  margin-top: 6px;
  font-size: 12px;
  color: #606266;
  white-space: nowrap;
}
.dp-step-bar.active .dp-step-name { color: #409eff; font-weight: 600; }
.dp-step-bar.done .dp-step-name { color: #67c23a; }

/* 事件流 */
.dp-events { max-height: 360px; overflow-y: auto; padding-right: 4px; }
.dp-event {
  padding: 10px 12px;
  margin-bottom: 8px;
  border-radius: 6px;
  border-left: 3px solid #dcdfe6;
  background: #fafbfc;
}
.dp-event.done { border-left-color: #67c23a; background: #f0f9eb; }
.dp-event.error { border-left-color: #f56c6c; background: #fef0f0; }
.dp-event.running { border-left-color: #e6a23c; background: #fdf6ec; }
.dp-event.warning { border-left-color: #e6a23c; background: #fefce8; }
.dp-event.log { border-left-color: #909399; background: #f4f4f5; padding: 4px 12px; }
.dp-ev-head { display: flex; align-items: center; gap: 8px; }
.dp-ev-icon { font-size: 14px; width: 16px; text-align: center; }
.dp-spin { display: inline-block; animation: dp-rot 1s linear infinite; }
@keyframes dp-rot { to { transform: rotate(360deg); } }
.dp-ev-step { font-size: 13px; font-weight: 600; color: #303133; flex: 1; }
.dp-ev-msg { font-size: 12px; color: #606266; margin: 4px 0 0 24px; }
.dp-ev-output {
  margin: 6px 0 0 24px;
  background: #1e1e1e; color: #d4d4d4;
  padding: 8px 10px; border-radius: 4px;
  font-size: 11px; font-family: 'SFMono-Regular', Consolas, monospace;
  white-space: pre-wrap; word-break: break-all; max-height: 200px; overflow-y: auto;
}
.dp-loading { padding: 12px; color: #e6a23c; font-size: 13px; display: flex; align-items: center; gap: 6px; }
.dp-done { padding: 12px; text-align: center; }
</style>

<style>
.deploy-progress-dialog .el-dialog__body { padding: 16px 20px; }
</style>
