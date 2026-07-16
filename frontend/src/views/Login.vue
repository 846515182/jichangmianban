<template>
  <div class="login-container">
    <el-card class="login-card">
      <h2 class="title">登录 Nexus-Panel</h2>
      <el-form :model="form" :rules="rules" ref="formRef" @submit.prevent="onSubmit">
        <el-form-item label="账号" prop="username">
          <el-input v-model="form.username" placeholder="用户名或邮箱" autocomplete="username" size="large" />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input v-model="form.password" type="password" show-password
                    placeholder="密码" autocomplete="current-password" size="large" />
        </el-form-item>
        <el-button type="primary" native-type="submit" :loading="loading" size="large" style="width:100%">
          登录
        </el-button>

        <div class="links">
          <router-link to="/register" class="link-item">没有账号?去注册</router-link>
          <span class="divider" aria-hidden="true">|</span>
          <router-link to="/forgot-password" class="link-item">忘记密码?</router-link>
        </div>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'
import { useUserStore } from '@/stores/user'

const router = useRouter()
const route = useRoute()
const userStore = useUserStore()

const form = ref({ username: '', password: '' })
const formRef = ref()
const loading = ref(false)

const rules = {
  username: [{ required: true, message: '请输入用户名或邮箱', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

const onSubmit = async () => {
  await formRef.value.validate()
  loading.value = true
  try {
    const role = await userStore.loginAuto(form.value.username, form.value.password)
    ElMessage.success('登录成功')
    const redirect = (route.query.redirect as string) || (role === 'admin' ? '/admin/dashboard' : '/')
    router.push(redirect)
  } catch (e: any) {
    ElMessage.error(e?.msg || '登录失败')
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-container {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #0f172a, #1e293b);
  padding: 16px;
}

.login-card {
  width: 100%;
  max-width: 420px;
  border-radius: 12px;
}

.login-card :deep(.el-card__body) {
  padding: 32px 24px;
}

.title {
  text-align: center;
  margin: 0 0 28px;
  font-size: 26px;
  font-weight: 700;
  color: #1e293b;
}

:deep(.el-form-item__label) {
  font-size: 15px;
  font-weight: 500;
}

:deep(.el-input__inner) {
  font-size: 15px;
}

.links {
  display: flex;
  justify-content: center;
  align-items: center;
  gap: 12px;
  margin-top: 20px;
  font-size: 14px;
}

.link-item {
  color: #3b82f6;
  text-decoration: none;
  transition: color 0.2s;
  font-weight: 500;
}

.link-item:hover {
  color: #2563eb;
  text-decoration: underline;
}

.divider {
  color: #94a3b8;
  user-select: none;
}

/* 移动端优化 */
@media (max-width: 640px) {
  .login-container {
    padding: 12px;
  }
  
  .login-card :deep(.el-card__body) {
    padding: 24px 20px;
  }
  
  .title {
    font-size: 22px;
    margin-bottom: 24px;
  }
  
  :deep(.el-form-item__label) {
    font-size: 14px;
  }
  
  :deep(.el-input__inner) {
    font-size: 14px;
    height: 44px;
  }
  
  :deep(.el-button) {
    height: 44px;
    font-size: 15px;
  }
  
  .links {
    font-size: 13px;
    gap: 10px;
    margin-top: 16px;
  }
}

/* 超小屏幕优化 */
@media (max-width: 360px) {
  .login-card :deep(.el-card__body) {
    padding: 20px 16px;
  }
  
  .title {
    font-size: 20px;
  }
  
  .links {
    flex-direction: column;
    gap: 8px;
  }
  
  .divider {
    display: none;
  }
}
</style>
