<template>
  <div class="user-referral">
    <!-- 顶部邀请横幅 -->
    <div class="invite-hero">
      <div class="hero-bg">
        <div class="blob blob-1"></div>
        <div class="blob blob-2"></div>
        <div class="blob blob-3"></div>
      </div>
      <div class="hero-content">
        <div class="hero-left">
          <div class="hero-badge">
            <el-icon><Present /></el-icon>
            <span>邀请好友赚返利</span>
          </div>
          <h2 class="hero-title">分享你的专属邀请码</h2>
          <p class="hero-desc">
            好友首次购买套餐后，你将获得订单金额
            <strong class="rate">{{ rewardRateText }}</strong> 的现金返利
          </p>
          <div class="hero-actions">
            <el-button size="large" type="primary" @click="copyCode">
              <el-icon><CopyDocument /></el-icon>
              复制邀请码
            </el-button>
            <el-button size="large" @click="showPoster = true" plain>
              <el-icon><Picture /></el-icon>
              生成海报
            </el-button>
          </div>
        </div>
        <div class="hero-right">
          <div class="invite-code-card">
            <div class="code-label">我的邀请码</div>
            <div class="code-value">{{ inviteCode || '---- ----' }}</div>
            <div class="qr-wrap" v-if="qrDataUrl">
              <div class="qr-inner">
                <img :src="qrDataUrl" alt="邀请二维码" />
              </div>
              <div class="qr-tip">扫码注册 · 享好友福利</div>
            </div>
            <div class="share-link" v-if="shareUrl" @click="copyShareUrl">
              <el-icon><Link /></el-icon>
              <span class="link-text">{{ shareUrl }}</span>
              <el-icon class="copy-icon"><CopyDocument /></el-icon>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 数据统计卡片 -->
    <el-row :gutter="16" class="stats-row">
      <el-col :xs="12" :sm="6" v-for="(item, i) in statItems" :key="i">
        <div class="stat-card" :class="item.type">
          <div class="stat-icon">
            <el-icon><component :is="item.icon" /></el-icon>
          </div>
          <div class="stat-info">
            <div class="stat-num">{{ item.value }}</div>
            <div class="stat-label">{{ item.label }}</div>
          </div>
        </div>
      </el-col>
    </el-row>

    <!-- 明细列表 -->
    <div class="np-card list-card">
      <el-tabs v-model="activeTab" class="referral-tabs">
        <!-- 邀请记录 -->
        <el-tab-pane label="邀请记录" name="invitations">
          <div class="list-header">
            <span class="list-count">共 {{ inviteTotal }} 位好友</span>
            <el-button size="small" @click="loadInvitations" :loading="loadingInvites">
              <el-icon><Refresh /></el-icon>刷新
            </el-button>
          </div>
          <div class="invite-list">
            <div
              v-for="item in invitations"
              :key="item.id"
              class="invite-item"
            >
              <div class="invite-avatar">
                {{ getInitial(item.invitee_id) }}
              </div>
              <div class="invite-info">
                <div class="invite-name">用户 {{ maskName(item.invitee_id) }}</div>
                <div class="invite-time">{{ formatTime(item.created_at) }}</div>
              </div>
              <div class="invite-status">
                <el-tag :type="inviteStatusType(item.status)" effect="dark" size="small" round>
                  {{ inviteStatusText(item.status) }}
                </el-tag>
              </div>
              <div class="invite-reward">
                <span v-if="item.reward_cents > 0" class="reward-amount">
                  +¥{{ (item.reward_cents / 100).toFixed(2) }}
                </span>
                <span v-else class="reward-pending">待返利</span>
              </div>
            </div>
            <el-empty
              v-if="!loadingInvites && !invitations.length"
              description="还没有邀请记录"
              :image-size="60"
            />
          </div>
          <div class="pagination-wrap" v-if="inviteTotal > inviteSize">
            <el-pagination
              v-model:current-page="invitePage"
              :page-size="inviteSize"
              :total="inviteTotal"
              layout="prev, pager, next"
              @current-change="loadInvitations"
            />
          </div>
        </el-tab-pane>

        <!-- 返利明细 -->
        <el-tab-pane label="返利明细" name="rewards">
          <div class="list-header">
            <span class="list-count">累计返利 ¥{{ totalRewardText }}</span>
            <el-button size="small" @click="loadRewards" :loading="loadingRewards">
              <el-icon><Refresh /></el-icon>刷新
            </el-button>
          </div>
          <div class="reward-list">
            <div
              v-for="item in rewards"
              :key="item.id"
              class="reward-item"
            >
              <div class="reward-icon">
                <el-icon><Wallet /></el-icon>
              </div>
              <div class="reward-info">
                <div class="reward-title">{{ item.description || '邀请返利' }}</div>
                <div class="reward-meta">
                  <span>订单 ¥{{ (item.order_amount_cents / 100).toFixed(2) }}</span>
                  <span class="dot">·</span>
                  <span>{{ (item.reward_rate * 100).toFixed(0) }}%</span>
                  <span class="dot">·</span>
                  <span>{{ formatTime(item.created_at) }}</span>
                </div>
              </div>
              <div class="reward-amount plus">
                +¥{{ (item.amount_cents / 100).toFixed(2) }}
              </div>
            </div>
            <el-empty
              v-if="!loadingRewards && !rewards.length"
              description="暂无返利记录"
              :image-size="60"
            />
          </div>
          <div class="pagination-wrap" v-if="rewardTotal > rewardSize">
            <el-pagination
              v-model:current-page="rewardPage"
              :page-size="rewardSize"
              :total="rewardTotal"
              layout="prev, pager, next"
              @current-change="loadRewards"
            />
          </div>
        </el-tab-pane>

        <!-- 绑定邀请码 -->
        <el-tab-pane label="绑定邀请人" name="bind">
          <div class="bind-section">
            <template v-if="!binded">
              <div class="bind-icon">
                <el-icon><UserFilled /></el-icon>
              </div>
              <h3 class="bind-title">输入好友邀请码</h3>
              <p class="bind-desc">绑定后即可确认邀请关系，仅可绑定一次</p>
              <div class="bind-form">
                <el-input
                  v-model="bindCode"
                  placeholder="请输入邀请码"
                  size="large"
                  class="bind-input"
                  :disabled="binding"
                  @keyup.enter="bindInvite"
                >
                  <template #append>
                    <el-button type="primary" :loading="binding" @click="bindInvite">
                      绑定
                    </el-button>
                  </template>
                </el-input>
              </div>
              <div class="bind-tip">
                <el-icon><InfoFilled /></el-icon>
                <span>每人只能被邀请一次，绑定后不可更改</span>
              </div>
            </template>
            <template v-else>
              <div class="bind-icon success">
                <el-icon><CircleCheck /></el-icon>
              </div>
              <h3 class="bind-title">已绑定邀请关系</h3>
              <p class="bind-desc">感谢你加入我们的大家庭 🎉</p>
            </template>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>

    <!-- 海报弹窗 -->
    <el-dialog
      v-model="showPoster"
      title="分享海报"
      width="380px"
      :show-close="true"
      align-center
    >
      <div class="poster-wrap">
        <div class="poster" ref="posterRef">
          <div class="poster-bg">
            <div class="poster-blob blob-a"></div>
            <div class="poster-blob blob-b"></div>
          </div>
          <div class="poster-content">
            <div class="poster-header">
              <span class="poster-logo">◆ NEXUS</span>
              <span class="poster-tag">邀请好友</span>
            </div>
            <h3 class="poster-title">
              邀请好友<br />一起享受高速网络
            </h3>
            <p class="poster-sub">注册即享专属福利</p>
            <div class="poster-qr">
              <img :src="qrDataUrl" v-if="qrDataUrl" alt="二维码" />
            </div>
            <div class="poster-code">
              <span class="pc-label">我的邀请码</span>
              <span class="pc-value">{{ inviteCode || '--------' }}</span>
            </div>
            <div class="poster-footer">
              <span>长按识别 · 立即加入</span>
            </div>
          </div>
        </div>
      </div>
      <template #footer>
        <el-button @click="showPoster = false">关闭</el-button>
        <el-button type="primary" :loading="posterSaving" @click="savePoster">保存海报</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage } from 'element-plus'
