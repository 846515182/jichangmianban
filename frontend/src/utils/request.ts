import axios, { type AxiosRequestConfig, type InternalAxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import router from '@/router'

// 扩展 axios 配置, 支持 silent 自定义字段: true 时请求失败不弹全局错误弹窗。
// 用于更新/重启面板期间的轮询请求(这些请求预期会因面板短暂不可用而失败,
// 不应每次失败都弹 ElMessage.error 刷屏)。
declare module 'axios' {
  interface AxiosRequestConfig {
    silent?: boolean
  }
}

const service = axios.create({
  baseURL: '',
  timeout: 15000,
})

let isRefreshing = false
let requestsQueue: Array<(token: string) => void> = []

service.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    const auth = useAuthStore()
    if (auth.token && config.headers) {
      config.headers.Authorization = `Bearer ${auth.token}`
    }
    return config
  },
  (error) => Promise.reject(error),
)

service.interceptors.response.use(
  (response) => {
    const res = response.data
    if (res && typeof res === 'object' && res.code !== undefined && res.code !== 0) {
      // 登录请求不弹全局错误，由调用方(loginAuto)处理，避免先弹"账号或密码错误"误导用户
      // silent 标记的请求也不弹错误，由调用方自行处理
      const url = response.config?.url || ''
      const isSilent = (response.config as any)?.silent === true
      if (!url.includes('/auth/login') && !isSilent) {
        ElMessage.error(res.msg || '请求失败')
      }
      return Promise.reject(new Error(res.msg || '请求失败'))
    }
    return res
  },
  async (error) => {
    const { response, config } = error

    // 登录请求的网络错误也不弹全局提示，由调用方处理
    const isLoginReq = config?.url && config.url.includes('/auth/login')

    if (
      response &&
      response.status === 401 &&
      config.url &&
      !config.url.includes('/auth/refresh') &&
      !config.url.includes('/auth/login')
    ) {
      const auth = useAuthStore()

      if (!isRefreshing) {
        isRefreshing = true
        try {
          const newToken = await auth.refresh()
          isRefreshing = false
          requestsQueue.forEach((cb) => cb(newToken))
          requestsQueue = []
          config.headers.Authorization = `Bearer ${newToken}`
          return service(config)
        } catch (refreshErr) {
          isRefreshing = false
          requestsQueue = []
          auth.clear()
          ElMessage.error('登录已过期，请重新登录')
          router.push('/login')
          return Promise.reject(refreshErr)
        }
      } else {
        return new Promise((resolve) => {
          requestsQueue.push((token: string) => {
            config.headers.Authorization = `Bearer ${token}`
            resolve(service(config))
          })
        })
      }
    }

    if (!isLoginReq && !(config as any)?.silent) {
      let msg =
        response?.data?.msg || response?.data?.message || error.message || '网络异常'
      // 502/503 通常是后端重启或短暂不可用, 显示友好中文提示而非英文错误
      if (response?.status === 502 || response?.status === 503) {
        msg = '系统正在重启,请稍候...'
      }
      ElMessage.error(msg)
    }
    return Promise.reject(error)
  },
)

export default {
  get<T = any>(url: string, config?: AxiosRequestConfig) {
    return service.get<any, T>(url, config)
  },
  post<T = any>(url: string, data?: any, config?: AxiosRequestConfig) {
    return service.post<any, T>(url, data, config)
  },
  put<T = any>(url: string, data?: any, config?: AxiosRequestConfig) {
    return service.put<any, T>(url, data, config)
  },
  delete<T = any>(url: string, config?: AxiosRequestConfig) {
    return service.delete<any, T>(url, config)
  },
  patch<T = any>(url: string, data?: any, config?: AxiosRequestConfig) {
    return service.patch<any, T>(url, data, config)
  },
}
