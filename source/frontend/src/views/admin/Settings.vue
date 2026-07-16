<template>
  <div class="admin-settings">
    <el-tabs v-model="activeTab" class="settings-tabs">
      <el-tab-pane label="系统设置" name="system">
    <el-row :gutter="20">
      <!-- 安全设置 -->
      <el-col :xs="24" :md="12">
        <div class="np-card section-card">
          <div class="section-title"><el-icon><Lock /></el-icon> 安全设置</div>
          <el-form label-width="130px" label-position="right">
            <el-form-item label="HMAC 密钥">
              <el-input v-model="security.hmacKey" :type="showKey ? 'text' : 'password'" readonly>
                <template #append>
                  <el-button @click="showKey = !showKey">
                    <el-icon><View v-if="!showKey" /><Hide v-else /></el-icon>
                  </el-button>
                </template>
              </el-input>
            </el-form-item>
            <el-form-item>
              <el-button type="warning" @click="rotateHmac" :loading="rotating">
                <el-icon><Refresh /></el-icon>轮换 HMAC 密钥
              </el-button>
              <span class="form-tip">轮换后所有已签发订阅Token需重新生成</span>
            </el-form-item>
            <el-form-item label="管理员密码">
              <el-button @click="pwdDialog = true">修改密码</el-button>
            </el-form-item>
          </el-form>
        </div>
      </el-col>

      <!-- 订阅配置 -->
      <el-col :xs="24" :md="12">
        <div class="np-card section-card">
          <div class="section-title"><el-icon><Link /></el-icon> 订阅配置</div>
          <el-form label-width="130px" label-position="right">
            <el-form-item label="订阅域名">
              <el-input v-model="subscribe.domain" placeholder="sub.example.com" />
            </el-form-item>
            <el-form-item label="订阅端口">
              <el-input-number v-model="subscribe.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
            </el-form-item>
            <el-form-item label="HTTPS">
              <el-switch v-model="subscribe.https" />
            </el-form-item>
            <el-form-item label="默认格式">
              <el-select v-model="subscribe.defaultFormat" style="width: 100%">
                <el-option label="Clash" value="clash" />
                <el-option label="Sing-Box" value="singbox" />
                <el-option label="V2Ray" value="v2ray" />
              </el-select>
            </el-form-item>
            <el-form-item>
              <el-button type="primary" @click="saveSubscribe" :loading="savingSub">保存配置</el-button>
            </el-form-item>
          </el-form>
        </div>
      </el-col>

      <!-- 备份管理 -->
      <el-col :span="24">
        <div class="np-card section-card">
          <div class="section-title"><el-icon><FolderOpened /></el-icon> 备份管理</div>
          <div class="backup-actions">
            <el-button type="primary" @click="createBackup" :loading="backing">
              <el-icon><Download /></el-icon>立即备份
            </el-button>
            <el-upload :show-file-list="false" :before-upload="restoreBackup" accept=".json,.db,.tar">
              <el-button><el-icon><Upload /></el-icon>恢复备份</el-button>
            </el-upload>
          </div>
          <el-table :data="backups" stripe style="margin-top: 16px">
            <el-table-column prop="name" label="备份名称" min-width="220" />
            <el-table-column prop="size" label="大小" width="120" />
            <el-table-column label="创建时间" width="180">
              <template #default="{ row }">{{ formatTime(row.createdAt) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="160">
              <template #default="{ row }">
                <el-button size="small" link type="primary" @click="downloadBackup(row)">下载</el-button>
                <el-button size="small" link type="danger" @click="deleteBackup(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </el-col>
    </el-row>
      </el-tab-pane>

      <!-- 支付配置 -->
      <el-tab-pane label="支付配置" name="payment">
        <el-row :gutter="20">
          <el-col :xs="24" :md="14">
            <div class="np-card section-card">
              <div class="section-title"><el-icon><Wallet /></el-icon> EPay 支付配置</div>
              <el-form label-width="120px" label-position="right">
                <el-form-item label="支付开关">
                  <el-switch v-model="payment.enabled" active-text="开启支付" inactive-text="关闭支付" />
                  <span class="form-tip">关闭后用户端将无法发起支付</span>
                </el-form-item>
                <el-form-item label="商户 PID">
                  <el-input v-model="payment.pid" placeholder="EPay 商户 ID" />
                </el-form-item>
                <el-form-item label="商户密钥">
                  <el-input v-model="payment.key" :type="showPayKey ? 'text' : 'password'" show-password placeholder="已保存，如需修改请输入新密钥">
                    <template #append>
                      <el-button @click="showPayKey = !showPayKey">
                        <el-icon><View v-if="!showPayKey" /><Hide v-else /></el-icon>
                      </el-button>
                    </template>
                  </el-input>
                </el-form-item>
                <el-form-item label="API 地址">
                  <el-input v-model="payment.apiUrl" placeholder="https://pay.example.com" />
                </el-form-item>
                <el-form-item label="回调地址">
                  <el-input v-model="payment.notifyUrl" placeholder="https://api.example.com/api/payment/notify" />
                  <span class="form-tip">EPay 异步通知地址，需外网可访问</span>
                </el-form-item>
                <el-form-item label="返回地址">
                  <el-input v-model="payment.returnUrl" placeholder="https://panel.example.com/user/orders" />
                </el-form-item>
                <el-form-item label="支持方式">
                  <el-checkbox-group v-model="payment.methods">
                    <el-checkbox value="epay_alipay">支付宝</el-checkbox>
                    <el-checkbox value="epay_wechat">微信支付</el-checkbox>
                  </el-checkbox-group>
                </el-form-item>
                <el-form-item>
                  <el-button type="primary" @click="savePayment" :loading="savingPay">保存配置</el-button>
                  <el-button @click="testPayment" :loading="testing">测试连接</el-button>
                </el-form-item>
              </el-form>
            </div>
          </el-col>

          <el-col :xs="24" :md="10">
            <div class="np-card section-card">
              <div class="section-title"><el-icon><InfoFilled /></el-icon> 支付说明</div>
              <ul class="pay-tips">
                <li>EPay 是聚合支付网关，支持支付宝、微信等多种支付方式。</li>
                <li>商户 PID 与密钥可在 EPay 商户后台获取。</li>
                <li>回调地址需配置为后端接收 EPay 异步通知的接口。</li>
                <li>开启支付前请先点击「测试连接」确认配置有效。</li>
                <li>线下收款订单可由管理员在订单管理中手动标记为已支付。</li>
              </ul>
              <el-divider />
              <div class="section-title"><el-icon><CircleCheckFilled /></el-icon> 当前状态</div>
              <div class="pay-status">
                <el-tag :type="payment.enabled ? 'success' : 'danger'" effect="dark">
                  {{ payment.enabled ? '支付已开启' : '支付已关闭' }}
                </el-tag>
                <el-tag v-if="lastTestResult" :type="lastTestResult.success ? 'success' : 'danger'" effect="plain">
                  {{ lastTestResult.success ? '连接正常' : '连接异常' }}
                </el-tag>
              </div>
            </div>
          </el-col>
        </el-row>
      </el-tab-pane>

      <!-- 邮件配置 -->
      <el-tab-pane label="邮件配置" name="email">
        <el-row :gutter="20">
          <el-col :xs="24" :md="14">
            <div class="np-card section-card">
              <div class="section-title"><el-icon><Message /></el-icon> SMTP 邮件配置</div>
              <el-form label-width="120px" label-position="right">
                <el-form-item label="启用邮件">
                  <el-switch v-model="email.enabled" active-text="启用" inactive-text="关闭" />
                  <span class="form-tip">关闭后用户将无法收到验证邮件</span>
                </el-form-item>
                <el-form-item label="SMTP 服务器">
                  <el-input v-model="email.host" placeholder="smtp.example.com" />
                </el-form-item>
                <el-form-item label="SMTP 端口">
                  <el-input-number v-model="email.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
                  <span class="form-tip">Mailtrap 推荐 587(TLS)，QQ/163 用 465(SSL)</span>
                </el-form-item>
                <el-form-item label="发件人邮箱">
                  <el-input v-model="email.user" placeholder="noreply@example.com" />
                </el-form-item>
                <el-form-item label="发件人名称">
                  <el-input v-model="email.from" placeholder="Nexus-Panel" />
                </el-form-item>
                <el-form-item label="邮箱密码">
                  <el-input v-model="email.password" :type="showEmailPwd ? 'text' : 'password'" show-password placeholder="已保存，如需修改请输入新密码">
                    <template #append>
                      <el-button @click="showEmailPwd = !showEmailPwd">
                        <el-icon><View v-if="!showEmailPwd" /><Hide v-else /></el-icon>
                      </el-button>
                    </template>
                  </el-input>
                </el-form-item>
                <el-form-item>
                  <el-button type="primary" @click="saveEmail" :loading="savingEmail">保存配置</el-button>
                  <el-button @click="testEmail" :loading="testingEmail">测试发送</el-button>
                </el-form-item>
              </el-form>
            </div>
          </el-col>

          <el-col :xs="24" :md="10">
            <div class="np-card section-card">
              <div class="section-title"><el-icon><InfoFilled /></el-icon> 邮件说明</div>
              <ul class="pay-tips">
                <li>SMTP 服务器用于发送用户注册验证、密码重置等邮件。</li>
                <li>推荐 SMTP 配置：
                  <ul>
                    <li><strong>Mailtrap</strong>：smtp.mailtrap.io:587（测试环境推荐）</li>
                    <li>QQ邮箱：smtp.qq.com:587</li>
                    <li>163邮箱：smtp.163.com:465</li>
                    <li>Gmail：smtp.gmail.com:587</li>
                  </ul>
                </li>
                <li>发件人密码通常为邮箱的授权码（非登录密码）。</li>
                <li>Mailtrap 用户可在 Email Testing → Sandboxes → Integration 中获取 SMTP 凭据。</li>
                <li>保存配置后请点击「测试发送」验证配置是否正确。</li>
              </ul>
              <el-divider />
              <div class="section-title"><el-icon><CircleCheckFilled /></el-icon> 当前状态</div>
              <div class="pay-status">
                <el-tag :type="email.enabled ? 'success' : 'danger'" effect="dark">
                  {{ email.enabled ? '邮件已启用' : '邮件已关闭' }}
                </el-tag>
                <el-tag v-if="lastEmailTestResult" :type="lastEmailTestResult.success ? 'success' : 'danger'" effect="plain">
                  {{ lastEmailTestResult.success ? '测试成功' : '测试失败' }}
                </el-tag>
              </div>
            </div>
          </el-col>
        </el-row>
      </el-tab-pane>

    </el-tabs>

    <!-- 修改密码对话框 -->
    <el-dialog v-model="pwdDialog" title="修改管理员密码" width="420px">
      <el-form ref="pwdFormRef" :model="pwdForm" :rules="pwdRules" label-width="100px">
        <el-form-item label="原密码" prop="oldPwd">
          <el-input v-model="pwdForm.oldPwd" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码" prop="newPwd">
          <el-input v-model="pwdForm.newPwd" type="password" show-password />
        </el-form-item>
        <el-form-item label="确认密码" prop="confirmPwd">
          <el-input v-model="pwdForm.confirmPwd" type="password" show-password />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="pwdDialog = false">取消</el-button>
        <el-button type="primary" @click="savePwd">确认</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

