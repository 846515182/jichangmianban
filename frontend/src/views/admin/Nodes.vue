<template>
  <div class="admin-nodes">
    <div class="np-card page-card">
      <div class="page-header">
        <div>
          <h2 class="page-title">节点管理</h2>
          <p class="page-desc">管理代理节点，创建后获取 node_token 用于部署 node-agent</p>
        </div>
        <div class="header-actions">
          <el-input
            v-model="keyword"
            placeholder="搜索节点名称/地址"
            :prefix-icon="Search"
            clearable
            style="width: 220px"
          />
          <el-button type="primary" @click="openDialog()">
            <el-icon><Plus /></el-icon>新增节点
          </el-button>
        </div>
      </div>

      <!-- 统一流量汇总(按服务器IP聚合，管理员参考) -->
      <el-collapse v-model="trafficSummaryActive" style="margin-bottom:16px" v-if="trafficGroups.length">
        <el-collapse-item :title="`📊 服务器流量汇总（按IP聚合，共 ${trafficGroups.length} 台服务器）`" name="traffic">
          <div class="traffic-summary-grid">
            <div v-for="g in trafficGroups" :key="g.server_address" class="traffic-summary-card">
              <div class="ts-header">
                <span class="ts-ip">{{ g.server_address }}</span>
                <el-tag size="small" type="info" effect="plain">{{ g.node_count }} 节点</el-tag>
              </div>
              <div class="ts-body">
                <div class="ts-used">{{ formatTraffic(g.traffic_used) }}</div>
                <div class="ts-limit" v-if="g.traffic_limit > 0">
                  / {{ formatTraffic(g.traffic_limit) }}
                </div>
                <div class="ts-limit" v-else>/ 不限</div>
              </div>
              <div class="ts-bar" v-if="g.traffic_limit > 0">
                <div class="ts-bar-inner" :style="{ width: groupPercent(g) + '%', background: groupColor(g) }"></div>
              </div>
              <div class="ts-remain" v-if="g.traffic_limit > 0">
                剩余 {{ formatTraffic(Math.max(0, g.traffic_limit - g.traffic_used)) }}
              </div>
            </div>
          </div>
        </el-collapse-item>
      </el-collapse>

      <!-- PC 端：表格视图 -->
      <el-table v-if="!isMobile" :data="filteredList" stripe v-loading="loading">
        <el-table-column prop="name" label="名称" min-width="120" />
        <el-table-column prop="country_code" label="地区" width="70">
          <template #default="{ row }">{{ row.country_code || '-' }}</template>
        </el-table-column>
        <el-table-column prop="protocol" label="协议" width="90">
          <template #default="{ row }">
            <el-tag size="small" effect="dark" :type="protocolTagType(row.protocol)">
              {{ (row.protocol || 'vless').toUpperCase() }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="动态限速" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="row.usage_type === 'limited' ? 'warning' : 'info'" effect="plain">
              {{ row.usage_type === 'limited' ? '已开启' : '关闭' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="服务器地址" min-width="150">
          <template #default="{ row }">{{ row.server_address }}:{{ row.port }}</template>
        </el-table-column>
        <el-table-column label="绑定套餐" min-width="140">
          <template #default="{ row }">
            <div v-if="row.plan_ids && row.plan_ids.length" style="display:flex;flex-wrap:wrap;gap:4px">
              <el-tag
                v-for="pid in row.plan_ids"
                :key="pid"
                size="small"
                effect="plain"
                type="success"
              >
                {{ planName(pid) }}
              </el-tag>
            </div>
            <span v-else style="color:#909399;font-size:12px">未绑定</span>
          </template>
        </el-table-column>
        <el-table-column label="在线状态" width="90">
          <template #default="{ row }">
            <span class="np-flex" style="gap:6px;align-items:center;">
              <i class="np-dot" :class="row.online ? 'online' : 'offline'"></i>
              {{ row.online ? '在线' : '离线' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="负载状态" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="loadStatusTagType(row.load_status)" effect="dark">
              {{ loadStatusText(row.load_status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="实时负载" width="200">
          <template #default="{ row }">
            <div v-if="row.runtime && row.runtime.updated_at > 0" class="runtime-cell">
              <div class="rt-row">
                <span class="rt-label">CPU</span>
                <div class="rt-bar">
                  <div class="rt-bar-inner" :style="{ width: Math.min(100, row.runtime.cpu_usage || 0) + '%', background: loadColor(row.runtime.cpu_usage) }"></div>
                </div>
                <span class="rt-value">{{ (row.runtime.cpu_usage || 0).toFixed(1) }}%</span>
              </div>
              <div class="rt-row">
                <span class="rt-label">内存</span>
                <div class="rt-bar">
                  <div class="rt-bar-inner" :style="{ width: Math.min(100, row.runtime.memory_usage || 0) + '%', background: loadColor(row.runtime.memory_usage) }"></div>
                </div>
                <span class="rt-value">{{ (row.runtime.memory_usage || 0).toFixed(1) }}%</span>
              </div>
              <div class="rt-row">
                <span class="rt-label">连接</span>
                <span class="rt-value" style="margin-left:auto">{{ row.runtime.online_connections || 0 }}</span>
                <span class="rt-label" style="margin-left:8px">速度</span>
                <span class="rt-value">{{ formatSpeed(row.runtime.speed_bps) }}</span>
              </div>
            </div>
            <span v-else style="color:#909399;font-size:12px">无数据</span>
          </template>
        </el-table-column>
        <el-table-column label="启用" width="70">
          <template #default="{ row }">
            <el-tag size="small" :type="row.is_enabled ? 'success' : 'info'" effect="plain">
              {{ row.is_enabled ? '是' : '否' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="scope">
            <div class="row-actions">
              <el-button size="small" link type="primary" @click="openDialog(scope.row as NodeRow)">编辑</el-button>
              <el-dropdown trigger="click" @command="(cmd: string) => handleRowAction(cmd, scope.row as NodeRow)">
                <el-button size="small" link>更多<el-icon class="el-icon--right"><ArrowDown /></el-icon></el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="deploy">一键部署</el-dropdown-item>
                    <el-dropdown-item command="deployInfo">部署信息</el-dropdown-item>
                    <el-dropdown-item command="rotateToken">轮换Token</el-dropdown-item>
                    <el-dropdown-item command="ping" :disabled="pingLoading.get(scope.row.id)||false">
                      {{ pingLoading.get(scope.row.id) ? '检测中...' : (scope.row.online ? '重新检测' : '检测连接') }}
                    </el-dropdown-item>
                    <el-dropdown-item command="delete" divided>删除</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </div>
          </template>
        </el-table-column>
      </el-table>

      <!-- 移动端：卡片视图(竖向展开所有信息, 避免表格水平滚动看不到列) -->
      <div v-else class="node-cards" v-loading="loading">
        <div v-for="row in filteredList" :key="row.id" class="node-card">
          <div class="nc-header">
            <div class="nc-title-wrap">
              <span class="nc-name">{{ row.name }}</span>
              <el-tag size="small" effect="dark" :type="protocolTagType(row.protocol)">{{ (row.protocol || 'vless').toUpperCase() }}</el-tag>
              <el-tag size="small" :type="row.usage_type === 'limited' ? 'warning' : 'info'" effect="plain">{{ row.usage_type === 'limited' ? '动态限速' : '不限速' }}</el-tag>
              <el-tag size="small" :type="loadStatusTagType(row.load_status)" effect="dark">{{ loadStatusText(row.load_status) }}</el-tag>
              <el-tag size="small" :type="row.is_enabled ? 'success' : 'info'" effect="plain">{{ row.is_enabled ? '启用' : '停用' }}</el-tag>
            </div>
            <span class="nc-online" :class="row.online ? 'online' : 'offline'">
              <i class="np-dot" :class="row.online ? 'online' : 'offline'"></i>
              {{ row.online ? '在线' : '离线' }}
            </span>
          </div>
          <div class="nc-row">
            <span class="nc-label">地区</span>
            <span class="nc-value">{{ row.country_code || '-' }}</span>
          </div>
          <div class="nc-row">
            <span class="nc-label">地址</span>
            <span class="nc-value nc-mono">{{ row.server_address }}:{{ row.port }}</span>
          </div>
          <div class="nc-row nc-plans">
            <span class="nc-label">套餐</span>
            <div v-if="row.plan_ids && row.plan_ids.length" class="nc-tags">
              <el-tag v-for="pid in row.plan_ids" :key="pid" size="small" effect="plain" type="success">{{ planName(pid) }}</el-tag>
            </div>
            <span v-else class="nc-value nc-muted">未绑定</span>
          </div>
          <div class="nc-load" v-if="row.runtime && row.runtime.updated_at > 0">
            <div class="nc-row">
              <span class="nc-label">CPU</span>
              <div class="rt-bar">
                <div class="rt-bar-inner" :style="{ width: Math.min(100, row.runtime.cpu_usage || 0) + '%', background: loadColor(row.runtime.cpu_usage) }"></div>
              </div>
              <span class="nc-value">{{ (row.runtime.cpu_usage || 0).toFixed(1) }}%</span>
            </div>
            <div class="nc-row">
              <span class="nc-label">内存</span>
              <div class="rt-bar">
                <div class="rt-bar-inner" :style="{ width: Math.min(100, row.runtime.memory_usage || 0) + '%', background: loadColor(row.runtime.memory_usage) }"></div>
              </div>
              <span class="nc-value">{{ (row.runtime.memory_usage || 0).toFixed(1) }}%</span>
            </div>
            <div class="nc-row">
              <span class="nc-label">连接</span>
              <span class="nc-value">{{ row.runtime.online_connections || 0 }}</span>
              <span class="nc-label" style="margin-left:12px">速度</span>
              <span class="nc-value">{{ formatSpeed(row.runtime.speed_bps) }}</span>
            </div>
          </div>
          <div class="nc-row nc-load-empty" v-else>
            <span class="nc-label">负载</span>
            <span class="nc-value nc-muted">无数据</span>
          </div>
          <div class="nc-actions">
            <el-button size="small" type="primary" plain @click="openDialog(row)">编辑</el-button>
            <el-dropdown trigger="click" @command="(cmd: string) => handleRowAction(cmd, row)">
              <el-button size="small" plain>更多<el-icon class="el-icon--right"><ArrowDown /></el-icon></el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="deploy">一键部署</el-dropdown-item>
                  <el-dropdown-item command="deployInfo">部署信息</el-dropdown-item>
                  <el-dropdown-item command="rotateToken">轮换Token</el-dropdown-item>
                  <el-dropdown-item command="ping" :disabled="pingLoading.get(row.id)||false">
                    {{ pingLoading.get(row.id) ? '检测中...' : (row.online ? '重新检测' : '检测连接') }}
                  </el-dropdown-item>
                  <el-dropdown-item command="delete" divided>删除</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </div>
        <el-empty v-if="!loading && !filteredList.length" description="暂无节点" />
      </div>
    </div>

    <!-- 新增/编辑对话框 -->
    <el-dialog
      v-model="dialogVisible"
      :title="editing ? '编辑节点' : '新增节点'"
      :width="dialogWidth"
      destroy-on-close
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="100px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="如：美国-节点1" />
        </el-form-item>
        <el-form-item label="地区代码" prop="country_code">
          <el-input v-model="form.country_code" placeholder="如：US / HK / JP" style="width: 120px" />
        </el-form-item>
        <el-form-item label="协议" prop="protocol">
          <el-select v-model="form.protocol" style="width: 100%">
            <!-- 当前后端 buildXrayConfig 仅支持 VLESS+REALITY，暂只开放该选项 -->
            <el-option label="VLESS (推荐)" value="vless" />
          </el-select>
          <div class="form-tip">当前仅支持 VLESS+REALITY，其他协议后续版本开放。</div>
        </el-form-item>
        <el-form-item label="服务器IP" prop="server_address">
          <el-input v-model="form.server_address" placeholder="节点服务器的公网IP" />
        </el-form-item>
        <el-form-item label="端口" prop="port">
          <el-input-number v-model="form.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
        </el-form-item>
        <el-form-item label="绑定套餐" prop="plan_ids">
          <el-select v-model="form.plan_ids" multiple filterable style="width: 100%" placeholder="至少选择一个套餐">
            <el-option
              v-for="p in planList"
              :key="p.id"
              :label="`${p.name}（流量: ${p.traffic_limit ? formatTraffic(p.traffic_limit) : '不限'} / ${p.duration_days}天）`"
              :value="p.id"
            />
          </el-select>
          <div style="font-size:12px;color:#909399">绑定后，购买这些套餐的用户才能使用此节点。至少选择一个</div>
        </el-form-item>
        <el-form-item label="流量上限(GB)">
          <el-input-number v-model="form.trafficLimitGB" :min="0" :precision="2" controls-position="right" style="width: 100%" />
          <div style="font-size:12px;color:#909399">0 = 不限</div>
        </el-form-item>
        <el-divider content-position="left">节点策略控制（可选）</el-divider>
        <el-form-item label="最大用户数">
          <el-input-number v-model="form.maxClients" :min="0" controls-position="right" style="width:100%" />
          <div style="font-size:12px;color:#909399">0=不限。超过此数量新用户不会下发到此节点，已连接用户超额时自动踢出最后加入的用户</div>
        </el-form-item>
        <el-form-item label="带宽上限(Mbps)">
          <el-input-number v-model="form.maxBandwidthMbps" :min="0" controls-position="right" style="width:100%" />
          <div style="font-size:12px;color:#909399">0=不限。节点总带宽上限，用于负载评分计算</div>
        </el-form-item>
        <el-form-item label="CPU阈值(%)">
          <el-input-number v-model="form.cpuThreshold" :min="1" :max="100" controls-position="right" style="width:100%" />
          <div style="font-size:12px;color:#909399">CPU超过此阈值视为满载，默认80</div>
        </el-form-item>
        <el-form-item label="动态限速">
          <el-switch v-model="form.dynamicLimit" active-text="开启" inactive-text="关闭" />
          <div style="font-size:12px;color:#909399">
            开启后系统自动检测节点负载并动态调整单用户限速：<br/>
            • 空闲：15 Mbps（1080P流畅，4K看不了）<br/>
            • 正常：12 Mbps（1080P底线）<br/>
            • 繁忙：12 Mbps（仍保1080P底线，不降）<br/>
            • 满载：5 Mbps（降级保720P+聊天）<br/>
            底线保证每人能看1080P视频，只有CPU满载才降级
          </div>
        </el-form-item>
        <el-divider content-position="left">一键自动部署（可选）</el-divider>
        <el-form-item label="SSH 密码">
          <el-input v-model="form.sshPassword" type="password" show-password placeholder="留空则不自动部署，后续可点「部署」按钮" autocomplete="new-password" name="ssh-deploy-pwd" />
          <div style="font-size:12px;color:#909399">填写后，创建节点将自动 SSH 推送文件并启动 node-agent</div>
        </el-form-item>
        <el-form-item label="SSH 端口">
          <el-input-number v-model="form.sshPort" :min="1" :max="65535" controls-position="right" style="width: 100%" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSave">保存</el-button>
      </template>
    </el-dialog>

    <!-- 一键自动部署进度弹窗 -->
    <DeployProgress
      v-model="deployProgressVisible"
      :nodeId="deployProgressNodeId"
      :prePassword="deployProgressPassword"
      :preUsername="deployProgressUsername"
      :prePort="deployProgressPort"
      @done="fetchList"
    />

    <!-- 清理并删除节点进度弹窗 (SSE 流式, 自动 SSH 清理节点服务器) -->
    <CleanupProgress
      v-model="cleanupProgressVisible"
      :nodeId="cleanupProgressNodeId"
      :nodeName="cleanupProgressNodeName"
      @done="fetchList"
    />

    <!-- 部署信息对话框(创建节点后展示 node_token 等) -->
    <el-dialog v-model="deployVisible" title="节点部署信息" :width="deployDialogWidth" top="5vh" class="deploy-dialog">
      <el-alert
        type="warning"
        :closable="false"
        show-icon
        style="margin-bottom: 16px"
      >
        <template #title>请妥善保管以下信息，node_token 是节点服务器连接面板的凭证，切勿泄露。</template>
      </el-alert>

      <el-descriptions :column="1" border size="small">
        <el-descriptions-item label="节点ID">{{ deployData.id }}</el-descriptions-item>
        <el-descriptions-item label="节点名称">{{ deployData.name }}</el-descriptions-item>
        <el-descriptions-item label="node_token">
          <div style="display:flex;align-items:center;gap:8px">
            <code style="flex:1;word-break:break-all;background:#f5f7fa;padding:4px 8px;border-radius:4px;font-size:12px">
              {{ deployData.node_token }}
            </code>
            <el-button size="small" link @click="copyText(deployData.node_token)">
              <el-icon><CopyDocument /></el-icon> 复制
            </el-button>
          </div>
        </el-descriptions-item>
        <el-descriptions-item label="REALITY 公钥" v-if="deployData.public_key">
          <div style="display:flex;align-items:center;gap:8px">
            <code style="flex:1;word-break:break-all;background:#f5f7fa;padding:4px 8px;border-radius:4px;font-size:12px">
              {{ deployData.public_key }}
            </code>
            <el-button size="small" link @click="copyText(deployData.public_key)">
              <el-icon><CopyDocument /></el-icon> 复制
            </el-button>
          </div>
        </el-descriptions-item>
        <el-descriptions-item label="Short ID" v-if="deployData.short_id">
          <code style="background:#f5f7fa;padding:4px 8px;border-radius:4px;font-size:12px">{{ deployData.short_id }}</code>
        </el-descriptions-item>
      </el-descriptions>

      <!-- Tabs：部署步骤 / SSH 终端 -->
      <el-tabs v-model="deployTab" style="margin-top: 16px">
        <el-tab-pane label="📋 部署步骤" name="steps">
          <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px">
            <span style="font-size:13px;color:#606266">每条命令可单独复制，或一键复制全部</span>
            <el-button size="small" type="primary" plain @click="copyAllSteps">
              <el-icon><CopyDocument /></el-icon> 一键复制全部命令
            </el-button>
          </div>
          <deploy-steps-viewer :steps="deployData.steps" />
        </el-tab-pane>
        <el-tab-pane label="💻 SSH 终端" name="terminal">
          <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;flex-wrap:wrap;gap:8px">
            <span style="font-size:13px;color:#606266">输入节点服务器密码，连接后直接复制上方命令粘贴执行</span>
          <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
            <el-input
              v-model="termPassword"
              size="small"
              type="password"
              show-password
              placeholder="节点服务器 root 密码"
              style="width:180px"
              :disabled="termStatus === 'connecting' || termStatus === 'connected'"
            />
            <el-input
              v-model="termUser"
              size="small"
              placeholder="用户"
              style="width:90px"
              :disabled="termStatus === 'connecting' || termStatus === 'connected'"
            />
            <el-button
              v-if="termStatus !== 'connected' && termStatus !== 'connecting'"
              size="small"
              type="primary"
              :disabled="!termPassword"
              @click="connectTerminal"
            >连接节点</el-button>
            <el-button
              v-else
              size="small"
              type="danger"
              plain
              @click="disconnectTerminal"
            >断开</el-button>
          </div>
        </div>
          <el-alert
            type="info"
            :closable="false"
            show-icon
            style="margin-bottom:10px;font-size:12px"
            v-if="!deployData.node_token"
          >
            <template #title>先创建节点或选择节点获取部署信息后，再连接终端</template>
          </el-alert>
          <web-terminal
            v-else
            ref="termRef"
            :wsUrl="termWsUrl"
            :password="termPassword"
            :username="termUser"
            :port="22"
            :title="`${deployData.name} (${deployData.serverAddress})`"
            :autoConnect="false"
            @status="onTermStatus"
          />
        </el-tab-pane>
      </el-tabs>

      <template #footer>
        <el-button type="primary" @click="onDeployClose">知道了</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, h, defineComponent } from 'vue'
import { ElMessage, ElMessageBox, ElIcon, type FormInstance, type FormRules } from 'element-plus'
import { Search, Plus, CopyDocument, ArrowDown } from '@element-plus/icons-vue'
import WebTerminal from '@/components/WebTerminal.vue'
import DeployProgress from '@/components/DeployProgress.vue'
import CleanupProgress from '@/components/CleanupProgress.vue'
import request from '@/utils/request'
import { formatTraffic } from '@/utils/format'

// 移动端检测: <768px 切换为卡片视图, 避免表格水平滚动看不到列
const isMobile = ref(false)
const checkMobile = () => { isMobile.value = window.innerWidth < 768 }
const dialogWidth = computed(() => (isMobile.value ? '92%' : '560px'))
const deployDialogWidth = computed(() => (isMobile.value ? '95%' : '820px'))

// 后端响应格式: { code: 0, msg: "ok", data: ... }
interface ApiResponse<T> { code: number; msg: string; data: T }
interface NodeRuntime {
  cpu_usage: number
  memory_usage: number
  memory_total: number
  online_connections: number
  speed_bps: number
  uptime_seconds: number
  updated_at: number
}
interface TrafficGroup {
  server_address: string
  node_count: number
  traffic_used: number
  traffic_limit: number
}
interface PlanOption {
  id: string
  name: string
  traffic_limit: number
  duration_days: number
}
interface NodeRow {
  id: string
  name: string
  country_code: string
  protocol: string
  server_address: string
  port: number
  grpc_port: number
  traffic_limit: number
  traffic_used: number
  is_enabled: boolean
  online: boolean
  node_token: string
  server_config: string | Record<string, any>
  version: string
  plan_ids: string[]
  runtime: NodeRuntime
  // 节点策略控制字段
  max_clients: number
  max_bandwidth_mbps: number
  cpu_threshold: number
  usage_type: string
  // 节点实时负载状态: idle/normal/busy/full
  load_status: string
  [k: string]: any
}

// 修复 P0-FE3: 旧版硬编码 PANEL_IP = '177.3.32.94', 多环境部署(开发/测试/生产)都指向同一台,
// 节点 agent 连不上正确面板。改为从 Vite 环境变量读取, 缺省回退到当前域名。
// - 开发: 在 frontend/.env 中设置 VITE_PANEL_IP=192.168.x.x
// - 生产: 不配置则自动用 window.location.hostname(节点通过浏览器访问的同一面板域名)
//
// P2-16: 注意 CDN 场景限制 — 若面板域名走 Cloudflare 等 CDN, hostname 解析到 CDN IP,
// 节点 agent 无法通过 CDN 连接 50051 端口。此场景必须配置 VITE_PANEL_IP 为面板真实 IP,
// 或在面板 .env 设置 PANEL_GRPC_HOST 后端变量由 auto_deploy.go 注入到 .env.node。
const PANEL_IP = import.meta.env.VITE_PANEL_IP || window.location.hostname

// 部署步骤数据结构
interface DeployCommand {
  label: string
  cmd: string
}
interface DeployStep {
  no: number
  title: string
  host: string
  target?: string
  desc: string
  commands: DeployCommand[]
  tip?: string
  expected?: string
}

// 构建部署步骤（根据节点信息替换变量）
// 多节点支持: 按节点 ID 前 8 位区分容器名和部署目录
const buildDeploySteps = (node: { id?: string; server_address: string; node_token: string; port: number }): DeployStep[] => {
  const nodeIP = node.server_address
  const token = node.node_token
  const port = node.port || 443
  // 按节点 ID 前 8 位区分容器和目录，同一台服务器可部署多个节点
  const shortID = node.id ? node.id.substring(0, 8) : 'demo0001'
  const containerName = `nexus-agent-${shortID}`
  const deployDir = `/root/node-agent-${shortID}`
  return [
    {
      no: 1,
      title: '推送 node_agent 到节点服务器',
      host: '面板服务器',
      target: '节点服务器',
      desc: 'node_agent 是节点代理程序源码，位于面板服务器项目根目录的 node_agent 子目录。此步骤把它通过 scp 传到节点服务器，无需手动下载。',
      commands: [
        {
          label: '推送 node_agent 目录到节点（会提示输入节点 root 密码，请替换为你的面板项目路径）',
          cmd: `scp -r /path/to/nexus-panel/node_agent root@${nodeIP}:${deployDir}`,
        },
        {
          label: '顺便在节点服务器一键安装 Docker（若已安装可跳过）',
          cmd: `ssh root@${nodeIP} 'curl -fsSL https://get.docker.com | sh && systemctl enable docker && systemctl start docker'`,
        },
      ],
      tip: `执行完后，节点服务器的 ${deployDir} 目录会有 docker-compose.node.yml 等部署文件。同一台服务器可部署多个节点，每个节点独立目录。`,
    },
    {
      no: 2,
      title: 'SSH 登录到节点服务器',
      host: '面板服务器',
      target: '节点服务器',
      desc: '后续操作全部在节点服务器上执行。先 SSH 登录上去。',
      commands: [
        { label: '登录节点服务器', cmd: `ssh root@${nodeIP}` },
      ],
    },
    {
      no: 3,
      title: '创建环境配置文件 .env.node',
      host: '节点服务器',
      desc: '在节点服务器上创建配置文件，变量已根据本节点信息自动替换好，直接复制执行即可。',
      commands: [
        { label: '进入节点部署目录', cmd: `cd ${deployDir}` },
        {
          label: '创建 .env.node 配置文件',
          cmd: `cat > .env.node <<EOF
CONTAINER_NAME=${containerName}
PANEL_GRPC_ADDR=${PANEL_IP}:50051
NODE_TOKEN=${token}
LISTEN_PORT=${port}
HEALTH_PORT=50052
XRAY_VERSION=v1.8.23
# 面板启用 gRPC TLS 时取消下行注释(公信 CA 如 Let's Encrypt 用系统证书即可)
#GRPC_TLS_CA=/etc/ssl/certs/ca-certificates.crt
EOF`,
        },
      ],
      tip: `CONTAINER_NAME=${containerName}，每节点独立容器名，同一台服务器可部署多个节点互不干扰。NODE_TOKEN 是本节点的专属凭证，切勿混用。`,
    },
    {
      no: 4,
      title: '启动 node-agent 容器',
      host: '节点服务器',
      desc: '首次启动会自动构建镜像并下载 Xray-core，约 30~60 秒。',
      commands: [
        {
          label: '构建并启动',
          cmd: `docker compose -f docker-compose.node.yml --env-file .env.node up -d --build`,
        },
      ],
    },
    {
      no: 5,
      title: '查看日志，验证连接面板成功',
      host: '节点服务器',
      desc: '查看运行日志，确认节点已注册到面板并启动 Xray 代理服务。',
      commands: [
        { label: '实时查看日志（Ctrl+C 退出）', cmd: `docker logs -f ${containerName}` },
        { label: '查看容器运行状态', cmd: `docker ps | grep ${containerName}` },
      ],
      expected: '日志看到 "已注册到面板" "Xray 启动成功" 即为正常；此时回到面板节点列表，该节点应显示「在线」。',
    },
  ]
}



// 局部组件：渲染步骤列表（弹窗和指南共用）
const DeployStepsViewer = defineComponent({
  name: 'DeployStepsViewer',
  props: {
    steps: { type: Array as () => DeployStep[], required: true },
  },
  setup(props) {
    const copy = (text: string) => {
      navigator.clipboard.writeText(text).then(() => {
        ElMessage.success('已复制到剪贴板')
      }).catch(() => {
        ElMessage.warning('复制失败，请手动选中复制')
      })
    }
    return () => props.steps.map((step) =>
      h('div', { class: 'deploy-step' }, [
        h('div', { class: 'step-head' }, [
          h('span', { class: 'step-no' }, String(step.no)),
          h('span', { class: 'step-title' }, step.title),
        ]),
        h('div', { class: 'step-exec-bar' }, [
          h('span', { class: 'step-exec-icon' }, '📍'),
          h('span', { class: 'step-exec-text' }, `此命令在【${step.host}】执行`),
          step.target && step.target !== step.host
            ? h('span', { class: 'step-flow' }, `📤 ${step.host} → ${step.target}`)
            : null,
        ]),
        h('div', { class: 'step-desc' }, step.desc),
        ...step.commands.map((c) =>
          h('div', { class: 'cmd-block' }, [
            h('div', { class: 'cmd-label' }, c.label),
            h('div', { class: 'cmd-row' }, [
              h('code', { class: 'cmd-text' }, c.cmd),
              h('button', {
                class: 'cmd-copy-btn',
                title: '复制命令',
                onClick: () => copy(c.cmd),
              }, [
                h(ElIcon, null, () => h(CopyDocument)),
              ]),
            ]),
          ]),
        ),
        step.tip ? h('div', { class: 'step-tip' }, `💡 ${step.tip}`) : null,
        step.expected ? h('div', { class: 'step-expected' }, `✅ 预期：${step.expected}`) : null,
      ]),
    )
  },
})

const loading = ref(false)
const saving = ref(false)
const keyword = ref('')
const deployTab = ref('steps')
const trafficSummaryActive = ref<string[]>([])
const list = ref<NodeRow[]>([])
const planList = ref<PlanOption[]>([])
const trafficGroups = ref<TrafficGroup[]>([])

const filteredList = computed(() => {
  if (!keyword.value) return list.value
  const k = keyword.value.toLowerCase()
  return list.value.filter(
    (n) =>
      (n.name || '').toLowerCase().includes(k) ||
      (n.server_address || '').toLowerCase().includes(k),
  )
})

type TagType = 'primary' | 'success' | 'info' | 'warning' | 'danger'
const protocolTagType = (p: string): TagType => {
  const map: Record<string, TagType> = { vless: 'success', vmess: 'primary', trojan: 'warning', shadowsocks: 'info' }
  return map[p] || 'primary'
}

// 节点负载状态: idle 空闲 / normal 正常 / busy 繁忙 / full 满载
const loadStatusText = (s: string) => {
  const map: Record<string, string> = { idle: '空闲', normal: '正常', busy: '繁忙', full: '满载' }
  return map[s] || '空闲'
}
const loadStatusTagType = (s: string): any => {
  const map: Record<string, string> = { idle: 'success', normal: '', busy: 'warning', full: 'danger' }
  return map[s] || 'success'
}
// 节点动态限速状态: limited 开启 / general 关闭
const usageText = (t: string) => (t === 'limited' ? '动态限速' : '不限速')
const usageTagType = (t: string): any => (t === 'limited' ? 'warning' : 'info')

// 套餐名称查找(用于表格显示绑定的套餐名)
const planName = (pid: string): string => {
  const p = planList.value.find((x) => x.id === pid)
  return p ? p.name : pid.substring(0, 8)
}

// 实时速度格式化: bytes/s → KB/s 或 MB/s
const formatSpeed = (bps: number): string => {
  if (!bps || bps <= 0) return '0 B/s'
  if (bps < 1024) return `${bps} B/s`
  if (bps < 1024 * 1024) return `${(bps / 1024).toFixed(1)} KB/s`
  return `${(bps / 1024 / 1024).toFixed(2)} MB/s`
}

// 负载颜色: <70% 绿, 70-90% 橙, >=90% 红
const loadColor = (val: number): string => {
  if (val >= 90) return '#f56c6c'
  if (val >= 70) return '#e6a23c'
  return '#67c23a'
}

// 服务器流量汇总百分比与颜色
const groupPercent = (g: TrafficGroup): number => {
  if (!g.traffic_limit || g.traffic_limit === 0) return 0
  return Math.min(100, Math.round((g.traffic_used / g.traffic_limit) * 1000) / 10)
}
const groupColor = (g: TrafficGroup): string => {
  const pct = groupPercent(g)
  if (pct >= 90) return '#f56c6c'
  if (pct >= 70) return '#e6a23c'
  return '#67c23a'
}

// 对话框
const dialogVisible = ref(false)
const editing = ref<NodeRow | null>(null)
const formRef = ref<FormInstance>()
const form = reactive({
  name: '',
  country_code: 'US',
  protocol: 'vless',
  server_address: '',
  port: 443,
  grpc_port: 50051,
  plan_ids: [] as string[],
  trafficLimitGB: 0,
  maxClients: 0,
  maxBandwidthMbps: 0,
  cpuThreshold: 80,
  dynamicLimit: false,
  sshPassword: '',
  sshPort: 22,
})
const rules: FormRules = {
  name: [{ required: true, message: '请输入节点名称', trigger: 'blur' }],
  protocol: [{ required: true, message: '请选择协议', trigger: 'change' }],
  server_address: [{ required: true, message: '请输入服务器IP', trigger: 'blur' }],
  port: [{ required: true, message: '请输入端口', trigger: 'blur' }],
  plan_ids: [{ required: true, type: 'array', min: 1, message: '请至少选择一个套餐', trigger: 'change' }],
}

const openDialog = (row?: NodeRow) => {
  editing.value = row || null
  if (row) {
    Object.assign(form, {
      name: row.name,
      country_code: row.country_code || 'US',
      protocol: row.protocol,
      server_address: row.server_address,
      port: row.port,
      // 回填 grpc_port, 避免保存时被硬编码 50051 覆盖导致 agent 连不上面板
      grpc_port: row.grpc_port || 50051,
      plan_ids: row.plan_ids ? [...row.plan_ids] : [],
      trafficLimitGB: row.traffic_limit ? Math.round(row.traffic_limit / 1024 / 1024 / 1024 * 100) / 100 : 0,
      maxClients: row.max_clients || 0,
      maxBandwidthMbps: row.max_bandwidth_mbps || 0,
      cpuThreshold: row.cpu_threshold || 80,
      dynamicLimit: row.usage_type === "limited",
      // 安全: 编辑节点时清空密码, 避免上次添加节点时的密码缓存
      sshPassword: '',
      sshPort: 22,
    })
  } else {
    Object.assign(form, {
      name: '',
      country_code: 'US',
      protocol: 'vless',
      server_address: '',
      port: 443,
      grpc_port: 50051,
      plan_ids: [],
      trafficLimitGB: 0,
      maxClients: 0,
      maxBandwidthMbps: 0,
      cpuThreshold: 80,
      dynamicLimit: false,
      sshPassword: '',
      sshPort: 22,
    })
  }
  dialogVisible.value = true
}

const handleSave = async () => {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    saving.value = true
    try {
      const payload: any = {
        name: form.name,
        country_code: form.country_code,
        protocol: form.protocol,
        server_address: form.server_address,
        port: form.port,
        grpc_port: form.grpc_port,
        plan_ids: form.plan_ids,
        // 修复 P1: 保存用 Math.round 与编辑回填一致, Math.floor 会向下漂移
        traffic_limit: Math.round(form.trafficLimitGB * 1024 * 1024 * 1024),
        // 节点策略控制字段
        max_clients: form.maxClients,
        max_bandwidth_mbps: form.maxBandwidthMbps,
        cpu_threshold: form.cpuThreshold,
        usage_type: form.dynamicLimit ? "limited" : "general",
      }
      if (editing.value) {
        // 编辑模式: 不发送 extra_config, 避免覆盖节点原有 REALITY dest/sni 配置
        await request.put(`/api/v1/admin/nodes/${editing.value.id}`, payload)
        ElMessage.success('节点已更新')
        dialogVisible.value = false
        await fetchList()
      } else {
        // 创建模式: 发送默认 REALITY 配置
        payload.extra_config = {
          reality: { dest: 'gateway.icloud.com:443', sni: 'gateway.icloud.com' },
        }
        const res = await request.post<ApiResponse<NodeRow>>('/api/v1/admin/nodes', payload)
        if (res && res.code === 0 && res.data) {
          ElMessage.success('节点已创建')
          dialogVisible.value = false
          await fetchList()
          if (form.sshPassword) {
            startAutoDeploy(res.data, form.sshPassword, 'root', form.sshPort)
          } else {
            showDeployInfo(res.data)
          }
        } else {
          ElMessage.error((res as any)?.msg || '创建失败')
        }
      }
    } catch (e: any) {
      // 错误已由拦截器提示
    } finally {
      saving.value = false
    }
  })
}

// ---- 清理并删除节点 (SSE 流式进度) ----
const cleanupProgressVisible = ref(false)
const cleanupProgressNodeId = ref('')
const cleanupProgressNodeName = ref('')

const handleDelete = (row: NodeRow) => {
  ElMessageBox.confirm(
    `确定删除节点「${row.name}」吗？\n\n将自动 SSH 清理节点服务器残留资源(Docker 容器/部署目录/镜像), 确保无残留。\n如节点服务器不可达, 会跳过物理清理仅删面板记录。`,
    '删除节点',
    {
      type: 'warning',
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
    },
  ).then(() => {
    // [P1-删除审计] 统一走 cleanup 流程, 移除"仅删除"分支:
    // 旧版"仅删除"只删面板 DB+Redis, 节点服务器的 agent 容器/部署目录/镜像全部残留,
    // 旧 agent 用失效 token 注册失败 30 次 → log.Fatalf → restart=unless-stopped 死循环刷日志
    cleanupProgressNodeId.value = row.id
    cleanupProgressNodeName.value = row.name
    cleanupProgressVisible.value = true
  }).catch(() => {})
}

const rotateToken = (row: NodeRow) => {
  ElMessageBox.confirm(`确定轮换节点「${row.name}」的通信Token吗？轮换后旧Token失效，需重新部署 node-agent。`, '轮换Token', {
    type: 'warning',
    confirmButtonText: '轮换',
    cancelButtonText: '取消',
  }).then(async () => {
    try {
      const res = await request.post<ApiResponse<{ node_token: string }>>(`/api/v1/admin/nodes/${row.id}/rotate-token`)
      if (res && res.code === 0 && res.data) {
        ElMessage.success('Token 已轮换')
        await fetchList()
        // 修复 P1: 轮换后旧 agent 仍用旧 token, 会注册失败 30 次 -> log.Fatalf -> docker restart 死循环
        // 必须重新部署 agent 更新配置文件的 NODE_TOKEN, 强制弹窗让用户选择部署方式
        ElMessageBox.confirm(
          `⚠️ 节点「${row.name}」的 Token 已轮换\n\n节点服务器上的旧 agent 仍在使用旧 Token，将持续注册失败并死循环重启。\n\n必须立即重新部署 node-agent 以更新配置文件中的 NODE_TOKEN。`,
          '必须重新部署 node-agent',
          {
            type: 'error',
            confirmButtonText: '一键重新部署',
            cancelButtonText: '查看手动命令',
            distinguishCancelAndClose: true,
          },
        ).then(() => {
          // 一键重新部署: 走 auto-deploy 流程(会弹窗收集 SSH 密码)
          startAutoDeploy({ ...row, node_token: res.data.node_token })
        }).catch((action: string) => {
          if (action === 'cancel') {
            // 查看手动部署命令
            showDeployInfo({ ...row, node_token: res.data.node_token })
          }
        })
      }
    } catch { /* */ }
  }).catch(() => {})
}

// ---- 部署信息对话框 ----
const deployVisible = ref(false)
const deployData = reactive<{
  id: string
  name: string
  serverAddress: string
  node_token: string
  public_key: string
  short_id: string
  steps: DeployStep[]
}>({
  id: '',
  name: '',
  serverAddress: '',
  node_token: '',
  public_key: '',
  short_id: '',
  steps: [],
})

// 从 server_config 解析 public_key / short_id
const parseRealityInfo = (node: NodeRow): { public_key: string; short_id: string } => {
  let sc = node.server_config
  if (typeof sc === 'string') {
    try { sc = JSON.parse(sc) } catch { sc = {} }
  }
  const reality = (sc as any)?.reality || {}
  return {
    public_key: reality.public_key || '',
    short_id: reality.short_id || '',
  }
}

// 部署信息加载状态(列表点击时需调 NodeDetail 获取 node_token)
const deployLoading = ref(false)

// 渲染部署信息到对话框(从节点对象填充 deployData)
const renderDeployInfo = (node: NodeRow) => {
  const { public_key, short_id } = parseRealityInfo(node)
  deployData.id = node.id
  deployData.name = node.name
  deployData.serverAddress = node.server_address
  deployData.node_token = node.node_token
  deployData.public_key = public_key
  deployData.short_id = short_id
  deployData.steps = buildDeploySteps({
    id: node.id,
    server_address: node.server_address,
    node_token: node.node_token,
    port: node.port,
  })
  deployTab.value = 'steps'
  deployVisible.value = true
}

// 显示部署信息
// 修复 P1: NodeList 已隐藏 node_token 防泄露, 列表点击时 row 无 node_token,
// 需调 NodeDetail 获取完整信息。否则部署步骤里 NODE_TOKEN=undefined,
// agent 启动后注册失败 30 次 -> log.Fatalf -> docker restart 死循环刷日志
const showDeployInfo = async (node: NodeRow) => {
  // 已有 node_token(创建后/轮换后调用), 直接渲染
  if (node.node_token) {
    renderDeployInfo(node)
    return
  }
  // 列表点击: node_token 为空, 调 NodeDetail 获取
  if (deployLoading.value) return
  deployLoading.value = true
  try {
    const res = await request.get<ApiResponse<NodeRow>>(`/api/v1/admin/nodes/${node.id}`)
    if (res && res.code === 0 && res.data) {
      renderDeployInfo(res.data)
    } else {
      ElMessage.error('获取节点详情失败')
    }
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.msg || '获取节点详情失败')
  } finally {
    deployLoading.value = false
  }
}

// 一键复制全部命令
// ---- Web 终端相关 ----
const termRef = ref()
const termPassword = ref('')
const termUser = ref('root')
const termStatus = ref<'disconnected' | 'connecting' | 'connected' | 'error'>('disconnected')
const termWsUrl = computed(() => {
  if (!deployData.id) return ''
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}/api/v1/admin/nodes/${deployData.id}/terminal`
})
const onTermStatus = (st: 'disconnected' | 'connecting' | 'connected' | 'error') => {
  termStatus.value = st
}
const connectTerminal = () => {
  termRef.value && termRef.value.connect()
}
const disconnectTerminal = () => {
  termRef.value && termRef.value.disconnect()
}
// ---- 一键自动部署 ----
const deployProgressVisible = ref(false)
const deployProgressNodeId = ref('')
const deployProgressPassword = ref('')
const deployProgressUsername = ref('root')
const deployProgressPort = ref(22)
const startAutoDeploy = (node: NodeRow, password?: string, username?: string, port?: number) => {
  deployProgressNodeId.value = node.id
  deployProgressPassword.value = password || ''
  deployProgressUsername.value = username || 'root'
  deployProgressPort.value = port || 22
  deployProgressVisible.value = true
}

const onDeployClose = () => {
  disconnectTerminal()
  deployVisible.value = false
}

const copyText = (text: string) => {
  navigator.clipboard.writeText(text).then(() => {
    ElMessage.success('已复制到剪贴板')
  }).catch(() => {
    ElMessage.warning('复制失败，请手动选中复制')
  })
}

const copyAllSteps = () => {
  const all = deployData.steps
    .map((s) => {
      const cmds = s.commands.map((c) => c.cmd).join('\n')
      return `# 步骤 ${s.no}: ${s.title} (在${s.host}执行)\n${cmds}`
    })
    .join('\n\n')
  copyText(all)
}

const fetchList = async () => {
  loading.value = true
  try {
    const res = await request.get<ApiResponse<{ list: NodeRow[]; total: number; traffic_groups: TrafficGroup[] }>>('/api/v1/admin/nodes')
    if (res && res.code === 0 && res.data) {
      list.value = res.data.list || []
      trafficGroups.value = res.data.traffic_groups || []
    }
  } catch {
    // 错误由拦截器处理
  } finally {
    loading.value = false
  }
}

// 主动检测节点连接: TCP 探测节点 gRPC 端口，立即确认在线状态
const pingLoading = ref<Map<string, boolean>>(new Map())
const pingNode = async (row: NodeRow) => {
  pingLoading.value.set(row.id, true)
  try {
    const res = await request.post<ApiResponse<{ reachable: boolean; error?: string; action: string; checked_at: string }>>(`/api/v1/admin/nodes/${row.id}/ping`)
    if (res && res.code === 0 && res.data) {
      if (res.data.reachable) {
        ElMessage.success(`${row.name} 连接正常，在线状态已刷新`)
      } else {
        ElMessage.warning(`${row.name} 无法连接 (${res.data.error || '未知错误'})，已标记为离线`)
      }
      // P2-10: 局部更新当前行 online 状态, 避免全量 fetchList 浪费请求
      row.online = res.data.reachable
    }
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.msg || '检测失败')
  } finally {
    pingLoading.value.set(row.id, false)
  }
}

// 操作列 dropdown 命令分发
const handleRowAction = (cmd: string, row: NodeRow) => {
  switch (cmd) {
    case 'deploy': startAutoDeploy(row); break
    case 'deployInfo': showDeployInfo(row); break
    case 'rotateToken': rotateToken(row); break
    case 'ping': pingNode(row); break
    case 'delete': handleDelete(row); break
  }
}

// 获取套餐列表(用于节点绑定时选择)
const fetchPlans = async () => {
  try {
    const res = await request.get<ApiResponse<{ list: PlanOption[]; total: number }>>('/api/v1/admin/plans')
    if (res && res.code === 0 && res.data) {
      planList.value = res.data.list || []
    }
  } catch {
    // 静默失败
  }
}

onMounted(() => {
  checkMobile()
  window.addEventListener('resize', checkMobile)
  fetchList()
  fetchPlans()
})
onUnmounted(() => {
  window.removeEventListener('resize', checkMobile)
})
</script>

<style scoped>
.page-card { padding: 20px; }
.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 20px;
  flex-wrap: wrap;
  gap: 12px;
}
.page-title { margin: 0; font-size: 18px; color: var(--np-text); }
.page-desc { margin: 6px 0 0; font-size: 13px; color: var(--np-text-secondary); }
.header-actions { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.form-tip { margin-top: 6px; font-size: 12px; color: var(--np-text-secondary); line-height: 1.4; }

/* 节点流量使用展示 */
.traffic-cell { display: flex; flex-direction: column; gap: 2px; }
.traffic-cell .traffic-used { font-weight: 600; }
.traffic-bar {
  width: 100%;
  height: 4px;
  background: #ebeef5;
  border-radius: 2px;
  overflow: hidden;
  margin: 2px 0;
}
.traffic-bar-inner {
  height: 100%;
  border-radius: 2px;
  transition: width 0.3s;
}

/* 服务器流量汇总面板 */
.traffic-summary-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
}
.traffic-summary-card {
  border: 1px solid #e4e7ed;
  border-radius: 8px;
  padding: 12px 14px;
  background: #fafbfc;
}
.ts-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}
.ts-ip { font-size: 14px; font-weight: 600; color: #303133; font-family: monospace; }
.ts-body { display: flex; align-items: baseline; gap: 4px; margin-bottom: 6px; }
.ts-used { font-size: 18px; font-weight: 700; color: #409eff; }
.ts-limit { font-size: 12px; color: #909399; }
.ts-bar {
  width: 100%;
  height: 6px;
  background: #ebeef5;
  border-radius: 3px;
  overflow: hidden;
  margin: 4px 0;
}
.ts-bar-inner {
  height: 100%;
  border-radius: 3px;
  transition: width 0.3s;
}
.ts-remain { font-size: 11px; color: #909399; }

/* 实时负载单元格 */
.runtime-cell { display: flex; flex-direction: column; gap: 3px; }
.rt-row { display: flex; align-items: center; gap: 4px; font-size: 12px; }
.rt-label { color: #909399; min-width: 28px; }
.rt-value { color: #303133; font-weight: 500; }
.rt-bar {
  flex: 1;
  height: 4px;
  background: #ebeef5;
  border-radius: 2px;
  overflow: hidden;
  min-width: 40px;
}
.rt-bar-inner {
  height: 100%;
  border-radius: 2px;
  transition: width 0.3s;
}

/* 移动端节点卡片视图 */
.node-cards { display: flex; flex-direction: column; gap: 12px; }
.node-card {
  border: 1px solid var(--np-border, #e4e7ed);
  border-radius: 10px;
  padding: 12px 14px;
  background: var(--np-card, #fafbfc);
}
.nc-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 10px;
  padding-bottom: 10px;
  border-bottom: 1px dashed var(--np-border, #ebeef5);
  flex-wrap: wrap;
}
.nc-title-wrap { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; min-width: 0; flex: 1; }
.nc-name { font-size: 15px; font-weight: 600; color: var(--np-text, #303133); word-break: break-all; }
.nc-online {
  display: inline-flex; align-items: center; gap: 4px;
  font-size: 12px; padding: 2px 8px; border-radius: 10px;
  flex-shrink: 0;
}
.nc-online.online { color: #67c23a; background: #f0f9eb; }
.nc-online.offline { color: #909399; background: #f4f4f5; }
.nc-row {
  display: flex; align-items: center; gap: 8px;
  font-size: 13px; margin-bottom: 6px; flex-wrap: wrap;
}
.nc-label { color: var(--np-text-muted, #909399); min-width: 40px; flex-shrink: 0; }
.nc-value { color: var(--np-text, #303133); word-break: break-all; }
.nc-mono { font-family: 'JetBrains Mono', Consolas, monospace; font-size: 12px; }
.nc-muted { color: #909399; font-size: 12px; }
.nc-plans { align-items: flex-start; }
.nc-tags { display: flex; flex-wrap: wrap; gap: 4px; }
.nc-load { margin-top: 4px; padding-top: 8px; border-top: 1px dashed var(--np-border, #ebeef5); }
.nc-load .rt-bar { flex: 1; min-width: 60px; }
.nc-load-empty { color: #909399; }
/* PC 端操作列: 编辑 + 更多下拉, 紧凑排列 */
.row-actions { display: inline-flex; align-items: center; gap: 4px; }
.row-actions .el-button { margin-left: 0; }

.nc-actions {
  display: flex; gap: 6px; flex-wrap: wrap;
  margin-top: 10px; padding-top: 10px;
  border-top: 1px dashed var(--np-border, #ebeef5);
}
.nc-actions .el-button { margin-left: 0 !important; flex: 1; min-width: 80px; }

/* 移动端: page-header 改为竖向, 搜索框占满宽度 */
@media (max-width: 768px) {
  .page-card { padding: 12px; }
  .page-header { flex-direction: column; align-items: stretch; }
  .header-actions { flex-direction: column; align-items: stretch; }
  .header-actions .el-input { width: 100% !important; }
}
</style>

<style>
/* 部署步骤渲染样式（全局，供局部组件使用） */
.deploy-step {
  border: 1px solid #e4e7ed;
  border-radius: 8px;
  padding: 14px 16px;
  margin-bottom: 14px;
  background: #fafbfc;
}
.deploy-step .step-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  flex-wrap: wrap;
}
.deploy-step .step-no {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: #409eff;
  color: #fff;
  font-size: 12px;
  font-weight: bold;
  flex-shrink: 0;
}
.deploy-step .step-title {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.deploy-step .step-exec-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 8px 12px;
  border-radius: 4px;
  margin-bottom: 10px;
  font-size: 13px;
  font-weight: 600;
}
.deploy-step .step-exec-bar .step-exec-icon {
  font-size: 14px;
}
.deploy-step .step-exec-bar .step-exec-text {
  color: #303133;
}
.deploy-step .step-exec-bar .step-flow {
  font-size: 12px;
  padding: 2px 10px;
  border-radius: 10px;
  background: #fdf6ec;
  color: #e6a23c;
  border: 1px solid #faecd8;
  font-weight: 500;
}
.deploy-step .step-desc {
  font-size: 12px;
  color: #606266;
  line-height: 1.6;
  margin-bottom: 10px;
}
.deploy-step .cmd-block {
  margin-bottom: 8px;
}
.deploy-step .cmd-label {
  font-size: 12px;
  color: #909399;
  margin-bottom: 4px;
}
.deploy-step .cmd-row {
  display: flex;
  align-items: stretch;
  gap: 0;
  border-radius: 4px;
  overflow: hidden;
  border: 1px solid #2d2d2d;
}
.deploy-step .cmd-text {
  flex: 1;
  display: block;
  background: #1e1e1e;
  color: #d4d4d4;
  padding: 8px 12px;
  font-size: 12px;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  white-space: pre-wrap;
  word-break: break-all;
  line-height: 1.6;
}
.deploy-step .cmd-copy-btn {
  flex-shrink: 0;
  width: 40px;
  background: #2d2d2d;
  color: #9cdcfe;
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.2s;
}
.deploy-step .cmd-copy-btn:hover {
  background: #3c3c3c;
  color: #fff;
}
.deploy-step .step-tip {
  font-size: 12px;
  color: #8c6d1f;
  background: #fdf6ec;
  border: 1px solid #faecd8;
  border-radius: 4px;
  padding: 6px 10px;
  margin-top: 8px;
  line-height: 1.5;
}
.deploy-step .step-expected {
  font-size: 12px;
  color: #1f6f3f;
  background: #f0f9eb;
  border: 1px solid #e1f3d8;
  border-radius: 4px;
  padding: 6px 10px;
  margin-top: 6px;
  line-height: 1.5;
}

/* 部署弹窗：允许内容滚动，确保终端可见 */
.deploy-dialog .el-dialog__body {
  max-height: calc(100vh - 200px);
  overflow-y: auto;
  padding-bottom: 16px;
}
.deploy-dialog .web-terminal {
  margin-top: 4px;
}
.deploy-dialog .term-host {
  min-height: 380px;
  height: 380px;
}
</style>
