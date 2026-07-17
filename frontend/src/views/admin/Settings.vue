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

      <!-- 邮件配置 -->
      <el-tab-pane label="邮件配置" name="email">
        <el-row :gutter="20">
          <el-col :xs="24" :md="14">
            <div class="np-card section-card">
              <div class="section-title"><el-icon><Message /></el-icon> SMTP 邮件配置</div>
              <el-form label-width="120px" label-position="right">
                <el-form-item label="启用邮件">
                  <el-switch v-model="email.enabled" active-text="开启邮件" inactive-text="关闭邮件" />
                  <span class="form-tip">关闭后用户将无法收到验证邮件</span>
                </el-form-item>
                <el-form-item label="SMTP 服务器">
                  <el-input v-model="email.host" placeholder="smtp.example.com" />
                </el-form-item>
                <el-form-item label="SMTP 端口">
                  <el-input-number v-model="email.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
                  <span class="form-tip">Mailtrap 推荐 587(TLS)，QQ/163 用 465(SSL)</span>
                </el-form-item>
                <el-form-item label="SMTP 用户名">
                  <el-input v-model="email.user" placeholder="APIsmtp@mailtrap.io 或 noreply@example.com" />
                  <span class="form-tip">SMTP 登录用户名。Mailtrap 填 APIsmtp@mailtrap.io，QQ邮箱填完整邮箱地址</span>
                </el-form-item>
                <el-form-item label="发件人地址">
                  <el-input v-model="email.from" placeholder="noreply@yourdomain.com" />
                  <span class="form-tip">收件人看到的发件人邮箱，必须是 SMTP 服务商已验证域名的邮箱</span>
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
                  <el-tag v-if="gitStatus.up_to_date" size="small" type="success" effect="dark">已是最新版本</el-tag>
                  <el-tag v-else-if="gitStatus.behind > 0" size="small" type="warning" effect="dark">有 {{ gitStatus.behind }} 个更新可用</el-tag>
                  <el-tag v-else-if="gitStatus.ahead > 0" size="small" type="info" effect="dark">本地有 {{ gitStatus.ahead }} 个未推送提交</el-tag>
                  <el-tag v-else size="small" type="info">检测中...</el-tag>
                </div>
                <div class="git-info">
                  <span class="git-label">当前版本:</span>
                  <code class="git-commit-hash">{{ gitStatus.local_head || '-' }}</code>
                  <span v-if="gitStatus.behind > 0" class="git-arrow">→</span>
                  <code v-if="gitStatus.behind > 0" class="git-commit-hash git-commit-new">{{ gitStatus.remote_head || '-' }}</code>
                </div>
                <div class="git-info">
                  <span class="git-label">最近提交:</span>
                  <pre class="git-log">{{ gitStatus.recent5 || '加载中...' }}</pre>
                </div>
                <div class="git-info">
                  <span class="git-label">变更文件:</span>
                  <pre class="git-log" :class="{ 'has-changes': gitStatus.status }">{{ gitStatus.status || '无变更' }}</pre>
                </div>
              </div>
              <div class="git-actions">
                <el-button type="primary" @click="gitPull" :loading="pulling">
                  <el-icon><Download /></el-icon><span>一键在线更新</span>
                </el-button>
                <el-button type="warning" @click="systemRestart" :loading="restarting">
                  <el-icon><RefreshRight /></el-icon><span>重启面板</span>
                </el-button>
                <el-button type="info" @click="loadGitStatus" :loading="loadingGitStatus">
                  <el-icon><Refresh /></el-icon><span>刷新状态</span>
                </el-button>
              </div>
              <div v-if="pullResult" class="update-progress">
                <div class="progress-header" :class="pullDone ? (pullSuccess ? 'is-success' : 'is-error') : 'is-pending'">
                  <span class="status-dot" :class="{ spinning: !pullDone }"></span>
                  <span class="header-text">{{ pullDone ? (pullSuccess ? '更新成功' : '更新失败') : '更新中...' }}</span>
                  <span v-if="restartingPanel" class="restart-hint">正在重启，稍后自动恢复...</span>
                </div>
                <div class="step-list">
                  <div v-for="(step, idx) in pullSteps" :key="idx" class="step-item" :class="step.status">
                    <div class="step-head">
                      <span class="step-dot" :class="step.status"></span>
                      <span class="step-title">{{ step.title }}</span>
                    </div>
                    <div v-if="step.detail && (step.status === 'running' || step.status === 'error')" class="step-detail">
                      <pre>{{ step.detail }}</pre>
                    </div>
                  </div>
                </div>
                <details v-if="pullResult" class="raw-log">
                  <summary>查看完整日志</summary>
                  <pre>{{ pullResult }}</pre>
                </details>
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
import { ref, reactive, onMounted, onUnmounted, computed } from 'vue'
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