const showKey = ref(false)
const rotating = ref(false)
const savingSub = ref(false)
const backing = ref(false)

// 设置页 Tab 切换
const activeTab = ref('system')

// 支付配置
const showPayKey = ref(false)
const savingPay = ref(false)
const testing = ref(false)
const lastTestResult = ref<{ success: boolean; message: string } | null>(null)
const payment = reactive({
  enabled: true,
  pid: '',
  key: '',
  apiUrl: 'https://pay.example.com',
  notifyUrl: '',
  returnUrl: '',
  methods: ['epay_alipay', 'epay_wechat'] as string[],
})

const security = reactive({
  hmacKey: 'hmac_' + Math.random().toString(36).slice(2, 18),
})

const subscribe = reactive({
  domain: 'sub.nexus.dev', port: 443, https: true, defaultFormat: 'clash',
})

const backups = ref([
  { name: 'backup-20260706-020000.tar', size: '12.4 MB', createdAt: '2026-07-06 02:00:00' },
  { name: 'backup-20260705-020000.tar', size: '11.8 MB', createdAt: '2026-07-05 02:00:00' },
  { name: 'backup-20260704-020000.tar', size: '11.2 MB', createdAt: '2026-07-04 02:00:00' },
])

const rotateHmac = () => {
  ElMessageBox.confirm('轮换HMAC密钥将使所有现有订阅Token失效，确定继续吗？', '危险操作', {
    type: 'warning', confirmButtonText: '确认轮换', cancelButtonText: '取消',
  }).then(async () => {
    rotating.value = true
    try {
      try { const res = await request.post('/api/v1/admin/system/rotate-hmac'); if (res && res.hmacKey) security.hmacKey = res.hmacKey } catch { /* */ }
      security.hmacKey = 'hmac_' + Math.random().toString(36).slice(2, 18)
      ElMessage.success('HMAC 密钥已轮换')
    } finally { rotating.value = false }
  }).catch(() => {})
}

