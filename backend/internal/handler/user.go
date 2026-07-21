package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nexus-panel/internal/app"
	"nexus-panel/internal/middleware"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
	"nexus-panel/internal/service"
)

// UserHandler 用户端处理器
type UserHandler struct {
	userRepo       *repo.UserRepo
	nodeRepo       *repo.NodeRepo
	announceRepo   *repo.AnnouncementRepo
	subService     *service.SubscribeService
	trafficService *service.TrafficService
}

// NewUserHandler 创建用户处理器
func NewUserHandler(u *repo.UserRepo, n *repo.NodeRepo, a *repo.AnnouncementRepo, sub *service.SubscribeService, ts *service.TrafficService) *UserHandler {
	return &UserHandler{userRepo: u, nodeRepo: n, announceRepo: a, subService: sub, trafficService: ts}
}

// UserInfo [4] GET /api/v1/user/info
// 当前登录用户信息(含订阅链接)
func (h *UserHandler) UserInfo(c *gin.Context) {
	uid := middleware.GetUserID(c)
	u, err := h.userRepo.GetByID(uid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.CodeNotFound)
			return
		}
		response.Fail(c, response.CodeDBError)
		return
	}
	// 确保订阅存在并生成带签名订阅链接
	sub, err := h.subService.EnsureSubscription(uid)
	subURL := ""
	if err == nil && sub != nil {
		base := getRequestBaseURL(c)
		subURL, _ = h.subService.GenerateSignedURL(uid, base, c.ClientIP())
	}
	// 计算流量使用百分比
	usedPercent := 0.0
	if u.TrafficLimit > 0 {
		usedPercent = float64(u.TrafficUsed) / float64(u.TrafficLimit) * 100
	}
	response.OK(c, gin.H{
		"id":             u.ID,
		"username":       u.Username,
		"email":          u.Email,
		"traffic_limit":  u.TrafficLimit,
		"traffic_used":   u.TrafficUsed,
		"upload_bytes":   u.UploadBytes,
		"download_bytes": u.DownloadBytes,
		"used_percent":   usedPercent,
		"expired_at":     u.ExpiredAt,
		"status":         u.Status,
		"remark":         u.Remark,
		"subscribe_url":  subURL,
	})
}

// Subscribe [5] GET /api/v1/user/subscribe?type=&sig=
// 拉取订阅内容，需带签名 sig(有效期由配置决定)
func (h *UserHandler) Subscribe(c *gin.Context) {
	uid := middleware.GetUserID(c)
	subType := c.Query("type")
	sig := c.Query("sig")
	if sig == "" {
		response.Fail(c, response.CodeSubSigExpired)
		return
	}
	ua := c.GetHeader("User-Agent")
	res, err := h.subService.Fetch(uid, subType, sig, ua, c.ClientIP())
	if err != nil {
		if errors.Is(err, service.ErrSubSigExpired) {
			response.Fail(c, response.CodeSubSigExpired)
			return
		}
		response.Fail(c, response.CodeConfigGenFailed)
		return
	}
	c.Header("Content-Type", res.ContentType)
	c.Header("Content-Disposition", "attachment; filename=\""+res.Filename+"\"")
	c.Header("Profile-Update-Interval", "24")
	// 填充真实流量/到期信息，便于客户端展示用量
	h.fillSubscriptionUserinfo(c, uid)
	if res.Blocked {
		c.Header("X-Subscription-Status", "blocked")
		c.Header("X-Subscription-Reason", res.BlockReason)
	}
	c.String(http.StatusOK, res.Content)
}

// fillSubscriptionUserinfo 填充 Subscription-Userinfo 响应头(真实数据)
func (h *UserHandler) fillSubscriptionUserinfo(c *gin.Context, uid string) {
	u, err := h.userRepo.GetByID(uid)
	if err != nil || u == nil {
		c.Header("Subscription-Userinfo", "upload=0; download=0; total=0; expire=0")
		return
	}
	expire := int64(0)
	if u.ExpiredAt != nil {
		expire = u.ExpiredAt.Unix()
	}
	c.Header("Subscription-Userinfo",
		fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
			u.UploadBytes, u.DownloadBytes, u.TrafficLimit, expire))
}