// ========== Git 同步 & 系统更新 ==========
const loadingGitStatus = ref(false)
const pulling = ref(false)
const restarting = ref(false)
const pullResult = ref('')
const pullSuccess = ref(false)
const pullDone = ref(false)
const restartingPanel = ref(false)
let pollTimer: any = null

// 解析更新日志的 >>> N/M 步骤标记, 按步骤一条条展示
const pullSteps = computed(() => {
  if (!pullResult.value) return []
  const lines = pullResult.value.split('\n')
  const steps: Array<{ title: string; detail: string; status: string }> = []
  let current: { title: string; detail: string; status: string } | null = null
  for (const line of lines) {
    const m1 = line.match(/^>>>\s*\d+\/\d+\s+(.+)/)
    const m2 = line.match(/^>>>\s+(.+)/)
    if (m1 || m2) {
      const title = m1 ? m1[1] : (m2 as RegExpMatchArray)[1]
      if (current) steps.push(current)
      current = { title, detail: '', status: 'pending' }
    } else if (current) {
      if (line.trim()) current.detail += line + '\n'
    }
  }
  if (current) steps.push(current)
  // 最后一步之前的都已完成, 最后一步根据 pullDone 判断
  for (let i = 0; i < steps.length; i++) {
    if (i < steps.length - 1) {
      steps[i].status = 'done'
    } else if (pullDone.value) {
      steps[i].status = pullSuccess.value ? 'done' : 'error'
    } else {
      steps[i].status = 'running'
    }
  }
  return steps
})

// 轮询面板健康, 恢复后自动刷新页面(用于更新/重启后面板短暂不可用的场景)
// 修复 UI-RELOAD-01 (P1): 旧版用"先等断开再等恢复"两阶段, 但后端 syscall.Exec
// 原地重启的 HTTP 断开窗口可能 <2s, 前端 2s 轮询容易错过断开瞬间, 导致永远
// 卡在"等待断开"阶段不刷新(用户看到"更新中"一直转)。
// 新方案: 记录调用前的 boot_time, 轮询 /healthz 对比 boot_time 是否变化,
// 变化 = 新进程已起来 = 重启完成, 直接刷新。同时保留"请求失败"作为断开信号。
const waitForPanelAndReload = () => {
  const oldBootTime = currentBootTime.value // 调用前缓存的面板启动时间
  let attempts = 0
  let sawDisconnect = false // 是否观察到过面板断开(用于区分"还没重启"和"已快速重启完")
  const check = setInterval(async () => {
    attempts++
    try {
      const res: any = await request.get('/healthz', { timeout: 3000 })
      const newBootTime = res?.data?.boot_time || res?.boot_time
      // boot_time 变化 = 新进程已起来, 重启完成
      if (newBootTime && oldBootTime && newBootTime !== oldBootTime) {
        clearInterval(check)
        ElMessage.success('面板已恢复，刷新中...')
        setTimeout(() => location.reload(), 500)
        return
      }
      // boot_time 没变但曾断开过 → 面板已恢复(只是 boot_time 没更新, 兼容旧版)
      if (sawDisconnect) {
        clearInterval(check)
        ElMessage.success('面板已恢复，刷新中...')
        setTimeout(() => location.reload(), 500)
        return
      }
      // boot_time 没变也没断开过 → 面板可能还没开始重启, 继续等
      // 但如果 oldBootTime 为空(页面加载时获取失败), 直接靠 sawDisconnect 判断
      if (!oldBootTime && attempts > 3) {
        // 没有基线 boot_time, 等 3 次后若面板一直活着, 视为已恢复
        clearInterval(check)
        ElMessage.success('面板已恢复，刷新中...')
        setTimeout(() => location.reload(), 500)
      }
    } catch {
      // 请求失败 = 面板正在重启(断开中), 标记已观察到断开
      sawDisconnect = true
    }
    // 超时: 90 秒(45 次 × 2s), 覆盖 docker 重建等慢场景
    if (attempts > 45) {
      clearInterval(check)
      ElMessage.warning('面板恢复超时，请手动刷新')
      restartingPanel.value = false
      restarting.value = false
    }
  }, 2000)
}
const gitStatus = reactive({
  branch: '',
  recent5: '',
  status: '',
  local_head: '',
  remote_head: '',
  behind: 0,
  ahead: 0,
  up_to_date: false,
})