import {
  Present, CopyDocument, Picture, Link, Refresh,
  Wallet, UserFilled, CircleCheck, InfoFilled,
  User, Trophy, Money, Clock,
} from '@element-plus/icons-vue'
import QRCode from 'qrcode'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

// ===== 类型定义 =====
interface ReferralStats {
  total_invited: number
  completed_count: number
  total_reward: number
}
interface Invitation {
  id: string
  inviter_id: string
  invitee_id: string
  order_id?: string
  reward_cents: number
  status: 'pending' | 'completed' | 'expired'
  reward_at?: string
  created_at: string
}
interface Reward {
  id: string
  user_id: string
  order_id: string
  invitee_id: string
  amount_cents: number
  order_amount_cents: number
  reward_rate: number
  description: string
  created_at: string
}

// ===== 邀请码 & 分享 =====
const loading = ref(false)
const inviteCode = ref('')
const shareUrl = ref('')
const qrDataUrl = ref('')
const rewardRateText = ref('10%')
const showPoster = ref(false)
const posterRef = ref<HTMLElement | null>(null)

const buildShareUrl = (code: string) => {
  const origin = window.location.origin
  return `${origin}/register?ref=${code}`
}

const loadInviteCode = async () => {
  loading.value = true
  try {
    const res: any = await request.get('/api/v1/user/referral/invite-code')
    const data = res?.data || res
    inviteCode.value = data?.invite_code || ''
    if (inviteCode.value) {
      shareUrl.value = buildShareUrl(inviteCode.value)
      try {
        qrDataUrl.value = await QRCode.toDataURL(shareUrl.value, {
          width: 200,
          margin: 1,
          color: { dark: '#000000', light: '#ffffff' },
        })
      } catch {
        qrDataUrl.value = ''
      }
    }
  } catch {
    inviteCode.value = ''
  } finally {
    loading.value = false
  }
}

