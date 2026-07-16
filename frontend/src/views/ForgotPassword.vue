<template>
  <div class="forgot-page">
    <div class="forgot-card">
      <h2 class="title">忘记密码</h2>
      <p class="subtitle">输入注册邮箱，我们将发送重置链接</p>
      <el-form :model="form" :rules="rules" ref="formRef" label-width="0">
        <el-form-item prop="email">
          <el-input v-model="form.email" placeholder="请输入注册邮箱" :prefix-icon="Message" size="large" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" size="large" :loading="loading" style="width: 100%" @click="submit">
            发送重置链接
          </el-button>
        </el-form-item>
      </el-form>
      <div class="back">
        <el-link type="primary" @click="$router.push('/login')">返回登录</el-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { Message } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const formRef = ref()
const loading = ref(false)
const form = reactive({ email: '' })
const rules = {
  email: [
    { required: true, message: '请输入邮箱', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
}

const submit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (!valid) return
    loading.value = true
    try {
      const res: any = await request.post('/api/v1/auth/forgot-password', { email: form.email })
      if (res && (res.code === 0 || res.code === 200)) {
        ElMessage.success('重置链接已发送，请检查邮箱')
        form.email = ''
      } else {
        ElMessage.error((res && res.msg) || '发送失败')
      }
    } catch (e: any) {
      ElMessage.error(e?.message || '请求失败')
    } finally {
      loading.value = false
    }
  })
}
</script>

<style scoped>
.forgot-page { min-height: 100vh; display: flex; align-items: center; justify-content: center; background: var(--np-bg, #0e1422); }
.forgot-card { background: var(--np-card, #1a2030); padding: 40px; border-radius: 16px; width: 100%; max-width: 420px; box-shadow: 0 8px 32px rgba(0,0,0,0.3); }
.title { margin: 0 0 8px; color: var(--np-text, #e7ecf3); font-size: 24px; text-align: center; }
.subtitle { color: var(--np-text-secondary, #8b98a9); text-align: center; margin-bottom: 28px; }
.back { text-align: center; margin-top: 16px; }
</style>
