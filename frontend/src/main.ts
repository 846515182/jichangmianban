import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
// Element Plus 暗色模式变量
import 'element-plus/theme-chalk/dark/css-vars.css'
// 所有 Element Plus 图标全局注册
import * as ElementPlusIconsVue from '@element-plus/icons-vue'

import App from './App.vue'
import router from './router'
import { useAuthStore } from './stores/auth'
import './style.css'

const app = createApp(App)

// 全局注册 Element Plus 图标
for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

app.use(createPinia())
app.use(router)
app.use(ElementPlus)

// [P1-5 2026-07-17] 在 mount 之前调用 auth.restore(), 恢复 sessionStorage 里的 token
// 否则刷新页面后 token 丢失, 路由守卫会强制跳转到 /login, 用户体验差
const auth = useAuthStore()
auth.restore()
// 如果有 refreshToken 但没有 accessToken, 触发静默刷新
if (!auth.token && auth.refreshToken) {
  // 不 await, 避免阻塞首屏
  auth.refresh().catch(() => {
    // refresh 失败由 store 内部清理状态
  })
}

app.mount('#app')