const copyCode = async () => {
  if (!inviteCode.value) {
    ElMessage.warning('邀请码生成中，请稍候')
    return
  }
  try {
    await navigator.clipboard.writeText(inviteCode.value)
    ElMessage.success('邀请码已复制')
  } catch {
    ElMessage.error('复制失败，请手动复制')
  }
}

const copyShareUrl = async () => {
  if (!shareUrl.value) return
  try {
    await navigator.clipboard.writeText(shareUrl.value)
    ElMessage.success('分享链接已复制')
  } catch {
    ElMessage.error('复制失败，请手动复制')
  }
}

// ===== 统计数据 =====
const stats = ref<ReferralStats>({ total_invited: 0, completed_count: 0, total_reward: 0 })

const totalRewardText = computed(() =>
  (stats.value.total_reward / 100).toFixed(2),
)
const pendingCount = computed(() =>
  Math.max(0, stats.value.total_invited - stats.value.completed_count),
)

const statItems = computed(() => [
  { label: '邀请人数', value: stats.value.total_invited, icon: User, type: 'blue' },
  { label: '已完成返利', value: stats.value.completed_count, icon: Trophy, type: 'green' },
  { label: '累计返利', value: `¥${totalRewardText.value}`, icon: Money, type: 'gold' },
  { label: '待返利', value: pendingCount.value, icon: Clock, type: 'orange' },
])

const loadStats = async () => {
  try {
    const res: any = await request.get('/api/v1/user/referral/stats')
    const data = res?.data || res
    stats.value = {
      total_invited: data?.total_invited || 0,
      completed_count: data?.completed_count || 0,
      total_reward: data?.total_reward || 0,
    }
  } catch { /* ignore */ }
}

// ===== 邀请列表 =====
const activeTab = ref<'invitations' | 'rewards' | 'bind'>('invitations')
const invitations = ref<Invitation[]>([])
const invitePage = ref(1)
const inviteSize = 10
const inviteTotal = ref(0)
const loadingInvites = ref(false)

