<template>
  <div class="user-announcements">
    <div class="page-header">
      <h2 class="page-title">系统公告</h2>
    </div>

    <div v-loading="loading">
      <div
        v-for="item in announcements"
        :key="item.id"
        class="np-card announce-card"
        :class="{ pinned: item.pinned }"
      >
        <div class="announce-header">
          <div class="announce-title">
            <el-icon v-if="item.pinned" class="pin-icon"><Top /></el-icon>
            <span>{{ item.title }}</span>
          </div>
          <el-tag v-if="item.pinned" size="small" type="warning" effect="dark">置顶</el-tag>
        </div>
        <!-- [S5 fix 2026-07-14] 移除 v-html, 改用纯文本 + white-space:pre-line 保留换行 -->
        <div class="announce-content">{{ item.content }}</div>
        <div class="announce-footer">
          <el-icon><Clock /></el-icon>
          <span>{{ formatTime(item.published_at) }}</span>
        </div>
      </div>
      <el-empty v-if="!announcements.length && !loading" description="暂无公告" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import request from '@/utils/request'
import { formatTime } from '@/utils/format'

interface AnnouncementRecord {
  id: string
  title: string
  content: string
  pinned: boolean
  published_at?: string
}

const loading = ref(false)
const announcements = ref<AnnouncementRecord[]>([])

onMounted(async () => {
  loading.value = true
  try {
    const res = await request.get<{ code: number; data: { list: AnnouncementRecord[] } }>('/api/v1/announcements')
    if (res && res.code === 0 && res.data) {
      announcements.value = res.data.list || []
    }
  } catch { /* 拦截器处理 */ } finally { loading.value = false }
})
</script>

<style scoped>
.page-header { margin-bottom: 20px; }
.page-title { margin: 0; font-size: 20px; color: var(--np-text); }
.announce-card { padding: 20px 24px; margin-bottom: 16px; }
.announce-card.pinned { border-color: rgba(255,190,11,0.3); }
.announce-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.announce-title { display: flex; align-items: center; gap: 8px; font-size: 17px; font-weight: 600; color: var(--np-text); }
.pin-icon { color: var(--np-warning); }
/* [S5 fix] 改用 white-space:pre-line 保留换行, 文本溢出自动换行 */
.announce-content {
  color: var(--np-text-secondary);
  font-size: 14px;
  line-height: 1.8;
  white-space: pre-line;     /* 保留换行符 \n */
  word-wrap: break-word;     /* 长 URL 强制换行 */
  overflow-wrap: anywhere;   /* 防溢出 */
}
.announce-footer { display: flex; align-items: center; gap: 6px; margin-top: 16px; padding-top: 12px; border-top: 1px solid var(--np-border); font-size: 12px; color: var(--np-text-muted); }

@media (max-width: 768px) {
  .announce-card { padding: 16px; }
  .announce-header { flex-direction: column; align-items: flex-start; gap: 8px; }
  .announce-title { font-size: 15px; }
  .announce-content { font-size: 13px; }
}
</style>
