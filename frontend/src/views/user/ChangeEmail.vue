<template>
  <div class="user-page">
    <div class="page-header"><h2 class="page-title">修改邮箱</h2></div>
    <div class="np-card form-card">
      <el-alert type="info" :closable="false" show-icon style="margin-bottom: 20px">
        修改后需要重新验证新邮箱才能使用账号功能
      </el-alert>
      <el-form :model="form" :rules="rules" ref="formRef" label-width="100px" v-loading="loading">
        <el-form-item label="新邮箱" prop="email">
          <el-input v-model="form.email" placeholder="请输入新邮箱" />
        </el-form-item>
        <el-form-item label="验证码" prop="code">
          <el-input v-model="form.code" placeholder="6 位邮箱验证码" style="max-width: 240px" />
          <el-button :disabled="cooldown > 0" @click="sendCode" style="margin-left: 8px">
            {{ cooldown > 0 ? cooldown + 's' : '获取验证码' }}
          </el-button>
        </el-form-item>
        <el-form-item label="当前密码" prop="oldPassword">
          <el-input v-model="form.oldPassword" type="password" show-password placeholder="为确认是本人操作，请输入当前账号密码" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="submitting" @click="submit">提交修改</el-button>
          <el-button @click="$router.back()">取消</el-button>
        </el-form-item>
      </el-form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import request from '@/utils/request'
import { useAuthStore } from '@/stores/auth'

const formRef = ref()
const loading = ref(false)
const submitting = ref(false)
const cooldown = ref(0)
let timer: any = null

// 修复 P1-FE10: 用于校验新邮箱是否与当前邮箱相同(避免无效请求)
const authStore = useAuthStore()

const form = reactive({ email: '', code: '', oldPassword: '' })
const rules = {
  email: [
    { required: true, message: '请输入新邮箱', trigger: 'blur' },
    { type: 'email', message: '邮箱格式不正确', trigger: 'blur' },
  ],
  code: [{ required: true, message: '请输入验证码', trigger: 'blur' }],
  oldPassword: [
    { required: true, message: '请输入当前密码', trigger: 'blur' },
    { min: 8, message: '密码至少 8 位', trigger: 'blur' },
  ],
}

const sendCode = async () => {
  if (!form.email) { ElMessage.warning('请先填写新邮箱'); return }
  // 修复 P1-FE10: 新邮箱与当前邮箱相同时直接拦截, 避免发无意义验证码 + 后端校验失败
  if (form.email === authStore.userInfo?.email) {
    ElMessage.warning('新邮箱不能与当前邮箱相同')
    return
  }
  try {
    // 修复 F2: 后端 SendVerifyCode 要求 type(oneof=verify change), 原前端发 purpose:'change_email' 被拒
    const res: any = await request.post('/api/v1/email/send-code', { email: form.email, type: 'change' })
    if (res && (res.code === 0 || res.code === 200)) {
      ElMessage.success('验证码已发送')
      cooldown.value = 60
      timer = setInterval(() => {
        cooldown.value--
        if (cooldown.value <= 0) clearInterval(timer)
      }, 1000)
    } else {
      ElMessage.error((res && res.msg) || '发送失败')
    }
  } catch (e: any) {
    ElMessage.error(e?.message || '请求失败')
  }
}

const submit = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (ok: boolean) => {
    if (!ok) return
    // 修复 P1-FE10: 提交前再次校验新旧邮箱相同(防止用户在 sendCode 后改回原邮箱)
    if (form.email === authStore.userInfo?.email) {
      ElMessage.warning('新邮箱不能与当前邮箱相同')
      return
    }
    try {
      await ElMessageBox.confirm('确定修改邮箱?', '确认操作', { type: 'warning' })
    } catch { return }
    submitting.value = true
    try {
      // 修复 F2: 后端 ChangeEmail 要求 new_email/verify_code/old_password, 原前端发 email/code 缺 old_password
      const res: any = await request.post('/api/v1/auth/change-email', {
        new_email: form.email,
        verify_code: form.code,
        old_password: form.oldPassword,
      })
      if (res && (res.code === 0 || res.code === 200)) {
        ElMessage.success('邮箱已换绑')
        form.email = ''; form.code = ''; form.oldPassword = ''
      } else {
        ElMessage.error((res && res.msg) || '修改失败')
      }
    } catch (e: any) {
      ElMessage.error(e?.message || '请求失败')
    } finally {
      submitting.value = false
    }
  })
}

onUnmounted(() => { if (timer) clearInterval(timer) })
</script>

<style scoped>
.user-page { padding: 20px; }
.page-header { margin-bottom: 20px; }
.page-title { margin: 0; font-size: 20px; color: var(--np-text); }
.form-card { padding: 24px; }
</style>