// NodeList [6] GET /api/v1/nodes/list
// 用户可访问的节点列表（含脱敏后的连接信息，用于前端生成单节点分享链接和二维码）
// 注意: 不返回节点级流量(traffic_limit/traffic_used)，用户端只展示自己套餐的流量
func (h *UserHandler) NodeList(c *gin.Context) {
	uid := middleware.GetUserID(c)
	nodes, err := h.nodeRepo.ListByUser(uid)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	// 批量查询每个节点最近 5 分钟流量（用于前端展示节点近实时速度）
	nodeIDs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		nodeIDs = append(nodeIDs, n.ID)
	}
	var recentTraffic map[string]struct{ Up, Dn int64 }
	if h.trafficService != nil && len(nodeIDs) > 0 {
		recentTraffic, _ = h.trafficService.NodeRecentTraffic(nodeIDs, 5)
	}
	// 脱敏：不返回 node_token / server_config 中的私钥
	// 但返回 public_key / short_id / sni / flow，前端用于生成单节点分享链接
	list := make([]gin.H, 0, len(nodes))
	for _, n := range nodes {
		item := gin.H{
			"id":             n.ID,
			"name":           n.Name,
			"country_code":   n.CountryCode,
			"protocol":       n.Protocol,
			"server_address": n.ServerAddress,
			"port":           n.Port,
			"is_enabled":     n.IsEnabled,
			"online":         n.Online,
			"version":        n.Version,
			"last_seen_at":   n.LastSeenAt,
		}
		// 节点近 5 分钟流量（前后端用于显示"近实时"速度/负载）
		if recentTraffic != nil {
			if t, ok := recentTraffic[n.ID]; ok {
				item["recent5m_up"] = t.Up
				item["recent5m_dn"] = t.Dn
			} else {
				item["recent5m_up"] = int64(0)
				item["recent5m_dn"] = int64(0)
			}
		}
		// 解析 server_config 提取脱敏连接信息
		var cfg map[string]interface{}
		if err := json.Unmarshal(n.ServerConfig, &cfg); err == nil {
			conn := gin.H{
				"uuid": uid, // VLESS/VMess 客户端 UUID = 用户 ID（与服务端 xray clients.id 对齐）
			}
			if reality, ok := cfg["reality"].(map[string]interface{}); ok {
				// SNI 回退: reality.sni → reality.dest(去端口) → 默认 gateway.icloud.com
				// 必须与服务端 buildXrayConfig 的 serverNames 默认值一致，否则 REALITY 握手失败
				sni := ""
				if v, ok := reality["sni"].(string); ok && v != "" {
					sni = v
				} else if v, ok := reality["dest"].(string); ok && v != "" {
					if idx := strings.LastIndex(v, ":"); idx > 0 {
						sni = v[:idx]
					} else {
						sni = v
					}
				}
				if sni == "" {
					sni = "gateway.icloud.com"
				}
				conn["sni"] = sni
				conn["public_key"] = ""
				conn["short_id"] = ""
				if v, ok := reality["public_key"].(string); ok {
					conn["public_key"] = v
				}
				if v, ok := reality["short_id"].(string); ok {
					conn["short_id"] = v
				}
				conn["flow"] = "xtls-rprx-vision"
				conn["security"] = "reality"
				conn["fingerprint"] = "chrome"
			}
			if v, ok := cfg["password"].(string); ok {
				conn["password"] = v
			}
			if v, ok := cfg["method"].(string); ok {
				conn["method"] = v
			}
			item["connect"] = conn
		}
		list = append(list, item)
	}
	response.OK(c, gin.H{"list": list, "total": len(list)})
}