const loadGitStatus = async () => {
  loadingGitStatus.value = true
  try {
    const res: any = await request.get('/api/v1/admin/system/git-status')
    const d = res?.data || res
    gitStatus.branch = d.branch || ''
    gitStatus.recent5 = d.recent_5 || ''
    gitStatus.status = d.status || ''
    gitStatus.local_head = d.local_head || ''
    gitStatus.remote_head = d.remote_head || ''
    gitStatus.behind = d.behind || 0
    gitStatus.ahead = d.ahead || 0
    gitStatus.up_to_date = !!d.up_to_date
  } catch { /* */ } finally {
    loadingGitStatus.value = false
  }
}

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
      const poll = async () => {
        try {
          const logRes: any = await request.get('/api/v1/admin/system/git-pull-log')
          const logData = logRes?.data || logRes
          if (logData.log) pullResult.value = logData.log
          if (logData.done) {
            pulling.value = false
            pullDone.value = true
            pullSuccess.value = logData.success !== false
            if (pullSuccess.value) {
              ElMessage.success('更新完成，面板重启中...')
              restartingPanel.value = true
              waitForPanelAndReload()
            } else {
              ElMessage.error('更新过程中出现错误，请查看日志')
            }
            loadGitStatus()
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
    restartingPanel.value = true
    try {
      await request.post('/api/v1/admin/system/restart')
      ElMessage.success('重启指令已下发，面板重启中...')
      // 注意: 不在 finally 里清 restarting, 保持按钮 loading 直到面板恢复
      // (面板恢复后 waitForPanelAndReload 会刷新页面, loading 自然消失)
      waitForPanelAndReload()
    } catch {
      restarting.value = false
      restartingPanel.value = false
    }
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

// 修复 UI-RELOAD-01 (P1): 缓存当前面板启动时间, 用于一键更新/重启后判断面板是否已重启
const currentBootTime = ref<number | string>('')
const loadBootTime = async () => {
  try {
    const res: any = await request.get('/healthz', { timeout: 3000 })
    currentBootTime.value = res?.data?.boot_time || res?.boot_time || ''
  } catch { /* 面板不可用时忽略 */ }
}

onMounted(() => {
  loadPaymentConfig()
  loadEmailConfig()
  loadBackups()
  loadGitStatus()
  loadDiskUsage()
  loadBootTime()
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

/* 更新进度步骤列表 */
.update-progress { margin-top: 12px; background: var(--np-bg-soft); border-radius: 8px; padding: 12px; }
.progress-header { display: flex; align-items: center; gap: 8px; font-size: 14px; font-weight: 600; margin-bottom: 12px; flex-wrap: wrap; }
.progress-header.is-success { color: #67c23a; }
.progress-header.is-error { color: #f56c6c; }
.progress-header.is-pending { color: var(--np-text-muted); }
.status-dot { width: 10px; height: 10px; border-radius: 50%; display: inline-block; flex-shrink: 0; }
.is-pending .status-dot { background: var(--np-text-muted); opacity: 0.4; }
.is-success .status-dot { background: #67c23a; }
.is-error .status-dot { background: #f56c6c; }
.status-dot.spinning { background: #409eff; animation: np-spin 1s linear infinite; }
@keyframes np-spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
.restart-hint { font-size: 12px; color: var(--np-text-muted); font-weight: normal; word-break: break-all; }
.step-list { display: flex; flex-direction: column; gap: 6px; }
.step-item { padding: 2px 0; }
.step-head { display: flex; align-items: center; gap: 8px; font-size: 13px; }
.step-dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; flex-shrink: 0; }
.step-dot.pending { background: var(--np-text-muted); opacity: 0.4; }
.step-dot.done { background: #67c23a; }
.step-dot.error { background: #f56c6c; }
.step-dot.running { background: #409eff; animation: np-spin 1s linear infinite; }
.step-title { color: var(--np-text); }
.step-item.done .step-title { color: var(--np-text-secondary); }
.step-item.running .step-title { color: #409eff; font-weight: 500; }
.step-item.error .step-title { color: #f56c6c; }
.step-detail { margin: 4px 0 4px 16px; }
.step-detail pre { margin: 0; font-size: 12px; color: var(--np-text-muted); white-space: pre-wrap; word-break: break-all; max-height: 120px; overflow-y: auto; background: var(--np-card); padding: 6px 8px; border-radius: 4px; }
.raw-log { margin-top: 12px; }
.raw-log summary { cursor: pointer; font-size: 12px; color: var(--np-text-muted); }
.raw-log pre { margin: 8px 0 0; font-size: 12px; color: var(--np-text-muted); white-space: pre-wrap; word-break: break-all; max-height: 200px; overflow-y: auto; }
</style>
