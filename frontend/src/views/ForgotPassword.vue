<template>
  <div class="forgot-page">
    <div class="forgot-card">
      <h2 class="title">忘记密码</h2>
      <p class="subtitle">输入注册邮箱，我们将发送重置链接</p>
      <el-form :model="form" :rules="rules" ref="formRef" label-width="0">
        <el-form-item prop="email">
          <el-input v-model="form.email" placeholder="请输入注册邮箱" :prefix-icon="Message" size="large" />
        </el-form-item>
        <el-form-item prop="captchaCode">
          <div class="captcha-row">
            <el-input
              v-model="form.captchaCode"
              size="large"
              placeholder="请输入图形验证码"
              :prefix-icon="Key"
              maxlength="4"
              @input="form.captchaCode = form.captchaCode.toUpperCase().replace(/[^0-9A-Z]/g, '')"
            />
            <img
              v-if="captchaImg"
              :src="captchaImg"
              class="captcha-img"
              alt="点击刷新验证码"
              title="点击刷新"
              @click="loadCaptcha"
            />
            <div v-else class="captcha-placeholder" @click="loadCaptcha">
              <el-icon><Refresh /></el-icon>
              <span>加载中</span>
            </div>
          </div>
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
import { ref, reactive, onMounted } from 'vue'
import { Message, Key, Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const formRef = ref()
const loading = ref(false)
const form = reactive({ email: '', captchaCode: '' })

const captchaId = ref('')
const captchaImg = ref('')

const rules = {
  email: [
    { required: true, message: '请输入邮箱', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
  captchaCode: [
    { required: true, message: '请输入图形验证码', trigger: 'blur' },
    { len: 4, message: '验证码为 4 位字符', trigger: 'blur' },
  ],
}

const loadCaptcha = async () => {
  try {
    const res: any = await request.get('/api/v1/captcha')
    if (res && res.code === 0 && res.data) {
      captchaId.value = res.data.captcha_id || ''
      captchaImg.value = res.data.captcha_img || ''
      form.captchaCode = ''
    }
  } catch {
    // 静默失败, 用户点击占位符可重试
  }
}

onMounted(() => {
  loadCaptcha()
})

const submit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid: boolean) => {
    if (!valid) return
    loading.value = true
    try {
      const res: any = await request.post('/api/v1/auth/forgot-password', {
        email: form.email,
        captcha_id: captchaId.value,
        captcha_code: form.captchaCode,
      })
      if (res && (res.code === 0 || res.code === 200)) {
        ElMessage.success('重置链接已发送，请检查邮箱')
        form.email = ''
        loadCaptcha()
      } else {
        ElMessage.error((res && res.msg) || '发送失败')
        loadCaptcha()
      }
    } catch (e: any) {
      // 失败后刷新验证码, 避免旧 captcha_id 失效
      loadCaptcha()
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
.captcha-row { display: flex; gap: 10px; width: 100%; align-items: center; }
.captcha-row .el-input { flex: 1; }
.captcha-img { height: 40px; width: 120px; border-radius: 6px; cursor: pointer; border: 1px solid var(--np-border, #2a3245); background: #f1f5f9; flex-shrink: 0; }
.captcha-placeholder { height: 40px; width: 120px; border-radius: 6px; cursor: pointer; border: 1px dashed var(--np-border, #2a3245); background: var(--np-bg-soft, #1a2030); display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 2px; color: var(--np-text-muted, #6b7785); font-size: 10px; flex-shrink: 0; }
.captcha-placeholder:hover { border-color: var(--np-primary, #00f5d4); color: var(--np-primary, #00f5d4); }
</style>