// NodeLatency [7] GET /api/v1/nodes/latency?node_id=
// 查询节点延迟(此处返回在线状态作为简化实现，真实延迟由 gRPC 上报)
func (h *UserHandler) NodeLatency(c *gin.Context) {
	nodeID := c.Query("node_id")
	if nodeID == "" {
		response.Fail(c, response.CodeParamError)
		return
	}
	n, err := h.nodeRepo.GetByID(nodeID)
	if err != nil {
		response.Fail(c, response.CodeNotFound)
		return
	}
	// 简化：根据在线状态返回延迟指标
	latency := 0
	if n.Online {
		latency = 50 // 占位，实际由节点 gRPC 上报
	}
	response.OK(c, gin.H{
		"node_id": n.ID,
		"online":  n.Online,
		"latency": latency,
	})
}

// Announcements [8] GET /api/v1/announcements
// 已发布公告列表
func (h *UserHandler) Announcements(c *gin.Context) {
	limit := atoiDefault(c.Query("limit"), 20)
	list, err := h.announceRepo.ListPublished(limit)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": len(list)})
}

// getRequestBaseURL 根据请求构造外部可访问的基础 URL
func getRequestBaseURL(c *gin.Context) string {
	// 优先使用配置的 PANEL_DOMAIN(反代/localhost 场景下 Host 头不可靠)
	// 但若 PANEL_DOMAIN 指向 127.0.0.1/localhost，说明是本地开发配置，
	// 扫码场景下其他设备无法访问，改用请求 Host 头
	if domain := app.Get().Cfg.PanelDomain; domain != "" {
		lower := strings.ToLower(domain)
		if !strings.Contains(lower, "127.0.0.1") && !strings.Contains(lower, "localhost") {
			domain = strings.TrimRight(domain, "/")
			// 自动补全协议前缀: PANEL_DOMAIN 可能配置为 "bbcdtv.top" 而非 "https://bbcdtv.top"
			if !strings.Contains(domain, "://") {
				domain = "https://" + domain
			}
			return domain
		}
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	// 优先信任 X-Forwarded-Proto(反代场景)
	if xfp := c.GetHeader("X-Forwarded-Proto"); xfp != "" {
		scheme = xfp
	}
	host := c.Request.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + strings.TrimRight(host, "/")
}

// PublicSubscribe [46] GET /api/v1/subscribe?token=&sig=&type=
// 公开订阅拉取(通过 sub_token + sig 认证, 无需 JWT)
func (h *UserHandler) PublicSubscribe(c *gin.Context) {
	token := c.Query("token")
	sig := c.Query("sig")
	subType := c.Query("type")
	if token == "" || sig == "" {
		response.Fail(c, response.CodeParamError)
		return
	}
	ua := c.GetHeader("User-Agent")
	res, err := h.subService.PublicFetch(token, subType, sig, ua, c.ClientIP())
	if err != nil {
		if errors.Is(err, service.ErrSubSigExpired) {
			response.Fail(c, response.CodeSubSigExpired)
			return
		}
		response.Fail(c, response.CodeConfigGenFailed)
		return
	}
	c.Header("Content-Type", res.ContentType)
	c.Header("Content-Disposition", "attachment; filename=\""+res.Filename+"\"")
	c.Header("Profile-Update-Interval", "24")
	// 通过 sub_token 反查用户，填充真实 Subscription-Userinfo 头
	if sub, err := h.subService.GetByToken(token); err == nil && sub != nil {
		h.fillSubscriptionUserinfo(c, sub.UserID)
	} else {
		c.Header("Subscription-Userinfo", "upload=0; download=0; total=0; expire=0")
	}
	if res.Blocked {
		c.Header("X-Subscription-Status", "blocked")
		c.Header("X-Subscription-Reason", res.BlockReason)
	}
	c.String(http.StatusOK, res.Content)
}
