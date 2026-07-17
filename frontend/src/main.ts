import { createApp } from 'vue'
import { createPinia } from 'pinia'
// Element Plus 暗色模式变量(全局 CSS 变量, 不随组件按需加载, 需全局引入)
import 'element-plus/theme-chalk/dark/css-vars.css'
// 命令式组件样式(ElMessage/ElMessageBox/ElNotification/ElLoading)全局引入
// 根因: 这些组件是 JS 命令式调用(ElMessageBox.confirm()), 不在模板里写
// <el-message-box>, unplugin-vue-components 的按需导入只扫描模板标签,
// 不会为它们自动导入样式。导致弹窗缺少 display/width/padding/定位等
// 基础样式, 退化成默认块级元素显示在左上角。这里全局引入确保样式齐全。
import 'element-plus/theme-chalk/el-overlay.css'
import 'element-plus/theme-chalk/el-message-box.css'
import 'element-plus/theme-chalk/el-message.css'
import 'element-plus/theme-chalk/el-notification.css'
import 'element-plus/theme-chalk/el-loading.css'
// 所有 Element Plus 图标全局注册(图标不在 unplugin-vue-components 自动导入范围内)
import * as ElementPlusIconsVue from '@element-plus/icons-vue'

import App from './App.vue'
import router from './router'
import { useAuthStore } from './stores/auth'
import './style.css'

// 修复 UI-BUNDLE-01 (P1): 旧版同时引入完整 ElementPlus 与全量 CSS:
//   import ElementPlus from 'element-plus'
//   import 'element-plus/dist/index.css'
//   app.use(ElementPlus)
// 这会让 vite 把整个 element-plus(组件 + 全量样式)打进首屏 chunk,
// 完全抵消了 vite.config.ts 中 unplugin-vue-components + ElementPlusResolver
// 的按需加载收益(首屏 JS 体积可从 ~800KB 降到 ~200KB)。
// 现移除全量注册, 改由:
//   - 组件按需: 模板里用到的 <el-xxx> 由 unplugin-vue-components 自动按需导入
//   - 工具方法按需: ElMessage/ElMessageBox 等由 unplugin-auto-import 自动按需导入
//   - 指令按需: v-loading 由 ElementPlusResolver 自动注册
//   - 暗色主题 CSS 变量仍需全局引入(纯 CSS 变量定义, 体积小)
const app = createApp(App)

// 全局错误处理器: 捕获组件渲染/生命周期内的未处理异常, 避免页面静默白屏。
// 把错误以 alert 形式抛出, 便于排查"点了导航没反应"实为渲染异常白屏的问题。
app.config.errorHandler = (err, _instance, info) => {
  console.error('[Vue ErrorHandler]', err, info)
  const msg = err instanceof Error ? err.message : String(err)
  try {
    const box = document.createElement('div')
    box.style.cssText = 'position:fixed;top:50%;left:50%;transform:translate(-50%,-50%);z-index:99999;background:#2a1a1a;color:#ffb4b4;border:1px solid #c00;border-radius:8px;padding:16px 20px;max-width:90vw;max-height:80vh;overflow:auto;font:14px/1.6 monospace;white-space:pre-wrap;box-shadow:0 8px 32px rgba(0,0,0,.5)'
    box.textContent = '页面渲染错误:\n\n' + msg + '\n\n[info] ' + info + '\n\n请截图反馈。'
    if (!document.getElementById('__err_box__')) {
      box.id = '__err_box__'
      document.body.appendChild(box)
    }
  } catch { /* ignore */ }
}

// 全局注册 Element Plus 图标(图标库不在自动导入范围内, 需手动注册)
for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

app.use(createPinia())
app.use(router)

// 修复刷新即登出: 在 app.mount 之前从 sessionStorage 恢复登录态。
// 原因: 路由守卫 beforeEach 会在首次导航时执行(早于 App.vue 的 onMounted),
// 若此时 store 还是空的, 守卫看到 !auth.token 就会把用户踢回登录页。
// 这里在 mount 前同步恢复 token/role/userInfo, 确保守卫首次执行时已有登录态。
useAuthStore().restore()

app.mount('#app')