const loadInvitations = async () => {
  loadingInvites.value = true
  try {
    const res: any = await request.get('/api/v1/user/referral/invitations', {
      params: { page: invitePage.value, size: inviteSize },
    })
    const data = res?.data || res
    invitations.value = Array.isArray(data?.list) ? data.list : []
    inviteTotal.value = data?.total || 0
  } catch {
    invitations.value = []
  } finally {
    loadingInvites.value = false
  }
}

// ===== 返利记录 =====
const rewards = ref<Reward[]>([])
const rewardPage = ref(1)
const rewardSize = 10
const rewardTotal = ref(0)
const loadingRewards = ref(false)

const loadRewards = async () => {
  loadingRewards.value = true
  try {
    const res: any = await request.get('/api/v1/user/referral/rewards', {
      params: { page: rewardPage.value, size: rewardSize },
    })
    const data = res?.data || res
    rewards.value = Array.isArray(data?.list) ? data.list : []
    rewardTotal.value = data?.total || 0
  } catch {
    rewards.value = []
  } finally {
    loadingRewards.value = false
  }
}

// ===== 绑定邀请码 =====
const binded = ref(false)
const bindCode = ref('')
const binding = ref(false)

const bindInvite = async () => {
  if (!bindCode.value.trim()) {
    ElMessage.warning('请输入邀请码')
    return
  }
  binding.value = true
  try {
    await request.post('/api/v1/user/referral/bind', {
      invite_code: bindCode.value.trim().toUpperCase(),
    })
    ElMessage.success('邀请码绑定成功')
    binded.value = true
    bindCode.value = ''
  } catch { /* 错误提示由拦截器统一处理 */ }
  finally {
    binding.value = false
  }
}

// ===== 工具方法 =====
type TagType = 'primary' | 'success' | 'info' | 'warning' | 'danger'
const inviteStatusText = (s: string): string => {
  const map: Record<string, string> = {
    pending: '待返利', completed: '已返利', expired: '已失效',
  }
  return map[s] || s
}
const inviteStatusType = (s: string): TagType => {
  const map: Record<string, TagType> = {
    pending: 'warning', completed: 'success', expired: 'info',
  }
  return map[s] || 'info'
}

const maskName = (id: string): string => {
  if (!id || id.length < 8) return id || '-'
  return id.slice(0, 4) + '****'
}

const getInitial = (id: string): string => {
  if (!id) return 'U'
  return id.charAt(0).toUpperCase()
}

// ===== 海报保存 (Canvas 绘制) =====
const posterSaving = ref(false)

// 将 dataURL 转为 Image 对象
const loadImage = (src: string): Promise<HTMLImageElement> => {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.crossOrigin = 'anonymous'
    img.onload = () => resolve(img)
    img.onerror = reject
    img.src = src
  })
}

// 绘制圆形光斑(模拟 blob 效果)
const drawBlob = (
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  r: number,
  color: string,
  alpha: number,
) => {
  const grad = ctx.createRadialGradient(x, y, 0, x, y, r)
  grad.addColorStop(0, color)
  grad.addColorStop(1, 'rgba(0,0,0,0)')
  ctx.globalAlpha = alpha
  ctx.fillStyle = grad
  ctx.beginPath()
  ctx.arc(x, y, r, 0, Math.PI * 2)
  ctx.fill()
  ctx.globalAlpha = 1
}

// 绘制圆角矩形
const roundRect = (
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  r: number,
) => {
  ctx.beginPath()
  ctx.moveTo(x + r, y)
  ctx.lineTo(x + w - r, y)
  ctx.quadraticCurveTo(x + w, y, x + w, y + r)
  ctx.lineTo(x + w, y + h - r)
  ctx.quadraticCurveTo(x + w, y + h, x + w - r, y + h)
  ctx.lineTo(x + r, y + h)
  ctx.quadraticCurveTo(x, y + h, x, y + h - r)
  ctx.lineTo(x, y + r)
  ctx.quadraticCurveTo(x, y, x + r, y)
  ctx.closePath()
}

