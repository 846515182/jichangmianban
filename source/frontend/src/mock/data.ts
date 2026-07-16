/**
 * 数据接口定义 + 空默认值
 * 所有页面优先调用 API，失败时回退到空数据
 */
import type { UserInfo } from '@/stores/auth'

export interface ProxyNode {
  id: string
  name: string
  protocol: string
  server_address: string
  address: string
  port: number
  online: boolean
  status: string
  traffic_used: number
  uploadTraffic: number
  downloadTraffic: number
  latency: number
  transport: string
  tls: boolean
  node_token: string
  node_level: number
  is_enabled: boolean
  last_seen_at: string
  created_at: string
}

export interface UserRecord {
  id: string
  username: string
  email: string
  traffic_used: number
  traffic_limit: number
  expired_at: string
  status: string
  plan_id: string
  node_level: number
  subscribe_url: string
  sub_token: string
  created_at: string
}

export interface TicketRecord {
  id: string
  subject: string
  user_id?: string
  userId?: string
  username: string
  status: string
  priority: string
  created_at?: string
  createdAt?: string
  updated_at?: string
  updatedAt?: string
  messages?: any[]
}

export interface AnnouncementRecord {
  id: string
  title: string
  content: string
  pinned: boolean
  published_at?: string
  publishedAt?: string
}

export interface LoginAuditRecord {
  id: string
  username: string
  ip: string
  location: string
  user_agent: string
  success: boolean
  status: string
  created_at: string
}

export const mockNodes: ProxyNode[] = []
export const mockUsers: UserRecord[] = []
export const mockTickets: TicketRecord[] = []
export const mockAnnouncements: AnnouncementRecord[] = []
export const mockLoginAudits: LoginAuditRecord[] = []

export const mockDashboardStats = {
  total_users: 0,
  active_users: 0,
  expired_users: 0,
  total_nodes: 0,
  online_nodes: 0,
  enabled_nodes: 0,
  today_upload: 0,
  today_download: 0,
  week_upload: 0,
  week_download: 0,
  total_traffic: 0,
}

export const mockTrafficTrend = {
  days: [] as string[],
  upload: [] as number[],
  download: [] as number[],
}

export const mockUserGrowth = {
  days: [] as string[],
  total: [] as number[],
  new: [] as number[],
}

export const mockTopUsers = [] as { username: string; traffic: number }[]
export const mockNodeTrafficDist = [] as { name: string; value: number }[]

export const mockCurrentUserInfo: UserInfo = {
  id: "",
  username: "",
  email: "",
  role: "user",
  trafficUsed: 0,
  trafficLimit: 0,
  expireAt: "",
  status: "active",
}

export const mockSubscribeUrl = ""