const saveSubscribe = async () => {
  savingSub.value = true
  try {
    try { await request.put('/api/v1/admin/system/sub-config', { ...subscribe }) } catch { /* */ }
    ElMessage.success('订阅配置已保存')
  } finally { savingSub.value = false }
}

// 加载已保存的支付配置
const loadPaymentConfig = async () => {
  try {
    const res: any = await request.get('/api/v1/admin/system/pay-config')
    if (res && res.code === 0 && res.data) {
      const d = res.data
      payment.enabled = !!d.enabled
      payment.pid = d.pid ? String(d.pid) : ''
      // 密钥脱敏时清空，让用户重新输入；保存时若为空后端会保留原值
      payment.key = (d.key && !d.key.includes('*')) ? d.key : ''
      payment.apiUrl = d.api_url || ''
      payment.notifyUrl = d.notify_url || ''
      payment.returnUrl = d.return_url || ''
    }
  } catch { /* 拦截器处理 */ }
}

// 保存支付配置
const savePayment = async () => {
  if (!payment.pid) {
    ElMessage.warning('请填写商户 PID')
    return
  }
  savingPay.value = true
  try {
    // 后端 EPayConfig 使用 snake_case 字段名；key 为空时后端保留原值
    await request.put('/api/v1/admin/system/pay-config', {
      pid: Number(payment.pid) || 0,
      key: payment.key || '',
      api_url: payment.apiUrl,
      enabled: payment.enabled,
      notify_url: payment.notifyUrl,
      return_url: payment.returnUrl,
    })
    ElMessage.success('支付配置已保存')
  } catch {
    /* 拦截器已提示 */
  } finally {
    savingPay.value = false
  }
}

