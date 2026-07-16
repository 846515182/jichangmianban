<template>
  <router-view v-slot="{ Component }">
    <transition name="np-route" mode="out-in">
      <component :is="Component" />
    </transition>
  </router-view>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()

onMounted(() => {
  // 启用 Element Plus 暗色模式
  document.documentElement.classList.add('dark')
  // 初始化时从本地恢复登录态
  auth.restore()
})
</script>

<style>
/* 路由切换过渡动画 */
.np-route-enter-active,
.np-route-leave-active {
  transition: opacity 0.25s ease, transform 0.25s ease;
}
.np-route-enter-from {
  opacity: 0;
  transform: translateY(10px);
}
.np-route-leave-to {
  opacity: 0;
  transform: translateY(-10px);
}
</style>
