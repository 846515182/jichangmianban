import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'Login',
    component: () => import('@/views/Login.vue'),
    meta: { title: '登录', public: true },
  },
  {
    path: '/register',
    name: 'Register',
    component: () => import('@/views/Register.vue'),
    meta: { title: '注册', public: true },
  },
  {
    path: '/verify-email',
    name: 'VerifyEmail',
    component: () => import('@/views/VerifyEmail.vue'),
    meta: { title: '验证邮箱', public: true },
  },
  {
    path: '/forgot-password',
    name: 'ForgotPassword',
    component: () => import('@/views/ForgotPassword.vue'),
    meta: { title: '忘记密码', public: true },
  },
  {
    path: '/reset-password',
    name: 'ResetPassword',
    component: () => import('@/views/ResetPassword.vue'),
    meta: { title: '重置密码', public: true },
  },
  // 管理端路由（需要 admin 角色）
  {
    path: '/admin',
    component: () => import('@/layouts/AdminLayout.vue'),
    meta: { requiresAuth: true, role: 'admin' },
    redirect: '/admin/dashboard',
    children: [
      {
        path: 'dashboard',
        name: 'AdminDashboard',
        component: () => import('@/views/admin/Dashboard.vue'),
        meta: { title: '仪表盘' },
      },
      {
        path: 'nodes',
        name: 'AdminNodes',
        component: () => import('@/views/admin/Nodes.vue'),
        meta: { title: '节点管理' },
      },
      {
        path: 'users',
        name: 'AdminUsers',
        component: () => import('@/views/admin/Users.vue'),
        meta: { title: '用户管理' },
      },
      {
        path: 'subscriptions',
        name: 'AdminSubscriptions',
        component: () => import('@/views/admin/Subscriptions.vue'),
        meta: { title: '订阅管理' },
      },
      {
        path: 'plans',
        name: 'AdminPlans',
        component: () => import('@/views/admin/Plans.vue'),
        meta: { title: '套餐管理' },
      },
      {
        path: 'orders',
        name: 'AdminOrders',
        component: () => import('@/views/admin/Orders.vue'),
        meta: { title: '订单管理' },
      },
      {
        path: 'coupons',
        name: 'AdminCoupons',
        component: () => import('@/views/admin/Coupons.vue'),
        meta: { title: '优惠券' },
      },
      {
        path: 'traffic',
        name: 'AdminTraffic',
        component: () => import('@/views/admin/Traffic.vue'),
        meta: { title: '流量统计' },
      },
      {
        path: 'tickets',
        name: 'AdminTickets',
        component: () => import('@/views/admin/Tickets.vue'),
        meta: { title: '工单管理' },
      },
      {
        path: 'invite-codes',
        name: 'AdminInviteCodes',
        component: () => import('@/views/admin/InviteCodes.vue'),
        meta: { title: '邀请码' },
      },
      {
        path: 'announcements',
        name: 'AdminAnnouncements',
        component: () => import('@/views/admin/Announcements.vue'),
        meta: { title: '公告管理' },
      },
      {
        path: 'settings',
        name: 'AdminSettings',
        component: () => import('@/views/admin/Settings.vue'),
        meta: { title: '系统设置' },
      },
      {
        path: 'login-audit',
        name: 'AdminLoginAudit',
        component: () => import('@/views/admin/LoginAudit.vue'),
        meta: { title: '登录审计' },
      },
    ],
  },
  // 用户端路由（需要 user 角色）
  {
    path: '/user',
    component: () => import('@/layouts/UserLayout.vue'),
    meta: { requiresAuth: true, role: 'user' },
    redirect: '/user/dashboard',
    children: [
      {
        path: 'dashboard',
        name: 'UserDashboard',
        component: () => import('@/views/user/Dashboard.vue'),
        meta: { title: '仪表盘' },
      },
      {
        path: 'plans',
        name: 'UserPlans',
        component: () => import('@/views/user/Plans.vue'),
        meta: { title: '购买套餐' },
      },
      {
        path: 'orders',
        name: 'UserOrders',
        component: () => import('@/views/user/Orders.vue'),
        meta: { title: '我的订单' },
      },
      {
        path: 'nodes',
        name: 'UserNodes',
        component: () => import('@/views/user/Nodes.vue'),
        meta: { title: '节点列表' },
      },
      {
        path: 'subscribe',
        name: 'UserSubscribe',
        component: () => import('@/views/user/Subscribe.vue'),
        meta: { title: '订阅管理' },
      },
      {
        path: 'tickets',
        name: 'UserTickets',
        component: () => import('@/views/user/Tickets.vue'),
        meta: { title: '我的工单' },
      },
      {
        path: 'announcements',
        name: 'UserAnnouncements',
        component: () => import('@/views/user/Announcements.vue'),
        meta: { title: '系统公告' },
      },
      {
        path: 'change-email',
        name: 'UserChangeEmail',
        component: () => import('@/views/user/ChangeEmail.vue'),
        meta: { title: '修改邮箱' },
      },
      {
        path: 'profile',
        name: 'UserProfile',
        component: () => import('@/views/user/Profile.vue'),
        meta: { title: '个人中心' },
      },
    ],
  },
  // 根路径根据角色跳转
  {
    path: '/',
    // [P1-4 2026-07-17] redirect 改为硬编码 /login, 不再依赖 auth.role 状态
    // 原因: pinia 还没挂载时 auth.role 是空, 这里若用 role 判定会导致无 token
    // 用户访问 / 看到一次空白闪烁。统一走 /login, 登录页内部会根据 role 再跳转
    redirect: () => '/login',
  },
  // 兜底: 未知路径重定向到 /login (原逻辑保留, 因为没单独的 404 页)
  { path: '/:pathMatch(.*)*', redirect: '/login' },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

// [P1-4 2026-07-17] 抽离"角色 → 主页"映射为独立函数, 避免各处重复 if/else
function dashboardByRole(role?: string): string {
  if (role === 'admin') return '/admin/dashboard'
  if (role === 'user') return '/user/dashboard'
  return '/login'
}

// 全局前置守卫：鉴权与角色校验
router.beforeEach((to, _from) => {
  const auth = useAuthStore()
  document.title = to.meta.title ? `${to.meta.title} - Nexus Panel` : 'Nexus Panel'

  // 公开页面直接放行
  if (to.meta.public) {
    // 已登录用户访问登录页/注册页则跳转到对应主页
    if ((to.name === 'Login' || to.name === 'Register') && auth.token) {
      return dashboardByRole(auth.role)
    }
    return true
  }

  // 需要鉴权
  if (to.meta.requiresAuth) {
    if (!auth.token) {
      return { name: 'Login' }
    }
    // 角色校验
    const requiredRole = to.meta.role as string | undefined
    if (requiredRole && auth.role !== requiredRole) {
      // 角色不匹配, 跳转到自身主页
      return dashboardByRole(auth.role)
    }
    return true
  }

  // 兜底: 任何未声明 requiresAuth 也未声明 public 的页面, 默认要求登录
  // [P1-4 2026-07-17] 防止新增页面忘记加 meta.public 时被默认放行
  if (!auth.token) {
    return { name: 'Login' }
  }
  return true
})

export default router
