// [compat] 重导出 useUserStore 以兼容旧 import 路径 (Login.vue 等)
import { useAuthStore as _useAuthStore } from './auth'
export const useUserStore = _useAuthStore
export default { useUserStore }