const savePoster = async () => {
  if (!inviteCode.value || !qrDataUrl.value) {
    ElMessage.warning('邀请码生成中，请稍候')
    return
  }
  posterSaving.value = true
  try {
    const W = 750
    const H = 1200
    const canvas = document.createElement('canvas')
    canvas.width = W
    canvas.height = H
    const ctx = canvas.getContext('2d')!

    // 1. 渐变背景
    const bgGrad = ctx.createLinearGradient(0, 0, W, H)
    bgGrad.addColorStop(0, '#667eea')
    bgGrad.addColorStop(1, '#764ba2')
    ctx.fillStyle = bgGrad
    ctx.fillRect(0, 0, W, H)

    // 2. 光斑装饰
    drawBlob(ctx, 600, 80, 180, '#f093fb', 0.35)
    drawBlob(ctx, 100, 900, 200, '#4facfe', 0.3)
    drawBlob(ctx, 400, 500, 140, '#00f2fe', 0.2)

    // 3. 顶部 Logo
    ctx.fillStyle = 'rgba(255,255,255,0.95)'
    ctx.font = 'bold 36px system-ui, -apple-system, sans-serif'
    ctx.textBaseline = 'top'
    ctx.fillText('◆ NEXUS', 50, 60)

    // 标签
    ctx.fillStyle = 'rgba(255,255,255,0.25)'
    roundRect(ctx, W - 180, 62, 130, 40, 20)
    ctx.fill()
    ctx.fillStyle = '#fff'
    ctx.font = '20px system-ui, -apple-system, sans-serif'
    ctx.textAlign = 'center'
    ctx.fillText('邀请好友', W - 115, 70)
    ctx.textAlign = 'left'

    // 4. 标题
    ctx.fillStyle = '#fff'
    ctx.font = 'bold 56px system-ui, -apple-system, sans-serif'
    ctx.fillText('邀请好友', 50, 220)
    ctx.fillText('一起享受高速网络', 50, 290)

    // 副标题
    ctx.fillStyle = 'rgba(255,255,255,0.85)'
    ctx.font = '26px system-ui, -apple-system, sans-serif'
    ctx.fillText('注册即享专属福利', 50, 380)

    // 5. 白色卡片区 (二维码 + 邀请码)
    const cardX = 75
    const cardY = 480
    const cardW = W - 150
    const cardH = 580
    ctx.fillStyle = 'rgba(255,255,255,0.97)'
    roundRect(ctx, cardX, cardY, cardW, cardH, 24)
    ctx.fill()

    // 卡片内标题
    ctx.fillStyle = '#909399'
    ctx.font = '22px system-ui, -apple-system, sans-serif'
    ctx.textAlign = 'center'
    ctx.fillText('扫码注册 · 立即加入', W / 2, cardY + 40)

    // 二维码底框
    const qrSize = 300
    const qrX = (W - qrSize) / 2
    const qrY = cardY + 90
    ctx.fillStyle = '#fff'
    roundRect(ctx, qrX - 10, qrY - 10, qrSize + 20, qrSize + 20, 12)
    ctx.fill()
    ctx.strokeStyle = '#ebeef5'
    ctx.lineWidth = 1
    roundRect(ctx, qrX - 10, qrY - 10, qrSize + 20, qrSize + 20, 12)
    ctx.stroke()

    // 绘制二维码
    const qrImg = await loadImage(qrDataUrl.value)
    ctx.drawImage(qrImg, qrX, qrY, qrSize, qrSize)

    // 邀请码
    ctx.fillStyle = '#909399'
    ctx.font = '22px system-ui, -apple-system, sans-serif'
    ctx.fillText('我的邀请码', W / 2, cardY + 430)

    ctx.fillStyle = '#667eea'
    ctx.font = 'bold 48px Courier New, monospace'
    ctx.letterSpacing = '4px'
    ctx.fillText(inviteCode.value, W / 2, cardY + 475)
    ctx.letterSpacing = '0px'

    // 分割线
    ctx.strokeStyle = '#ebeef5'
    ctx.lineWidth = 1
    ctx.beginPath()
    ctx.moveTo(cardX + 40, cardY + 540)
    ctx.lineTo(cardX + cardW - 40, cardY + 540)
    ctx.stroke()

    // 底部提示
    ctx.fillStyle = '#606266'
    ctx.font = '22px system-ui, -apple-system, sans-serif'
    ctx.fillText('长按识别二维码 · 立即加入', W / 2, cardY + 552)
    ctx.textAlign = 'left'

    // 6. 底部品牌
    ctx.fillStyle = 'rgba(255,255,255,0.6)'
    ctx.font = '20px system-ui, -apple-system, sans-serif'
    ctx.textAlign = 'center'
    ctx.fillText('NEXUS · 高速稳定的网络服务', W / 2, H - 80)
    ctx.textAlign = 'left'

    // 7. 导出并下载
    const dataUrl = canvas.toDataURL('image/png', 0.95)
    const link = document.createElement('a')
    link.download = `NEXUS邀请海报_${inviteCode.value}.png`
    link.href = dataUrl
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)

    ElMessage.success('海报已保存到本地')
  } catch (err) {
    ElMessage.error('海报生成失败，请重试')
  } finally {
    posterSaving.value = false
  }
}