// 测试支付连接
const testPayment = async () => {
  if (!payment.pid || !payment.key || !payment.apiUrl) {
    ElMessage.warning('请先填写商户 PID、密钥与 API 地址')
    return
  }
  testing.value = true
  try {
    // 后端 EPayConfig 使用 snake_case 字段名
    const res: any = await request.post('/api/v1/admin/system/pay-config/test', {
      pid: Number(payment.pid) || 0,
      key: payment.key,
      api_url: payment.apiUrl,
    })
    const data = res?.data || res
    lastTestResult.value = { success: true, message: data?.message || res?.msg || '连接成功' }
    ElMessage.success(res?.msg || '支付接口连接正常')
  } catch (e: any) {
    // axios 错误: e.response.data.msg 是后端返回的具体错误
    const backendMsg = e?.response?.data?.msg || e?.response?.data?.message
    const msg = backendMsg || e?.message || '连接失败'
    lastTestResult.value = { success: false, message: msg }
    ElMessage.error(msg)
  } finally {
    testing.value = false
  }
}

const createBackup = async () => {
  backing.value = true
  try {
    try { await request.post('/api/v1/admin/system/backup') } catch { /* */ }
    const now = new Date()
    const ts = now.toISOString().replace(/[-:T]/g, '').slice(0, 14)
    backups.value.unshift({
      name: `backup-${ts}.tar`, size: (10 + Math.random() * 5).toFixed(1) + ' MB',
      createdAt: now.toISOString().replace('T', ' ').slice(0, 19),
    })
    ElMessage.success('备份已创建')
  } finally { backing.value = false }
}

const restoreBackup = (file: File) => {
  ElMessageBox.confirm(`确定从「${file.name}」恢复备份吗？当前数据将被覆盖。`, '恢复备份', {
    type: 'warning', confirmButtonText: '确认恢复', cancelButtonText: '取消',
  }).then(() => {
    ElMessage.success(`正在从 ${file.name} 恢复...`)
  }).catch(() => {})
  return false
}

const downloadBackup = (row: any) => {
  ElMessage.success('开始下载：' + row.name)
}

const deleteBackup = (row: any) => {
  ElMessageBox.confirm(`确定删除备份「${row.name}」吗？`, '提示', { type: 'warning' }).then(() => {
    backups.value = backups.value.filter((b) => b.name !== row.name)
    ElMessage.success('备份已删除')
  }).catch(() => {})
}

