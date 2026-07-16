/**
 * 字节流量格式化：自动转换为合适的单位（B/KB/MB/GB/TB）
 */
export function formatBytes(bytes: number, decimals = 2): string {
  if (!bytes || bytes <= 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(decimals))} ${sizes[i]}`
}

/**
 * 流量格式化为 GB（保留2位小数）
 */
export function toGB(bytes: number, decimals = 2): string {
  if (!bytes || bytes <= 0) return '0 GB'
  const gb = bytes / 1024 / 1024 / 1024
  return `${gb.toFixed(decimals)} GB`
}

/**
 * 流量格式化为 TB（保留2位小数）
 */
export function toTB(bytes: number, decimals = 2): string {
  if (!bytes || bytes <= 0) return '0 TB'
  const tb = bytes / 1024 / 1024 / 1024 / 1024
  return `${tb.toFixed(decimals)} TB`
}

/**
 * 智能流量格式化：自动在 MB / GB / TB 间选择
 * - < 1GB 显示 MB
 * - 1GB ~ 1024GB 显示 GB
 * - >= 1024GB 显示 TB
 */
export function formatTraffic(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 GB'
  const gb = bytes / 1024 / 1024 / 1024
  if (gb >= 1024) {
    return toTB(bytes)
  }
  if (gb < 1) {
    const mb = bytes / 1024 / 1024
    return `${mb.toFixed(0)} MB`
  }
  return toGB(bytes)
}

/**
 * 流量配额格式化：traffic_limit=0 表示不限
 */
export function formatTrafficLimit(limit: number, used = 0): string {
  if (!limit || limit <= 0) return '不限'
  return `${formatTraffic(used)} / ${formatTraffic(limit)}`
}

/**
 * 时间格式化
 * @param value 时间戳(毫秒/秒) 或 日期字符串 或 Date
 * @param format 格式模板，默认 'YYYY-MM-DD HH:mm:ss'
 */
export function formatTime(
  value: number | string | Date | null | undefined,
  format = 'YYYY-MM-DD HH:mm:ss',
): string {
  if (!value) return '-'
  let date: Date
  if (value instanceof Date) {
    date = value
  } else if (typeof value === 'number') {
    // 秒级时间戳自动转换为毫秒
    date = new Date(value < 1e12 ? value * 1000 : value)
  } else {
    date = new Date(value)
  }
  if (isNaN(date.getTime())) return '-'

  const pad = (n: number) => String(n).padStart(2, '0')
  const map: Record<string, string> = {
    YYYY: String(date.getFullYear()),
    MM: pad(date.getMonth() + 1),
    DD: pad(date.getDate()),
    HH: pad(date.getHours()),
    mm: pad(date.getMinutes()),
    ss: pad(date.getSeconds()),
  }
  return format.replace(/YYYY|MM|DD|HH|mm|ss/g, (m) => map[m])
}

/**
 * 相对时间：刚刚 / x分钟前 / x小时前 / x天前
 */
export function formatRelative(value: number | string | Date): string {
  if (!value) return '-'
  let date: Date
  if (typeof value === 'number') {
    date = new Date(value < 1e12 ? value * 1000 : value)
  } else if (value instanceof Date) {
    date = value
  } else {
    date = new Date(value)
  }
  const diff = Date.now() - date.getTime()
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return '刚刚'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days} 天前`
  return formatTime(value, 'YYYY-MM-DD')
}

/**
 * 计算距到期时间的剩余天数
 */
export function daysUntil(value: number | string | Date): number {
  if (!value) return 0
  let date: Date
  if (typeof value === 'number') {
    date = new Date(value < 1e12 ? value * 1000 : value)
  } else if (value instanceof Date) {
    date = value
  } else {
    date = new Date(value)
  }
  const diff = date.getTime() - Date.now()
  return Math.max(0, Math.ceil(diff / (1000 * 60 * 60 * 24)))
}

/**
 * 网络速度格式化：bps -> 自动选择 bps/Kbps/Mbps/Gbps
 */
export function formatSpeed(bps: number, decimals = 2): string {
  if (!bps || bps <= 0) return '0 bps'
  const k = 1000
  const sizes = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps']
  const i = Math.min(sizes.length - 1, Math.floor(Math.log(bps) / Math.log(k)))
  return `${parseFloat((bps / Math.pow(k, i)).toFixed(decimals))} ${sizes[i]}`
}

/**
 * 时长（秒）格式化为 "X天Y时Z分" / "Y时Z分" / "Z分"
 */
export function formatDuration(seconds: number): string {
  if (!seconds || seconds <= 0) return '0分'
  const s = Math.floor(seconds)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}天${h}时${m}分`
  if (h > 0) return `${h}时${m}分`
  return `${m}分`
}
