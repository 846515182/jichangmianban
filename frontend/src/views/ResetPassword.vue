<template>
  <div class="reset-page">
    <div class="reset-card">
      <h2 class="title">重置密码</h2>
      <p v-if="!valid" class="subtitle error">链接无效或已过期</p>
      <p v-else class="subtitle">请输入新密码（至少 8 位，含字母+数字）</p>
      <el-form v-if="valid" :model="form" :rules="rules" ref="formRef" label-width="0">
        <el-form-item prop="password">
          <el-input v-model="form.password" type="password" show-password placeholder="新密码" size="large" />
        </el-form-item>
        <el-form-item prop="confirm">
          <el-input v-model="form.confirm" type="password" show-password placeholder="确认密码" size="large" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" size="large" :loading="loading" style="width: 100%" @click="submit">
            重置密码
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
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import request from '@/utils/request'

const route = useRoute()
const router = useRouter()
const formRef = ref()
const loading = ref(false)
const valid = ref(false)
const token = ref('')
const form = reactive({ password: '', confirm: '' })
const rules = {
  password: [
    { required: true, message: '请输入新密码', trigger: 'blur' },
    { min: 8, message: '至少 8 位', trigger: 'blur' },
    { pattern: /^(?=.*[A-Za-z])(?=.*\d).{8,}$/, message: '需含字母+数字', trigger: 'blur' },
  ],
  confirm: [
    { required: true, message: '请再次输入', trigger: 'blur' },
    {
      validator: (_: any, v: string, cb: (e?: Error) => void) => {
        if (v !== form.password) cb(new Error('两次输入不一致'))
        else cb()
      },
      trigger: 'blur',
    },
  ],
}

onMounted(() => {
  token.value = (route.query.token as string) || ''
  valid.value = !!token.value
})

const submit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (ok: boolean) => {
    if (!ok) return
    loading.value = true
    try {
      const res: any = await request.post('/api/v1/auth/reset-password', {
        token: token.value,
        password: form.password,
      })
      if (res && (res.code === 0 || res.code === 200)) {
        ElMessage.success('密码已重置')
        router.push('/login')
      } else {
        ElMessage.error((res && res.msg) || '重置失败')
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
.reset-page { min-height: 100vh; display: flex; align-items: center; justify-content: center; background: var(--np-bg, #0e1422); }
.reset-card { background: var(--np-card, #1a2030); padding: 40px; border-radius: 16px; width: 100%; max-width: 420px; box-shadow: 0 8px 32px rgba(0,0,0,0.3); }
.title { margin: 0 0 8px; color: var(--np-text, #e7ecf3); font-size: 24px; text-align: center; }
.subtitle { color: var(--np-text-secondary, #8b98a9); text-align: center; margin-bottom: 28px; }
.subtitle.error { color: #f56c6c; }
.back { text-align: center; margin-top: 16px; }
</style>