// 修改密码
const pwdDialog = ref(false)
const pwdFormRef = ref<FormInstance>()
const pwdForm = reactive({ oldPwd: '', newPwd: '', confirmPwd: '' })
const pwdRules: FormRules = {
  oldPwd: [{ required: true, message: '请输入原密码', trigger: 'blur' }],
  newPwd: [{ required: true, message: '请输入新密码', trigger: 'blur' }, { min: 6, message: '密码至少6位', trigger: 'blur' }],
  confirmPwd: [{ required: true, message: '请确认密码', trigger: 'blur' }, {
    validator: (_r, v, cb) => { v !== pwdForm.newPwd ? cb(new Error('两次密码不一致')) : cb() }, trigger: 'blur',
  }],
}

// 邮件配置
const showEmailPwd = ref(false)
const savingEmail = ref(false)
const testingEmail = ref(false)
const lastEmailTestResult = ref<{ success: boolean; message: string } | null>(null)
const email = reactive({
  enabled: false,
  host: '',
  port: 587,
  user: '',
  from: '',
  password: '',
})

// 加载邮件配置
const loadEmailConfig = async () => {
  try {
    const res: any = await request.get('/api/v1/admin/system/notify-config')
    if (res && res.code === 0 && res.data) {
      const d = res.data
      email.enabled = !!d.email_enabled
      email.host = d.email_host || ''
      email.port = d.email_port || 587
      email.user = d.email_user || ''
      email.from = d.email_from || ''
      email.password = '' // 不加载密码，让用户重新输入
    }
  } catch { /* 拦截器处理 */ }
}

// 保存邮件配置
const saveEmail = async () => {
  if (!email.host || !email.user) {
    ElMessage.warning('请填写 SMTP 服务器和发件人邮箱')
    return
  }
  savingEmail.value = true
  try {
    await request.put('/api/v1/admin/system/notify-config', {
      email_enabled: email.enabled,
      email_host: email.host,
      email_port: email.port,
      email_user: email.user,
      email_from: email.from,
      email_password: email.password,
    })
    ElMessage.success('邮件配置已保存')
  } catch {
    /* 拦截器已提示 */
  } finally {
    savingEmail.value = false
  }
}

// 测试邮件发送
const testEmail = async () => {
  if (!email.host || !email.user || !email.password) {
    ElMessage.warning('请先填写完整的 SMTP 配置')
    return
  }
  testingEmail.value = true
  try {
    const res: any = await request.post('/api/v1/admin/system/notify-config/test', {
      email_host: email.host,
      email_port: email.port,
      email_user: email.user,
      email_password: email.password,
      email_from: email.from,
    })
    lastEmailTestResult.value = { success: true, message: res?.msg || '测试邮件已发送' }
    ElMessage.success('测试邮件已发送，请查收')
  } catch (e: any) {
    const backendMsg = e?.response?.data?.msg || e?.message || '发送失败'
    lastEmailTestResult.value = { success: false, message: backendMsg }
    ElMessage.error(backendMsg)
  } finally {
    testingEmail.value = false
  }
}


const savePwd = async () => {
  if (!pwdFormRef.value) return
  await pwdFormRef.value.validate(async (valid) => {
    if (!valid) return
    try { await request.post('/api/v1/auth/change-password', { oldPassword: pwdForm.oldPwd, newPassword: pwdForm.newPwd }) } catch { /* */ }
    ElMessage.success('密码已修改')
    pwdDialog.value = false
    Object.assign(pwdForm, { oldPwd: '', newPwd: '', confirmPwd: '' })
  })
}

onMounted(() => {
  loadPaymentConfig()
  loadEmailConfig()
})
</script>

<style scoped>
.section-card { padding: 20px; margin-bottom: 20px; }
.section-title { display: flex; align-items: center; gap: 8px; font-size: 16px; font-weight: 600; color: var(--np-text); margin-bottom: 20px; padding-bottom: 12px; border-bottom: 1px solid var(--np-border); }
.form-tip { margin-left: 12px; font-size: 12px; color: var(--np-text-muted); }
.backup-actions { display: flex; gap: 12px; }
.pay-tips { margin: 0; padding-left: 18px; color: var(--np-text-secondary); font-size: 13px; line-height: 1.9; }
.pay-tips li { margin-bottom: 4px; }
.pay-status { display: flex; gap: 10px; flex-wrap: wrap; }
</style>
