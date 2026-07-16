import axios, { type AxiosRequestConfig, type InternalAxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '@/stores/auth'
import router from '@/router'

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
      // [P1-6 2026-07-17] 使用 .set() API, 兼容 axios v1.x 中 headers 可能为 AxiosHeaders 实例
      // 直接赋值可能在严格类型下触发 TS 错误, 也可能在某些 axios 版本中静默失败
      config.headers.set('Authorization', `Bearer ${auth.token}`)
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
      const url = response.config?.url || ''
      if (!url.includes('/auth/login')) {
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
          // [P1-6 2026-07-17] 同上, 使用 .set() 替代直接赋值
          if (config.headers) {
            config.headers.set('Authorization', `Bearer ${newToken}`)
          }
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
            // [P1-6 2026-07-17] 同上
            if (config.headers) {
              config.headers.set('Authorization', `Bearer ${token}`)
            }
            resolve(service(config))
          })
        })
      }
    }

    if (!isLoginReq) {
      const msg =
        response?.data?.msg || response?.data?.message || error.message || '网络异常'
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
