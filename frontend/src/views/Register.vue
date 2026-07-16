<template>
  <div class="register-page">
    <!-- 背景装饰 -->
    <div class="bg-glow bg-glow-1"></div>
    <div class="bg-glow bg-glow-2"></div>

    <div class="register-card np-card np-fade-in">
      <div class="register-header">
        <div class="logo-row">
          <span class="logo-icon">◆</span>
          <span class="logo-text np-title">NEXUS PANEL</span>
        </div>
        <p class="register-subtitle">注册新账号，开启全球网络加速</p>
      </div>

      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        class="register-form"
        @keyup.enter="handleRegister"
      >
        <el-form-item prop="username">
          <el-input
            v-model="form.username"
            size="large"
            placeholder="请输入用户名"
            :prefix-icon="User"
          />
        </el-form-item>
        <el-form-item prop="email">
          <el-input
            v-model="form.email"
            size="large"
            placeholder="请输入邮箱"
            :prefix-icon="Message"
          />
        </el-form-item>
        <el-form-item prop="password">
          <el-input
            v-model="form.password"
            size="large"
            type="password"
            placeholder="请输入密码"
            show-password
            :prefix-icon="Lock"
          />
        </el-form-item>
        <el-form-item prop="confirmPassword">
          <el-input
            v-model="form.confirmPassword"
            size="large"
            type="password"
            placeholder="请再次输入密码"
            show-password
            :prefix-icon="Lock"
          />
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            size="large"
            class="register-btn"
            :loading="loading"
            @click="handleRegister"
          >
            <span v-if="!loading">注 册</span>
            <span v-else>注册中...</span>
          </el-button>
        </el-form-item>
      </el-form>

      <div class="register-tip">
        <el-icon><InfoFilled /></el-icon>
        <span>注册后将自动获得试用套餐，详情请咨询管理员</span>
      </div>

      <div class="login-link">
        已有账号？
        <router-link to="/login">返回登录</router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { User, Lock, Message, InfoFilled } from '@element-plus/icons-vue'
import request from '@/utils/request'

const router = useRouter()

const formRef = ref<FormInstance>()
const loading = ref(false)

const form = reactive({
  username: '',
  email: '',
  password: '',
  confirmPassword: '',
})

// 表单校验规则
const rules: FormRules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { min: 3, max: 20, message: '用户名长度 3-20 个字符', trigger: 'blur' },
    { pattern: /^[a-zA-Z0-9_]+$/, message: '仅支持字母、数字和下划线', trigger: 'blur' },
  ],
  email: [
    { required: true, message: '请输入邮箱', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 6, max: 32, message: '密码长度 6-32 个字符', trigger: 'blur' },
  ],
  confirmPassword: [
    { required: true, message: '请再次输入密码', trigger: 'blur' },
    {
      validator: (_rule, value, callback) => {
        if (value !== form.password) {
          callback(new Error('两次输入的密码不一致'))
        } else {
          callback()
        }
      },
      trigger: 'blur',
    },
  ],
}

// 注册处理
const handleRegister = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    loading.value = true
    try {
      // 调用后端注册接口
      await request.post('/api/v1/auth/register', {
        username: form.username,
        email: form.email,
        password: form.password,
      })
      ElMessage.success('注册成功，请登录')
      router.push('/login')
    } catch (e: any) {
      // 接口失败时给出友好提示（具体错误由 request 拦截器统一弹窗）
      if (!e || !e.message) {
        ElMessage.error('注册失败，请稍后重试')
      }
    } finally {
      loading.value = false
    }
  })
}
</script>

<style scoped>
.register-page {
  position: relative;
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}

/* 背景光晕 */
.bg-glow {
  position: absolute;
  border-radius: 50%;
  filter: blur(80px);
  opacity: 0.4;
  pointer-events: none;
}
.bg-glow-1 {
  width: 400px;
  height: 400px;
  background: var(--np-primary);
  top: 10%;
  left: 15%;
  animation: float 8s ease-in-out infinite;
}
.bg-glow-2 {
  width: 350px;
  height: 350px;
  background: var(--np-purple);
  bottom: 10%;
  right: 15%;
  animation: float 10s ease-in-out infinite reverse;
}
@keyframes float {
  0%, 100% { transform: translate(0, 0); }
  50% { transform: translate(30px, -30px); }
}

.register-card {
  width: 420px;
  max-width: 92vw;
  padding: 40px 36px;
  position: relative;
  z-index: 1;
  box-shadow: 0 0 40px rgba(0, 245, 212, 0.08), 0 20px 60px rgba(0, 0, 0, 0.5);
}

.register-header {
  text-align: center;
  margin-bottom: 28px;
}
.logo-row {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
}
.logo-icon {
  font-size: 28px;
  color: var(--np-primary);
  text-shadow: 0 0 16px var(--np-primary-glow);
}
.logo-text {
  font-size: 26px;
  font-weight: 700;
  letter-spacing: 3px;
}
.register-subtitle {
  margin: 12px 0 0;
  color: var(--np-text-secondary);
  font-size: 13px;
  letter-spacing: 1px;
}

.register-form {
  margin-top: 8px;
}
.register-btn {
  width: 100%;
  font-size: 16px;
  letter-spacing: 4px;
  background: var(--np-primary) !important;
  color: var(--np-bg) !important;
  border: none !important;
  font-weight: 700;
  box-shadow: 0 0 20px var(--np-primary-dim);
  transition: all 0.25s ease;
}
.register-btn:hover {
  box-shadow: 0 0 30px var(--np-primary-glow);
  transform: translateY(-1px);
}

.register-tip {
  margin-top: 18px;
  padding: 10px 14px;
  background: var(--np-bg-soft);
  border: 1px solid var(--np-border);
  border-radius: 8px;
  color: var(--np-text-muted);
  font-size: 12px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.login-link {
  margin-top: 16px;
  text-align: center;
  color: var(--np-text-secondary);
  font-size: 13px;
}
.login-link a {
  color: var(--np-primary);
  text-decoration: none;
  margin-left: 4px;
}
.login-link a:hover {
  text-shadow: 0 0 8px var(--np-primary-glow);
}
</style>
