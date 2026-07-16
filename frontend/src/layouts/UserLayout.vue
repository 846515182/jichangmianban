<template>
  <div class="user-layout">
    <!-- 顶部导航栏 -->
    <header class="np-header">
      <div class="np-header-inner">
        <div class="np-brand">
          <span class="np-brand-icon">◆</span>
          <span class="np-brand-text np-title">NEXUS PANEL</span>
        </div>
        <el-icon class="np-mobile-menu-btn" @click="showMenu = !showMenu">
          <Menu v-if="!showMenu" />
          <Close v-else />
        </el-icon>
        <nav class="np-nav" :class="{ show: showMenu }">
          <router-link
            v-for="item in navItems"
            :key="item.path"
            :to="item.path"
            class="np-nav-item"
            active-class="active"
            @click="showMenu = false"
          >
            <el-icon><component :is="item.icon" /></el-icon>
            <span>{{ item.title }}</span>
          </router-link>
        </nav>
        <div class="np-header-right">
          <el-dropdown @command="handleCommand">
            <span class="np-user-chip">
              <el-avatar :size="30" class="np-avatar">
                {{ avatarText }}
              </el-avatar>
              <span class="np-username">{{ auth.userInfo?.username || '用户' }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="profile">
                  <el-icon><UserFilled /></el-icon>个人中心
                </el-dropdown-item>
                <el-dropdown-item command="logout" divided>
                  <el-icon><SwitchButton /></el-icon>退出登录
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>
    </header>

    <!-- 内容区 -->
    <main class="np-user-content np-fade-in">
      <router-view v-slot="{ Component }">
        <transition name="np-route" mode="out-in">
          <component :is="Component" />
        </transition>
      </router-view>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessageBox, ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()

const isMobile = ref(false)
const showMenu = ref(false)

const checkMobile = () => {
  isMobile.value = window.innerWidth < 768
  if (!isMobile.value) {
    showMenu.value = false
  }
}
onMounted(() => {
  checkMobile()
  window.addEventListener('resize', checkMobile)
})
onUnmounted(() => {
  window.removeEventListener('resize', checkMobile)
})

// 顶部导航项
const navItems = [
  { path: '/user/dashboard', title: '仪表盘', icon: 'DataLine' },
  { path: '/user/plans', title: '购买套餐', icon: 'ShoppingCart' },
  { path: '/user/orders', title: '我的订单', icon: 'List' },
  { path: '/user/nodes', title: '节点', icon: 'Connection' },
  { path: '/user/subscribe', title: '订阅', icon: 'Link' },
  { path: '/user/tickets', title: '工单', icon: 'ChatLineRound' },
  { path: '/user/announcements', title: '公告', icon: 'Bell' },
  { path: '/user/profile', title: '个人中心', icon: 'User' },
]

const avatarText = computed(() => {
  const name = auth.userInfo?.username || 'U'
  return name.charAt(0).toUpperCase()
})

const handleCommand = async (command: string) => {
  if (command === 'profile') {
    router.push('/user/profile')
  } else if (command === 'logout') {
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
      // 取消
    }
  }
}
</script>

<style scoped>
.user-layout {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.np-header {
  background: var(--np-bg-soft);
  border-bottom: 1px solid var(--np-border);
  position: sticky;
  top: 0;
  z-index: 100;
  backdrop-filter: blur(12px);
}
.np-header-inner {
  max-width: 1400px;
  margin: 0 auto;
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  position: relative;
}
.np-brand {
  display: flex;
  align-items: center;
  gap: 10px;
}
.np-brand-icon {
  color: var(--np-primary);
  font-size: 22px;
  text-shadow: 0 0 12px var(--np-primary-glow);
}
.np-brand-text {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 1px;
}

.np-nav {
  display: flex;
  align-items: center;
  gap: 4px;
}
.np-nav-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  border-radius: 8px;
  color: var(--np-text-secondary);
  text-decoration: none;
  font-size: 14px;
  transition: all 0.2s ease;
}
.np-nav-item:hover {
  color: var(--np-primary);
  background: var(--np-card-hover);
}
.np-nav-item.active {
  color: var(--np-primary);
  background: var(--np-primary-dim);
  box-shadow: inset 0 0 10px var(--np-primary-dim);
}

.np-user-chip {
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

.np-user-content {
  flex: 1;
  max-width: 1400px;
  width: 100%;
  margin: 0 auto;
  padding: 24px;
  box-sizing: border-box;
}

/* PC 端隐藏汉堡按钮 */
.np-mobile-menu-btn {
  display: none;
}

/* 移动端适配 */
@media (max-width: 768px) {
  .np-header-inner {
    height: 56px;
    padding: 0 16px;
  }
  .np-brand-text {
    display: none;
  }
  .np-nav {
    display: none;
    position: absolute;
    top: 56px;
    left: 0;
    right: 0;
    background: var(--np-bg-soft);
    flex-direction: column;
    border-bottom: 1px solid var(--np-border);
    padding: 8px;
    gap: 2px;
    z-index: 100;
    box-shadow: 0 4px 20px rgba(0,0,0,0.3);
  }
  .np-nav.show {
    display: flex;
  }
  .np-nav-item {
    width: 100%;
    padding: 12px 16px;
  }
  .np-username {
    display: none;
  }
  .np-user-content {
    padding: 16px;
  }
  .np-mobile-menu-btn {
    display: flex;
    align-items: center;
    cursor: pointer;
    font-size: 22px;
    color: var(--np-text);
  }
}
</style>
