<template>
  <div class="admin-layout">
    <!-- 遮罩层（移动端） -->
    <div v-if="showDrawer && isMobile" class="np-sidebar-overlay" @click="showDrawer = false"></div>
    <!-- 左侧导航栏 -->
    <aside class="np-sidebar" :class="{ collapsed: isCollapse, show: showDrawer && isMobile }">
      <div class="np-logo">
        <span class="np-logo-icon">◆</span>
        <span v-show="!isCollapse" class="np-logo-text np-title">NEXUS</span>
      </div>
      <el-menu
        :default-active="activeMenu"
        :collapse="isCollapse"
        :collapse-transition="false"
        router
        class="np-side-menu"
        background-color="transparent"
        text-color="#8b98a9"
        active-text-color="#00f5d4"
      >
        <el-menu-item index="/admin/dashboard">
          <el-icon><DataLine /></el-icon>
          <template #title>仪表盘</template>
        </el-menu-item>
        <el-menu-item index="/admin/nodes">
          <el-icon><Connection /></el-icon>
          <template #title>节点管理</template>
        </el-menu-item>
        <el-menu-item index="/admin/monitor">
          <el-icon><Monitor /></el-icon>
          <template #title>节点监控</template>
        </el-menu-item>
        <el-sub-menu index="user-ops">
          <template #title>
            <el-icon><User /></el-icon>
            <span>用户运营</span>
          </template>
          <el-menu-item index="/admin/users">
            <el-icon><UserFilled /></el-icon>
            <template #title>用户管理</template>
          </el-menu-item>
          <el-menu-item index="/admin/subscriptions">
            <el-icon><Link /></el-icon>
            <template #title>订阅管理</template>
          </el-menu-item>
          <el-menu-item index="/admin/tickets">
            <el-icon><ChatLineRound /></el-icon>
            <template #title>工单管理</template>
          </el-menu-item>
        </el-sub-menu>
        <el-sub-menu index="commerce">
          <template #title>
            <el-icon><Goods /></el-icon>
            <span>商品财务</span>
          </template>
          <el-menu-item index="/admin/plans">
            <el-icon><Goods /></el-icon>
            <template #title>套餐管理</template>
          </el-menu-item>
          <el-menu-item index="/admin/orders">
            <el-icon><List /></el-icon>
            <template #title>订单管理</template>
          </el-menu-item>
          <el-menu-item index="/admin/coupons">
            <el-icon><Ticket /></el-icon>
            <template #title>优惠券</template>
          </el-menu-item>
        </el-sub-menu>
        <el-menu-item index="/admin/traffic">
          <el-icon><TrendCharts /></el-icon>
          <template #title>流量统计</template>
        </el-menu-item>
        <el-sub-menu index="system">
          <template #title>
            <el-icon><Setting /></el-icon>
            <span>系统</span>
          </template>
          <el-menu-item index="/admin/announcements">
            <el-icon><Bell /></el-icon>
            <template #title>公告管理</template>
          </el-menu-item>
          <el-menu-item index="/admin/settings">
            <el-icon><Setting /></el-icon>
            <template #title>系统设置</template>
          </el-menu-item>
          <el-menu-item index="/admin/login-audit">
            <el-icon><DocumentChecked /></el-icon>
            <template #title>审计日志</template>
          </el-menu-item>
        </el-sub-menu>
      </el-menu>
    </aside>

    <!-- 右侧主区域 -->
    <div class="np-main">
      <!-- 顶部栏 -->
      <header class="np-topbar">
        <div class="np-topbar-left">
          <el-icon class="np-collapse-btn" @click="toggleSidebar">
            <Fold v-if="!isCollapse" />
            <Expand v-else />
          </el-icon>
          <span class="np-page-title">{{ currentTitle }}</span>
        </div>
        <div class="np-topbar-right">
          <el-dropdown @command="handleCommand">
            <span class="np-user-info">
              <el-avatar :size="32" class="np-avatar">
                {{ avatarText }}
              </el-avatar>
              <span class="np-username">{{ auth.userInfo?.username || '管理员' }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="profile">
                  <el-icon><UserFilled /></el-icon>个人信息
                </el-dropdown-item>
                <el-dropdown-item command="logout" divided>
                  <el-icon><SwitchButton /></el-icon>退出登录
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </header>

      <!-- 内容区 -->
      <main class="np-content np-fade-in">
        <router-view v-slot="{ Component }">
          <transition name="np-route" mode="out-in">
            <component :is="Component" />
          </transition>
        </router-view>
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox, ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const isCollapse = ref(false)
const isMobile = ref(false)
const showDrawer = ref(false)

const checkMobile = () => {
  isMobile.value = window.innerWidth < 768
}
onMounted(() => {
  checkMobile()
  window.addEventListener('resize', checkMobile)
})
onUnmounted(() => {
  window.removeEventListener('resize', checkMobile)
})

const toggleSidebar = () => {
  if (isMobile.value) {
    showDrawer.value = !showDrawer.value
  } else {
    isCollapse.value = !isCollapse.value
  }
}

// 当前激活菜单
const activeMenu = computed(() => route.path)

// 当前页面标题
const currentTitle = computed(() => (route.meta.title as string) || '仪表盘')

// 头像文字
const avatarText = computed(() => {
  const name = auth.userInfo?.username || 'A'
  return name.charAt(0).toUpperCase()
})

// 下拉菜单命令处理
const handleCommand = async (command: string) => {
  if (command === 'logout') {
    try {
      await ElMessageBox.confirm('确定要退出登录吗？', '提示', {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning',
      })
      await auth.logout()
      ElMessage.success('已退出登录')
      router.push('/login')
    } catch {
      // 取消操作
    }
  } else if (command === 'profile') {
    ElMessage.info('管理员信息')
  }
}
</script>

<style scoped>
.admin-layout {
  display: flex;
  height: 100vh;
  overflow: hidden;
}

/* 侧边栏 */
.np-sidebar {
  width: 220px;
  background: var(--np-bg-soft);
  border-right: 1px solid var(--np-border);
  display: flex;
  flex-direction: column;
  transition: width 0.3s ease;
  flex-shrink: 0;
}
.np-sidebar.collapsed {
  width: 64px;
}
.np-logo {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  border-bottom: 1px solid var(--np-border);
}
.np-logo-icon {
  color: var(--np-primary);
  font-size: 22px;
  text-shadow: 0 0 12px var(--np-primary-glow);
}
.np-logo-text {
  font-size: 20px;
  font-weight: 700;
}
.np-side-menu {
  flex: 1;
  padding: 12px 8px;
  overflow-y: auto;
}
.np-side-menu :deep(.el-menu-item) {
  border-radius: 8px;
  margin-bottom: 4px;
}
.np-side-menu :deep(.el-menu-item.is-active) {
  background: var(--np-primary-dim) !important;
  box-shadow: inset 0 0 12px var(--np-primary-dim);
}
.np-side-menu :deep(.el-menu-item:hover) {
  background: var(--np-card-hover) !important;
}
/* 子菜单标题 */
.np-side-menu :deep(.el-sub-menu__title) {
  border-radius: 8px;
  margin-bottom: 4px;
  color: #8b98a9;
}
.np-side-menu :deep(.el-sub-menu__title:hover) {
  background: var(--np-card-hover) !important;
}
.np-side-menu :deep(.el-sub-menu.is-active > .el-sub-menu__title) {
  color: var(--np-primary);
}
/* 子菜单内层项 */
.np-side-menu :deep(.el-menu-item:not(.is-active)) {
  background: transparent !important;
}

/* 主区域 */
.np-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.np-topbar {
  height: 60px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  background: var(--np-bg-soft);
  border-bottom: 1px solid var(--np-border);
  flex-shrink: 0;
}
.np-topbar-left {
  display: flex;
  align-items: center;
  gap: 16px;
}
.np-collapse-btn {
  font-size: 20px;
  color: var(--np-text-secondary);
  cursor: pointer;
  transition: color 0.2s;
}
.np-collapse-btn:hover {
  color: var(--np-primary);
}
.np-page-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--np-text);
}
.np-user-info {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  color: var(--np-text);
}
.np-avatar {
  background: var(--np-primary-dim);
  color: var(--np-primary);
  border: 1px solid var(--np-primary);
}
.np-username {
  font-size: 14px;
}
.np-content {
  flex: 1;
  padding: 24px;
  overflow-y: auto;
}

/* 移动端适配 */
@media (max-width: 768px) {
  .np-sidebar {
    position: fixed;
    left: 0;
    top: 0;
    bottom: 0;
    width: 220px !important;
    z-index: 1001;
    transform: translateX(-100%);
    transition: transform 0.3s ease;
    box-shadow: 2px 0 20px rgba(0,0,0,0.3);
  }
  .np-sidebar.collapsed {
    width: 220px !important;
  }
  .np-sidebar.show {
    transform: translateX(0);
  }
  .np-sidebar-overlay {
    display: block;
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.5);
    z-index: 1000;
  }
  .np-topbar {
    padding: 0 12px;
    height: 56px;
  }
  .np-topbar-left {
    gap: 10px;
    min-width: 0;
  }
  .np-page-title {
    font-size: 15px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 160px;
  }
  .np-username {
    display: none;
  }
  .np-content {
    padding: 12px;
    min-width: 0;
  }
}
</style>
