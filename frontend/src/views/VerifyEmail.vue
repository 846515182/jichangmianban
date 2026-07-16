<template>
  <div class="verify-email-page">
    <div class="verify-card">
      <el-icon :size="48" :color="statusColor"><component :is="statusIcon" /></el-icon>
      <h2 class="title">{{ title }}</h2>
      <p class="msg">{{ message }}</p>
      <el-button v-if="success" type="primary" @click="$router.push('/login')">前往登录</el-button>
      <el-button v-else @click="$router.push('/')">返回首页</el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { Check, CircleClose, Loading } from '@element-plus/icons-vue'
import request from '@/utils/request'

const route = useRoute()
const loading = ref(true)
const success = ref(false)
const message = ref('正在验证邮箱...')

const title = computed(() => loading.value ? '验证中' : (success.value ? '验证成功' : '验证失败'))
const statusIcon = computed(() => loading.value ? Loading : (success.value ? Check : CircleClose))
const statusColor = computed(() => loading.value ? '#909399' : (success.value ? '#67c23a' : '#f56c6c'))

onMounted(async () => {
  const token = route.query.token as string
  if (!token) {
    success.value = false
    message.value = '缺少验证 token'
    loading.value = false
    return
  }
  try {
    const res: any = await request.get('/api/v1/auth/verify-email', { params: { token } })
    if (res && (res.code === 0 || res.code === 200)) {
      success.value = true
      message.value = res.msg || '邮箱已成功验证，您可以登录了'
    } else {
      success.value = false
      message.value = (res && res.msg) || '验证失败，链接可能已过期'
    }
  } catch (e: any) {
    success.value = false
    message.value = e?.message || '验证请求失败'
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
.verify-email-page { min-height: 100vh; display: flex; align-items: center; justify-content: center; background: var(--np-bg, #0e1422); }
.verify-card { background: var(--np-card, #1a2030); padding: 48px 64px; border-radius: 16px; text-align: center; max-width: 480px; box-shadow: 0 8px 32px rgba(0,0,0,0.3); }
.title { margin: 16px 0 8px; color: var(--np-text, #e7ecf3); font-size: 22px; }
.msg { color: var(--np-text-secondary, #8b98a9); margin-bottom: 24px; }
</style>
