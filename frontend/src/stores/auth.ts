import { defineStore } from 'pinia'
import axios from 'axios'
import request from '@/utils/request'

export type UserRole = 'admin' | 'user'

export interface UserInfo {
  id: string
  username: string
  email: string
  role: UserRole
  trafficUsed: number
  trafficLimit: number
  expireAt: string
  status: 'active' | 'disabled'
}

interface AuthState {
  token: string
  refreshToken: string
  role: UserRole | null
  userInfo: UserInfo | null
}

const TOKEN_KEY = 'np_token'
const REFRESH_TOKEN_KEY = 'np_refresh_token'
const ROLE_KEY = 'np_role'
const USER_KEY = 'np_user'

// 安全提示: 生产环境建议后端实现 httpOnly Cookie 替代 localStorage 存储 Token
// access_token 也一并持久化到 sessionStorage, 这样刷新浏览器后能恢复登录态,
// 不会把用户踢回登录页。sessionStorage 在同标签页刷新时保留, 关闭标签页才清空
// (相当于登出)。若 access_token 已过期, axios 拦截器的 401 自动 refresh 机制会换新。

const rawAxios = axios.create({ baseURL: '/', timeout: 15000 })

export const useAuthStore = defineStore('auth', {
  state: (): AuthState => ({
    token: '',
    refreshToken: '',
    role: null,
    userInfo: null,
  }),
  getters: {
    isLoggedIn: (state) => !!state.token,
    isAdmin: (state) => state.role === 'admin',
    isUser: (state) => state.role === 'user',
  },
  actions: {
    restore() {
      this.token = sessionStorage.getItem(TOKEN_KEY) || ''
      this.refreshToken = sessionStorage.getItem(REFRESH_TOKEN_KEY) || ''
      this.role = (sessionStorage.getItem(ROLE_KEY) as UserRole) || null
      const userStr = sessionStorage.getItem(USER_KEY)
      this.userInfo = userStr ? JSON.parse(userStr) : null
    },

    persist() {
      if (this.token) sessionStorage.setItem(TOKEN_KEY, this.token)
      sessionStorage.setItem(REFRESH_TOKEN_KEY, this.refreshToken)
      if (this.role) sessionStorage.setItem(ROLE_KEY, this.role)
      if (this.userInfo) sessionStorage.setItem(USER_KEY, JSON.stringify(this.userInfo))
    },

    async login(username: string, password: string) {
      try {
        const res = await request.post('/api/v1/auth/login', { username, password, target: 'user' })
        this.token = res.data.access_token
        this.refreshToken = res.data.refresh_token
        this.role = res.data.role
        try {
          const info = await request.get('/api/v1/user/info')
          this.userInfo = info.data
        } catch {
          this.userInfo = null
        }
        this.persist()
        return this.role as UserRole
      } catch (e) {
        this.clear()
        throw e
      }
    },

    async loginAdmin(username: string, password: string) {
      try {
        const res = await request.post('/api/v1/auth/login', { username, password, target: 'admin' })
        this.token = res.data.access_token
        this.refreshToken = res.data.refresh_token
        this.role = (res.data.role === 'super_admin' || res.data.role === 'admin') ? 'admin' : res.data.role
        this.persist()
        return this.role as UserRole
      } catch (e) {
        this.clear()
        throw e
      }
    },

    async loginAuto(username: string, password: string) {
      let lastErr: any
      try {
        const resp = await rawAxios.post('/api/v1/auth/login', { username, password, target: 'admin' })
        const res = resp.data
        if (res.code !== 0) {
          throw new Error(res.msg || 'login failed')
        }
        this.token = res.data.access_token
        this.refreshToken = res.data.refresh_token
        this.role = (res.data.role === 'super_admin' || res.data.role === 'admin') ? 'admin' : res.data.role
        this.persist()
        return this.role as UserRole
      } catch (adminErr: any) {
        lastErr = adminErr
      }
      try {
        const resp = await rawAxios.post('/api/v1/auth/login', { username, password, target: 'user' })
        const res = resp.data
        if (res.code !== 0) {
          throw new Error(res.msg || 'login failed')
        }
        this.token = res.data.access_token
        this.refreshToken = res.data.refresh_token
        this.role = res.data.role
        try {
          const info = await request.get('/api/v1/user/info')
          this.userInfo = info.data
        } catch {
          this.userInfo = null
        }
        this.persist()
        return this.role as UserRole
      } catch (userErr: any) {
        throw userErr
      }
    },

    async refresh() {
      if (!this.refreshToken) throw new Error('no refresh token')
      const res = await request.post('/api/v1/auth/refresh', { refresh_token: this.refreshToken })
      this.token = res.data.access_token
      if (res.data.refresh_token) {
        this.refreshToken = res.data.refresh_token
      }
      this.persist()
      return this.token
    },

    async logout() {
      try {
        await request.post('/api/v1/auth/logout')
      } catch {
      }
      this.clear()
    },

    clear() {
      this.token = ''
      this.refreshToken = ''
      this.role = null
      this.userInfo = null
      sessionStorage.removeItem(TOKEN_KEY)
      sessionStorage.removeItem(REFRESH_TOKEN_KEY)
      sessionStorage.removeItem(ROLE_KEY)
      sessionStorage.removeItem(USER_KEY)
    },

    setUserInfo(info: UserInfo) {
      this.userInfo = info
      this.persist()
    },
  },
})