// tab 切换时懒加载
watch(activeTab, (v) => {
  if (v === 'rewards' && !rewards.value.length && !loadingRewards.value) {
    loadRewards()
  }
})

onMounted(async () => {
  await loadInviteCode()
  loadStats()
  loadInvitations()
})
</script>

<style scoped>
.user-referral {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

/* ===== 邀请横幅 ===== */
.invite-hero {
  position: relative;
  border-radius: 16px;
  overflow: hidden;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  min-height: 240px;
}
.hero-bg {
  position: absolute;
  inset: 0;
  overflow: hidden;
}
.blob {
  position: absolute;
  border-radius: 50%;
  filter: blur(60px);
  opacity: 0.4;
}
.blob-1 {
  width: 200px;
  height: 200px;
  background: #f093fb;
  top: -50px;
  right: 10%;
  animation: float 8s ease-in-out infinite;
}
.blob-2 {
  width: 180px;
  height: 180px;
  background: #4facfe;
  bottom: -40px;
  left: 5%;
  animation: float 10s ease-in-out infinite reverse;
}
.blob-3 {
  width: 120px;
  height: 120px;
  background: #00f2fe;
  top: 50%;
  left: 50%;
  animation: float 6s ease-in-out infinite;
}
@keyframes float {
  0%, 100% { transform: translate(0, 0); }
  50% { transform: translate(20px, -20px); }
}

.hero-content {
  position: relative;
  z-index: 1;
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 40px;
  gap: 30px;
  flex-wrap: wrap;
}
.hero-left {
  flex: 1;
  min-width: 280px;
  color: #fff;
}
.hero-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  background: rgba(255, 255, 255, 0.2);
  backdrop-filter: blur(10px);
  border-radius: 20px;
  font-size: 13px;
  margin-bottom: 16px;
}
.hero-title {
  margin: 0 0 12px;
  font-size: 28px;
  font-weight: 700;
  line-height: 1.3;
}
.hero-desc {
  margin: 0 0 24px;
  font-size: 14px;
  opacity: 0.9;
  line-height: 1.6;
}
.hero-desc .rate {
  color: #ffd700;
  font-size: 18px;
}
.hero-actions {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}
.hero-actions :deep(.el-button--large) {
  border-radius: 24px;
  padding-left: 24px;
  padding-right: 24px;
}
.hero-actions :deep(.el-button--primary) {
  background: #fff;
  color: #667eea;
  border-color: #fff;
}
.hero-actions :deep(.el-button--primary:hover) {
  background: rgba(255, 255, 255, 0.9);
  color: #667eea;
}
.hero-actions :deep(.el-button) {
  border-color: rgba(255, 255, 255, 0.5);
  color: #fff;
}
.hero-actions :deep(.el-button:hover) {
  background: rgba(255, 255, 255, 0.1);
  border-color: #fff;
  color: #fff;
}

