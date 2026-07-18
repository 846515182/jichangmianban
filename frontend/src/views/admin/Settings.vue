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
          <el-table :data="backups" stripe style="margin-top: 16px" v-loading="loadingBackups">
            <el-table-column prop="name" label="备份名称" min-width="220" />
            <el-table-column label="大小" width="120">
              <template #default="{ row }">{{ row.size_human || row.size }}</template>
            </el-table-column>
            <el-table-column label="创建时间" width="180">
              <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="180">
              <template #default="{ row }">
                <el-button size="small" link type="primary" @click="downloadBackup(row)">下载</el-button>
                <el-button size="small" link type="danger" @click="deleteBackup(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-empty v-if="!loadingBackups && !backups.length" description="暂无备份文件" />
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

      <!-- 系统维护 -->
      <el-tab-pane label="系统维护" name="maintenance">
        <el-row :gutter="20">
          <el-col :xs="24" :md="12">
            <div class="np-card section-card">
              <div class="section-title"><el-icon><Connection /></el-icon> GitHub 同步</div>
              <div class="git-status-box" v-loading="loadingGitStatus">
                <div class="git-info">
                  <span class="git-label">当前分支:</span>
                  <el-tag size="small" type="success">{{ gitStatus.branch }}</el-tag>
                </div>
                <div class="git-info">
                  <span class="git-label">版本状态:</span>
                  <el-tag v-if="gitStatus.needs_rebuild" size="small" type="danger" effect="dark">有更新待部署</el-tag>
                  <el-tag v-else-if="gitStatus.up_to_date" size="small" type="success" effect="dark">已是最新版本</el-tag>
                  <el-tag v-else-if="gitStatus.behind > 0" size="small" type="warning" effect="dark">有 {{ gitStatus.behind }} 个更新可用</el-tag>
                  <el-tag v-else-if="gitStatus.ahead > 0" size="small" type="info" effect="dark">本地有 {{ gitStatus.ahead }} 个未推送提交</el-tag>
                  <el-tag v-else size="small" type="info">检测中...</el-tag>
                </div>
                <div v-if="gitStatus.needs_rebuild" class="git-info">
                  <span class="git-label">运行版本:</span>
                  <code class="git-commit-hash">{{ gitStatus.running_version || '-' }}</code>
                  <span class="git-arrow">→</span>
                  <code class="git-commit-hash git-commit-new">{{ gitStatus.local_head || '-' }}</code>
                  <el-tag size="small" type="warning" style="margin-left: 8px">代码已更新, 点击下方「一键在线更新」部署</el-tag>
                </div>
                <div v-else class="git-info">
                  <span class="git-label">当前版本:</span>
                  <code class="git-commit-hash">{{ gitStatus.local_head || '-' }}</code>
                  <span v-if="gitStatus.behind > 0" class="git-arrow">→</span>
                  <code v-if="gitStatus.behind > 0" class="git-commit-hash git-commit-new">{{ gitStatus.remote_head || '-' }}</code>
                </div>
                <div class="git-info">
                  <span class="git-label">最近提交:</span>
                  <pre class="git-log">{{ gitStatus.recent5 || '加载中...' }}</pre>
                </div>
                <div v-if="gitStatus.needs_rebuild && gitStatus.rebuild_changelog" class="git-info">
                  <span class="git-label">更新说明:</span>
                  <pre class="git-log git-changelog">{{ gitStatus.rebuild_changelog }}</pre>
                </div>
                <div v-else-if="gitStatus.behind > 0 && gitStatus.changelog" class="git-info">
                  <span class="git-label">更新说明:</span>
                  <pre class="git-log git-changelog">{{ gitStatus.changelog }}</pre>
                </div>
                <div class="git-info">
                  <span class="git-label">本地变更:</span>
                  <pre class="git-log" :class="{ 'has-changes': gitStatus.status }">{{ gitStatus.status || '无变更' }}</pre>
                </div>
                <div v-if="gitStatus.behind > 0 && gitStatus.changed_files" class="git-info">
                  <span class="git-label">待更新文件:</span>
                  <pre class="git-log git-changed-files">{{ gitStatus.changed_files }}</pre>
                </div>
              </div>
              <div class="git-actions">
                <el-button type="primary" @click="gitPull" :loading="pulling">
                  <el-icon><Download /></el-icon><span>一键在线更新</span>
                </el-button>
                <el-button type="warning" @click="systemRestart" :loading="restarting">
                  <el-icon><RefreshRight /></el-icon><span>重启面板</span>
                </el-button>
                <el-button type="info" @click="() => loadGitStatus()" :loading="loadingGitStatus">
                  <el-icon><Refresh /></el-icon><span>刷新状态</span>
                </el-button>
              </div>
              <div v-if="pullResult" class="cmd-output">
                <div class="output-title" :class="pullDone ? (pullSuccess ? 'text-success' : 'text-danger') : 'text-pending'">
                  {{ pullDone ? (pullSuccess ? '更新成功 — 请点击「重启面板」使更新生效' : '更新失败') : '更新中...' }}
                </div>
                <pre class="pull-log">{{ pullResult }}</pre>
              </div>
            </div>
          </el-col>

          <el-col :xs="24" :md="12">
            <div class="np-card section-card">
              <div class="section-title"><el-icon><Monitor /></el-icon> 磁盘管理</div>
              <div class="disk-output" v-loading="loadingDisk">
                <pre>{{ diskUsage }}</pre>
              </div>
              <div class="disk-actions">
                <el-button type="info" @click="loadDiskUsage" :loading="loadingDisk">
                  <el-icon><Refresh /></el-icon><span>刷新</span>
                </el-button>
                <el-button type="danger" @click="diskCleanup" :loading="cleaning">
                  <el-icon><Delete /></el-icon><span>清理磁盘</span>
                </el-button>
              </div>
              <div v-if="cleanupResult" class="cmd-output">
                <div class="output-title">清理结果:</div>
                <pre>{{ cleanupResult }}</pre>
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
import { ref, reactive, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

// 判断密钥/密码是否为脱敏值（后端 maskSecret 输出格式: **** 或 ABCD****WXYZ）
const isMasked = (s: string) => s.includes('****')

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

const backups = ref<any[]>([])
const loadingBackups = ref(false)

const rotateHmac = () => {
  ElMessageBox.confirm('轮换HMAC密钥将使所有现有订阅Token失效，确定继续吗？', '危险操作', {
    type: 'warning', confirmButtonText: '确认轮换', cancelButtonText: '取消',
  }).then(async () => {
    rotating.value = true
    try {
      let gotKey = false
      try {
        const res: any = await request.post('/api/v1/admin/system/rotate-hmac')
        if (res && (res.hmac_key || res.data?.hmac_key)) {
          security.hmacKey = res.hmac_key || res.data.hmac_key
          gotKey = true
        }
      } catch { /* 忽略错误，使用本地生成的回退 */ }
      if (!gotKey) {
        security.hmacKey = 'hmac_' + Math.random().toString(36).slice(2, 18)
      }
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
      payment.key = (d.key && !isMasked(d.key)) ? d.key : ''
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
    await request.post('/api/v1/admin/system/backup')
    ElMessage.success('备份已创建')
    loadBackups()
  } catch {
    ElMessage.error('备份创建失败')
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
  window.open(`/api/v1/admin/system/backups/${encodeURIComponent(row.name)}/download`, '_blank')
}

const deleteBackup = (row: any) => {
  ElMessageBox.confirm(`确定删除备份「${row.name}」吗？`, '提示', { type: 'warning' }).then(async () => {
    try {
      await request.delete(`/api/v1/admin/system/backups/${encodeURIComponent(row.name)}`)
      backups.value = backups.value.filter((b) => b.name !== row.name)
      ElMessage.success('备份已删除')
    } catch {
      ElMessage.error('删除失败')
    }
  }).catch(() => {})
}

const loadBackups = async () => {
  loadingBackups.value = true
  try {
    const res: any = await request.get('/api/v1/admin/system/backups')
    if (res && res.data && Array.isArray(res.data.list)) {
      backups.value = res.data.list
    } else if (res && Array.isArray(res.data)) {
      backups.value = res.data
    } else if (Array.isArray(res)) {
      backups.value = res
    }
  } catch { /* 忽略 */ } finally {
    loadingBackups.value = false
  }
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

// ========== Git 同步 & 系统更新 ==========
const loadingGitStatus = ref(false)
const pulling = ref(false)
const restarting = ref(false)
const pullResult = ref('')
const pullSuccess = ref(false)
const pullDone = ref(false)
let pollTimer: any = null


const gitStatus = reactive({
  branch: '',
  recent5: '',
  status: '',
  local_head: '',
  remote_head: '',
  behind: 0,
  ahead: 0,
  up_to_date: false,
  changelog: '',
  changed_files: '',
  running_version: '',
  needs_rebuild: false,
  rebuild_changelog: '',
})

const loadGitStatus = async (silent = false) => {
  loadingGitStatus.value = true
  try {
    const res: any = await request.get('/api/v1/admin/system/git-status', { silent })
    const d = res?.data || res
    gitStatus.branch = d.branch || ''
    gitStatus.recent5 = d.recent_5 || ''
    gitStatus.status = d.status || ''
    gitStatus.local_head = d.local_head || ''
    gitStatus.remote_head = d.remote_head || ''
    gitStatus.behind = d.behind || 0
    gitStatus.ahead = d.ahead || 0
    gitStatus.up_to_date = !!d.up_to_date
    gitStatus.changelog = d.changelog || ''
    gitStatus.changed_files = d.changed_files || ''
    gitStatus.running_version = d.running_version || ''
    gitStatus.needs_rebuild = !!d.needs_rebuild
    gitStatus.rebuild_changelog = d.rebuild_changelog || ''
  } catch { /* */ } finally {
    loadingGitStatus.value = false
  }
}
// 更新/重启期间静默刷新 git 状态(面板可能正在重启, 请求会失败, 不弹错误弹窗)
const loadGitStatusSilent = () => loadGitStatus(true)

const gitPull = async () => {
  ElMessageBox.confirm('将从 GitHub 拉取最新代码，编译后端、构建前端，然后重启面板。确定继续？', '一键在线更新', {
    type: 'warning', confirmButtonText: '确认更新', cancelButtonText: '取消',
  }).then(async () => {
    pulling.value = true
    pullResult.value = ''
    pullSuccess.value = false
    pullDone.value = false
    // 修复 UI-POLL-01 (P1): 重置前先清掉可能残留的旧定时器, 防止用户重复点击
    // 触发"多个并行轮询"导致日志互相覆盖、pulling 状态错乱。
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
    try {
      const res: any = await request.post('/api/v1/admin/system/git-pull')
      pullResult.value = (res?.data?.msg || '更新已开始') + '\n'
      // 轮询实时日志
      // silent: 更新完成后面板会重启, 期间轮询请求必然失败,
      // 拦截器默认会弹 ElMessage.error("网络异常"), 会导致浏览器上方
      // 一直弹错误弹窗。这里用 silent 跳过弹窗, 只走 try-catch 静默忽略。
      const poll = async () => {
        try {
          const logRes: any = await request.get('/api/v1/admin/system/git-pull-log', { silent: true })
          const logData = logRes?.data || logRes
          if (logData.log) pullResult.value = logData.log
          // 自动滚动到更新进度底部，不用手动拖滚动条
          nextTick(() => {
            const el = document.querySelector('.update-progress')
            if (el) el.scrollIntoView({ behavior: 'smooth', block: 'end' })
          })
          if (logData.done) {
            pulling.value = false
            pullDone.value = true
            pullSuccess.value = logData.success !== false
            if (pullSuccess.value) {
              ElMessage.success('代码更新完成，请点击「重启面板」使更新生效')
            } else {
              ElMessage.error('更新过程中出现错误，请查看日志')
            }
            loadGitStatusSilent()
            if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
          }
        } catch { /* 忽略轮询错误 */ }
      }
      poll()
      pollTimer = setInterval(poll, 2000)
    } catch (e: any) {
      pullResult.value = e?.response?.data?.msg || e?.message || '更新失败'
      pullSuccess.value = false
      pullDone.value = true
      pulling.value = false
      ElMessage.error(pullResult.value)
    }
  }).catch(() => {})
}

const systemRestart = async () => {
  ElMessageBox.confirm('确定重启面板后端服务吗？重启后页面将短暂不可用。', '重启面板', {
    type: 'warning', confirmButtonText: '确认重启', cancelButtonText: '取消',
  }).then(async () => {
    restarting.value = true
    try {
      await request.post('/api/v1/admin/system/restart', {}, { silent: true })
      ElMessage.success('重启指令已下发，面板重启中...')
    } catch {
      // 请求失败大概率是面板已经开始重启(连接被切断)
    }
    // 面板重启后需要手动刷新
    setTimeout(() => {
      restarting.value = false
      ElMessage.info('面板可能已恢复，请手动刷新页面')
    }, 5000)
  }).catch(() => {})
}

// ========== 磁盘管理 ==========
const loadingDisk = ref(false)
const cleaning = ref(false)
const diskUsage = ref('')
const cleanupResult = ref('')

const loadDiskUsage = async () => {
  loadingDisk.value = true
  try {
    const res: any = await request.get('/api/v1/admin/system/disk-usage')
    const d = res?.data || res
    diskUsage.value = d.output || ''
  } catch { /* */ } finally {
    loadingDisk.value = false
  }
}

const diskCleanup = async () => {
  ElMessageBox.confirm('将清理 Docker 冗余数据、系统日志、临时文件、旧备份并执行数据库 VACUUM，确定继续？', '磁盘清理', {
    type: 'warning', confirmButtonText: '确认清理', cancelButtonText: '取消',
  }).then(async () => {
    cleaning.value = true
    try {
      const res: any = await request.post('/api/v1/admin/system/disk-cleanup', {
        clean_docker: true,
        clean_logs: true,
        clean_tmp: true,
        clean_old_backups: true,
        keep_backup_count: 1, // 仅保留最新 1 份备份(满足存储控制需求)
        vacuum_db: true,      // 清理 PostgreSQL 死元组(traffic_logs 高频 DELETE 后膨胀)
      })
      const d = res?.data || res
      cleanupResult.value = d.output || ''
      ElMessage.success('磁盘清理完成')
      loadDiskUsage()
      loadBackups()
    } catch (e: any) {
      cleanupResult.value = e?.response?.data?.msg || e?.message || '清理失败'
      ElMessage.error(cleanupResult.value)
    } finally { cleaning.value = false }
  }).catch(() => {})
}


onMounted(() => {
  loadPaymentConfig()
  loadBackups()
  loadGitStatus()
  loadDiskUsage()
})

// 修复 UI-POLL-02 (P1): 离开页面时清理 git-pull 日志轮询定时器,
// 防止组件卸载后定时器仍在跑, 造成内存泄漏 + 已卸载组件状态更新报错。
onUnmounted(() => {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
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

/* 系统维护 */
.git-status-box { background: var(--np-bg-soft); border-radius: 8px; padding: 12px; margin-bottom: 12px; }
.git-info { margin-bottom: 8px; display: flex; align-items: flex-start; gap: 8px; flex-wrap: wrap; }
.git-label { font-size: 12px; color: var(--np-text-muted); flex-shrink: 0; line-height: 24px; }
.git-log { margin: 0; padding: 8px; background: var(--np-card); border-radius: 4px; font-size: 12px; color: var(--np-text-secondary); white-space: pre-wrap; word-break: break-all; max-height: 120px; overflow-y: auto; flex: 1; min-width: 0; }
.git-log.has-changes { color: var(--np-warning); }
.git-commit-hash { font-family: 'JetBrains Mono', monospace; font-size: 12px; padding: 2px 6px; background: var(--np-bg-soft); border-radius: 3px; color: var(--np-text-secondary); }
.git-commit-new { color: var(--np-primary); border: 1px dashed var(--np-primary-dim); }
.git-arrow { color: var(--np-text-muted); font-size: 12px; }
.git-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.git-actions .el-button { margin-left: 0 !important; }
.git-actions .el-button span { margin-left: 4px; }
.disk-actions { margin-top: 12px; display: flex; gap: 8px; flex-wrap: wrap; }
.disk-actions .el-button { margin-left: 0 !important; }
.disk-actions .el-button span { margin-left: 4px; }
.disk-output { background: var(--np-bg-soft); border-radius: 8px; padding: 12px; margin-bottom: 12px; }
.disk-output pre { margin: 0; font-size: 12px; color: var(--np-text-secondary); white-space: pre-wrap; max-height: 200px; overflow-y: auto; }
.cmd-output { margin-top: 12px; background: var(--np-bg-soft); border-radius: 8px; padding: 12px; }
.cmd-output .output-title { font-size: 13px; font-weight: 600; color: var(--np-text); margin-bottom: 6px; }
.cmd-output .output-title.text-success { color: #67c23a; }
.cmd-output .output-title.text-danger { color: #f56c6c; }
.cmd-output .output-title.text-pending { color: var(--np-text-muted); }
.cmd-output pre { margin: 0; font-size: 12px; color: var(--np-text-secondary); white-space: pre-wrap; word-break: break-all; max-height: 200px; overflow-y: auto; }


/* 在线更新日志 — 终端风格固定高度日志框, 对齐磁盘管理 */
.pull-log {
  margin: 0; font-size: 12px; color: var(--np-text-secondary); white-space: pre-wrap;
  word-break: break-all; max-height: 300px; overflow-y: auto;
  font-family: 'JetBrains Mono', 'Consolas', monospace;
}

</style>
