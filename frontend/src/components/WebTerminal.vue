<template>
  <div class="web-terminal">
    <div class="term-toolbar">
      <div class="term-dots">
        <span class="dot red"></span>
        <span class="dot yellow"></span>
        <span class="dot green"></span>
      </div>
      <div class="term-title">
        <el-icon><Monitor /></el-icon>
        <span>{{ title || '终端' }}</span>
      </div>
      <div class="term-actions">
        <el-tag v-if="status === 'connecting'" size="small" type="warning">连接中...</el-tag>
        <el-tag v-else-if="status === 'connected'" size="small" type="success">已连接</el-tag>
        <el-tag v-else-if="status === 'disconnected'" size="small" type="info">未连接</el-tag>
        <el-tag v-else size="small" type="danger">{{ statusText }}</el-tag>
        <el-button size="small" link @click="clearScreen">
          <el-icon><Delete /></el-icon> 清屏
        </el-button>
      </div>
    </div>
    <div ref="termHost" class="term-host"></div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { ElIcon } from 'element-plus'
import { Monitor, Delete } from '@element-plus/icons-vue'
import '@xterm/xterm/css/xterm.css'

const props = defineProps<{
  wsUrl: string
  password: string
  username?: string
  port?: number
  title?: string
  autoConnect?: boolean
}>()

const emit = defineEmits<{ (e: 'status', s: 'connecting' | 'connected' | 'disconnected' | 'error', text?: string): void }>()

const termHost = ref<HTMLElement>()
const status = ref<'connecting' | 'connected' | 'disconnected' | 'error'>('disconnected')
const statusText = ref('')
let term: Terminal | null = null
let fit: FitAddon | null = null
let ws: WebSocket | null = null
let resizeObs: ResizeObserver | null = null

const clearScreen = () => term && term.clear()

const connect = () => {
  if (status.value === 'connecting' || status.value === 'connected') return
  if (!props.wsUrl || !props.password) {
    status.value = 'error'
    statusText.value = '缺少连接参数'
    return
  }
  status.value = 'connecting'
  statusText.value = '连接中'
  emit('status', 'connecting')

  try {
    ws = new WebSocket(props.wsUrl)
  } catch (e: any) {
    status.value = 'error'
    statusText.value = 'URL 错误'
    return
  }

  ws.binaryType = 'arraybuffer'
  ws.onopen = () => {
    // 发送认证信息
    const auth = {
      password: props.password,
      username: props.username || 'root',
      port: props.port || 22,
      cols: term ? term.cols : 100,
      rows: term ? term.rows : 28,
    }
    ws && ws.send(JSON.stringify(auth))
  }
  ws.onmessage = (ev) => {
    if (typeof ev.data === 'string') {
      // JSON 事件
      try {
        const msg = JSON.parse(ev.data)
        if (msg.type === 'ready') {
          status.value = 'connected'
          statusText.value = '已连接'
          emit('status', 'connected', msg.msg)
          if (term && msg.msg) {
            term.writeln('\r\n\x1b[32m' + msg.msg + '\x1b[0m\r\n')
          }
        } else if (msg.type === 'error') {
          status.value = 'error'
          statusText.value = msg.msg || '错误'
          emit('status', 'error', msg.msg)
          if (term && msg.msg) {
            term.writeln('\r\n\x1b[31m[错误] ' + msg.msg + '\x1b[0m\r\n')
          }
        } else if (msg.type === 'closed') {
          status.value = 'disconnected'
          statusText.value = '已断开'
          emit('status', 'disconnected', msg.msg)
          if (term && msg.msg) {
            term.writeln('\r\n\x1b[33m[' + msg.msg + ']\x1b[0m\r\n')
          }
        }
      } catch {
        // 非 JSON，当二进制处理
        if (term) term.write(ev.data)
      }
    } else {
      // 二进制 = 终端输出
      const buf = new Uint8Array(ev.data)
      if (term) term.write(buf)
    }
  }
  ws.onerror = () => {
    status.value = 'error'
    statusText.value = 'WebSocket 错误'
    emit('status', 'error', 'WebSocket 连接失败')
  }
  ws.onclose = () => {
    if (status.value !== 'error') {
      status.value = 'disconnected'
      statusText.value = '已断开'
      emit('status', 'disconnected')
    }
  }
}

const disconnect = () => {
  if (ws) {
    try { ws.close() } catch {}
    ws = null
  }
  status.value = 'disconnected'
}

const doFit = () => {
  if (!fit || !term) return
  try {
    fit.fit()
    if (ws && ws.readyState === WebSocket.OPEN && term) {
      ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
    }
  } catch {}
}

onMounted(async () => {
  await nextTick()
  if (!termHost.value) return
  term = new Terminal({
    cols: 100,
    rows: 28,
    cursorBlink: true,
    fontSize: 13,
    fontFamily: 'SFMono-Regular, Consolas, "Liberation Mono", Menlo, monospace',
    theme: {
      background: '#1e1e1e',
      foreground: '#d4d4d4',
      cursor: '#ffffff',
      selectionBackground: '#264f78',
    },
    allowProposedApi: true,
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(termHost.value)
  doFit()
  term.writeln('\x1b[36m Nexus Panel Web Terminal\x1b[0m')
  term.writeln('\x1b[90m 准备就绪后下方输入命令执行\x1b[0m\r\n')
  term.onData((data) => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(data)
    }
  })
  resizeObs = new ResizeObserver(() => doFit())
  resizeObs.observe(termHost.value)
  if (props.autoConnect !== false) {
    connect()
  }
})

onBeforeUnmount(() => {
  disconnect()
  if (resizeObs) { resizeObs.disconnect(); resizeObs = null }
  if (term) { term.dispose(); term = null }
})

watch(() => props.password, () => {})

defineExpose({ connect, disconnect, clearScreen })
</script>

<style scoped>
.web-terminal {
  border: 1px solid #2d2d2d;
  border-radius: 8px;
  overflow: hidden;
  background: #1e1e1e;
}
.term-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 12px;
  background: #252526;
  border-bottom: 1px solid #3c3c3c;
  gap: 12px;
}
.term-dots { display: flex; gap: 6px; }
.term-dots .dot { width: 12px; height: 12px; border-radius: 50%; display: inline-block; }
.term-dots .dot.red { background: #ff5f56; }
.term-dots .dot.yellow { background: #ffbd2e; }
.term-dots .dot.green { background: #27c93f; }
.term-title {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 6px;
  color: #cccccc;
  font-size: 12px;
}
.term-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.term-host {
  padding: 8px 10px;
  background: #1e1e1e;
  min-height: 320px;
  height: 320px;
}
.term-host :deep(.xterm) {
  padding: 0;
}
.term-host :deep(.xterm-viewport) {
  background-color: #1e1e1e !important;
}
</style>