/* 邀请码卡片 */
.hero-right {
  flex-shrink: 0;
}
.invite-code-card {
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(20px);
  border-radius: 16px;
  padding: 24px;
  width: 240px;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.2);
  text-align: center;
}
.code-label {
  font-size: 12px;
  color: #8b98a9;
  margin-bottom: 6px;
}
.code-value {
  font-size: 24px;
  font-weight: 800;
  letter-spacing: 3px;
  color: #667eea;
  font-family: 'Courier New', monospace;
  margin-bottom: 16px;
}
.qr-wrap {
  margin-bottom: 16px;
}
.qr-inner {
  display: inline-block;
  padding: 8px;
  background: #fff;
  border-radius: 10px;
  border: 1px solid #eef1f6;
}
.qr-inner img {
  width: 120px;
  height: 120px;
  display: block;
}
.qr-tip {
  margin-top: 8px;
  font-size: 11px;
  color: #8b98a9;
}
.share-link {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 8px 12px;
  background: #f5f7fa;
  border-radius: 8px;
  font-size: 12px;
  color: #606266;
  cursor: pointer;
  transition: all 0.2s;
  overflow: hidden;
}
.share-link:hover {
  background: #e9ecef;
}
.link-text {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 140px;
}
.copy-icon {
  color: #667eea;
  flex-shrink: 0;
}

/* ===== 统计卡片 ===== */
.stats-row {
  margin: 0;
}
.stat-card {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 20px;
  border-radius: 12px;
  background: var(--np-card-bg);
  border: 1px solid var(--np-border);
  transition: all 0.3s ease;
}
.stat-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.08);
}
.stat-icon {
  width: 48px;
  height: 48px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 22px;
  flex-shrink: 0;
}
.stat-card.blue .stat-icon {
  background: rgba(64, 158, 255, 0.1);
  color: #409eff;
}
.stat-card.green .stat-icon {
  background: rgba(103, 194, 58, 0.1);
  color: #67c23a;
}
.stat-card.gold .stat-icon {
  background: rgba(230, 162, 60, 0.1);
  color: #e6a23c;
}
.stat-card.orange .stat-icon {
  background: rgba(245, 108, 108, 0.1);
  color: #f56c6c;
}
.stat-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.stat-num {
  font-size: 22px;
  font-weight: 700;
  color: var(--np-text);
  line-height: 1.2;
}
.stat-card.gold .stat-num {
  color: #e6a23c;
}
.stat-label {
  font-size: 12px;
  color: var(--np-text-secondary);
}

/* ===== 列表卡片 ===== */
.list-card {
  padding: 20px;
}
.referral-tabs :deep(.el-tabs__header) {
  margin-bottom: 16px;
}
.list-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}
.list-count {
  font-size: 13px;
  color: var(--np-text-secondary);
}

/* 邀请列表样式 */
.invite-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.invite-item {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px 16px;
  border-radius: 10px;
  background: var(--np-card-hover);
  transition: all 0.2s;
}
.invite-item:hover {
  background: var(--np-primary-dim);
}
.invite-avatar {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: linear-gradient(135deg, #667eea, #764ba2);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
  font-size: 16px;
  flex-shrink: 0;
}
.invite-info {
  flex: 1;
  min-width: 0;
}
.invite-name {
  font-size: 14px;
  color: var(--np-text);
  font-weight: 500;
}
.invite-time {
  font-size: 12px;
  color: var(--np-text-muted);
  margin-top: 2px;
}
.invite-status {
  flex-shrink: 0;
}
.invite-reward {
  width: 90px;
  text-align: right;
  flex-shrink: 0;
}
.reward-amount {
  font-size: 15px;
  font-weight: 600;
  color: #67c23a;
}
.reward-pending {
  font-size: 13px;
  color: var(--np-text-muted);
}

/* 返利列表样式 */
.reward-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.reward-item {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px 16px;
  border-radius: 10px;
  background: var(--np-card-hover);
  transition: all 0.2s;
}
.reward-item:hover {
  background: var(--np-primary-dim);
}
.reward-icon {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: rgba(103, 194, 58, 0.1);
  color: #67c23a;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 18px;
  flex-shrink: 0;
}
.reward-info {
  flex: 1;
  min-width: 0;
}
.reward-title {
  font-size: 14px;
  color: var(--np-text);
  font-weight: 500;
}
.reward-meta {
  font-size: 12px;
  color: var(--np-text-muted);
  margin-top: 2px;
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
}
.reward-meta .dot {
  color: var(--np-border);
}
.reward-amount.plus {
  font-size: 16px;
  font-weight: 700;
  color: #67c23a;
  flex-shrink: 0;
}

/* 分页 */
.pagination-wrap {
  display: flex;
  justify-content: center;
  margin-top: 20px;
}

/* ===== 绑定邀请码 ===== */
.bind-section {
  padding: 40px 20px;
  text-align: center;
}
.bind-icon {
  width: 64px;
  height: 64px;
  margin: 0 auto 20px;
  border-radius: 50%;
  background: rgba(64, 158, 255, 0.1);
  color: #409eff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 28px;
}
.bind-icon.success {
  background: rgba(103, 194, 58, 0.1);
  color: #67c23a;
}
.bind-title {
  margin: 0 0 8px;
  font-size: 18px;
  color: var(--np-text);
}
.bind-desc {
  margin: 0 0 24px;
  font-size: 13px;
  color: var(--np-text-secondary);
}
.bind-form {
  max-width: 360px;
  margin: 0 auto 16px;
}
.bind-tip {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  font-size: 12px;
  color: var(--np-text-muted);
}

/* ===== 海报弹窗 ===== */
.poster-wrap {
  display: flex;
  justify-content: center;
}
.poster {
  width: 300px;
  border-radius: 16px;
  overflow: hidden;
  position: relative;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: #fff;
}
.poster-bg {
  position: absolute;
  inset: 0;
  overflow: hidden;
}
.poster-blob {
  position: absolute;
  border-radius: 50%;
  filter: blur(40px);
  opacity: 0.3;
}
.blob-a {
  width: 150px;
  height: 150px;
  background: #f093fb;
  top: -30px;
  right: -20px;
}
.blob-b {
  width: 120px;
  height: 120px;
  background: #4facfe;
  bottom: 30%;
  left: -30px;
}
.poster-content {
  position: relative;
  z-index: 1;
  padding: 30px 24px;
  text-align: center;
}
.poster-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}
.poster-logo {
  font-weight: 700;
  font-size: 16px;
  letter-spacing: 1px;
}
.poster-tag {
  font-size: 12px;
  padding: 4px 10px;
  background: rgba(255, 255, 255, 0.2);
  border-radius: 10px;
}
.poster-title {
  margin: 0 0 8px;
  font-size: 24px;
  font-weight: 700;
  line-height: 1.4;
}
.poster-sub {
  margin: 0 0 20px;
  font-size: 13px;
  opacity: 0.85;
}
.poster-qr {
  margin: 0 auto 16px;
  width: 120px;
  height: 120px;
  background: #fff;
  border-radius: 10px;
  padding: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.poster-qr img {
  width: 100%;
  height: 100%;
}
.poster-code {
  margin-bottom: 20px;
}
.pc-label {
  font-size: 11px;
  opacity: 0.8;
  margin-right: 8px;
}
.pc-value {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 2px;
  font-family: 'Courier New', monospace;
}
.poster-footer {
  font-size: 12px;
  opacity: 0.75;
}

/* ===== 响应式 ===== */
@media (max-width: 768px) {
  .hero-content {
    padding: 24px 20px;
    flex-direction: column;
    align-items: stretch;
  }
  .hero-title {
    font-size: 22px;
  }
  .hero-right {
    display: flex;
    justify-content: center;
  }
  .invite-code-card {
    width: 100%;
    max-width: 280px;
  }
  .stat-card {
    padding: 14px;
  }
  .stat-icon {
    width: 40px;
    height: 40px;
    font-size: 18px;
  }
  .stat-num {
    font-size: 18px;
  }
  .invite-item,
  .reward-item {
    padding: 12px;
    gap: 10px;
  }
  .invite-reward {
    width: auto;
  }
}
</style>
