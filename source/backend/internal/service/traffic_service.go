package service

import (
	"time"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// TrafficService 流量服务
type TrafficService struct {
	trafficRepo *repo.TrafficRepo
	nodeRepo    *repo.NodeRepo
	userRepo    *repo.UserRepo
}

// NewTrafficService 创建流量服务
func NewTrafficService(t *repo.TrafficRepo, n *repo.NodeRepo, u *repo.UserRepo) *TrafficService {
	return &TrafficService{trafficRepo: t, nodeRepo: n, userRepo: u}
}

// RecordTrafficInput 流量上报入参
type RecordTrafficInput struct {
	UserID        string `json:"user_id"`
	NodeID        string `json:"node_id"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
}

// RecordTraffic 记录流量(写日志 + 累加用户/节点统计)
func (s *TrafficService) RecordTraffic(in *RecordTrafficInput) error {
	day := time.Now()
	if err := s.trafficRepo.RecordLog(in.UserID, in.NodeID, in.UploadBytes, in.DownloadBytes, day); err != nil {
		return err
	}
	// 累加用户流量统计(忽略错误，避免阻断上报)
	_ = s.userRepo.AddTraffic(in.UserID, in.UploadBytes, in.DownloadBytes)
	return nil
}

// TopResult 流量 TOP 结果
type TopResult struct {
	Users []repo.TrafficTopRow     `json:"users"`
	Nodes []repo.TrafficTopNodeRow `json:"nodes"`
}

// Top 流量 TOP 统计(默认最近 7 天)
func (s *TrafficService) Top(limit int, days int) (*TopResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if days <= 0 {
		days = 7
	}
	// 修复: 与 Dashboard/Trend 一致, 使用 UTC 计算 since
	since := time.Now().UTC().AddDate(0, 0, -days)
	users, err := s.trafficRepo.TopUsers(limit, since)
	if err != nil {
		return nil, err
	}
	nodes, err := s.trafficRepo.TopNodes(limit, since)
	if err != nil {
		return nil, err
	}
	return &TopResult{Users: users, Nodes: nodes}, nil
}

// DashboardStats 仪表盘统计
type DashboardStats struct {
	TotalUsers    int64 `json:"total_users"`
	ActiveUsers   int64 `json:"active_users"`
	ExpiredUsers  int64 `json:"expired_users"`
	TotalNodes    int64 `json:"total_nodes"`
	OnlineNodes   int64 `json:"online_nodes"`
	EnabledNodes  int64 `json:"enabled_nodes"`
	TodayUpload   int64 `json:"today_upload"`
	TodayDownload int64 `json:"today_download"`
	WeekUpload    int64 `json:"week_upload"`
	WeekDownload  int64 `json:"week_download"`
	TotalTraffic  int64 `json:"total_traffic"`
}

// Dashboard 仪表盘数据
func (s *TrafficService) Dashboard() (*DashboardStats, error) {
	// 修复 M4: 原代码用 now.Location()(服务器本地时区)构造 todayStart, 而
	// repo/traffic.go RecordLog 用 UTC 桶存 log_time、DailyTraffic 用 date_trunc(UTC)。
	// 当前 TZ=UTC 下侥幸一致, 一旦改时区即错位缺日。统一用 UTC 计算。
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	weekStart := now.AddDate(0, 0, -7)

	stats := &DashboardStats{}

	var err error
	stats.TotalUsers, err = s.userRepo.CountAll()
	if err != nil {
		return nil, err
	}
	stats.ActiveUsers, err = s.userRepo.CountActive()
	if err != nil {
		return nil, err
	}
	stats.ExpiredUsers, err = s.userRepo.CountByExpired(now)
	if err != nil {
		return nil, err
	}
	stats.TotalNodes, err = s.nodeRepo.CountAll()
	if err != nil {
		return nil, err
	}
	stats.OnlineNodes, err = s.nodeRepo.CountOnline()
	if err != nil {
		return nil, err
	}
	stats.EnabledNodes, err = s.nodeRepo.CountEnabled()
	if err != nil {
		return nil, err
	}

	todayUp, todayDn, err := s.trafficRepo.TotalTrafficSince(todayStart)
	if err != nil {
		return nil, err
	}
	stats.TodayUpload = todayUp
	stats.TodayDownload = todayDn

	weekUp, weekDn, err := s.trafficRepo.TotalTrafficSince(weekStart)
	if err != nil {
		return nil, err
	}
	stats.WeekUpload = weekUp
	stats.WeekDownload = weekDn
	stats.TotalTraffic = weekUp + weekDn

	return stats, nil
}

// ListUserTraffic 查询用户流量记录
func (s *TrafficService) ListUserTraffic(userID string, page, size int) ([]model.TrafficLog, int64, error) {
	return s.trafficRepo.ListByUser(userID, page, size)
}

// NodeRecentTraffic 批量查询多个节点在最近 N 分钟内的总流量
// 用于节点列表"近实时速度"展示（按 N 分钟窗口计算平均速率）
func (s *TrafficService) NodeRecentTraffic(nodeIDs []string, minutes int) (map[string]struct{ Up, Dn int64 }, error) {
	if minutes <= 0 {
		minutes = 5
	}
	since := time.Now().Add(-time.Duration(minutes) * time.Minute)
	return s.trafficRepo.MultiNodeTrafficSince(nodeIDs, since)
}

// TrendPoint 流量趋势点
type TrendPoint struct {
	Day   string `json:"day"`
	Up    int64  `json:"up"`
	Down  int64  `json:"down"`
	Total int64  `json:"total"`
}

// TrendResult 流量趋势结果
type TrendResult struct {
	Days  int         `json:"days"`
	Total int64       `json:"total"`
	Up    int64       `json:"up"`
	Down  int64       `json:"down"`
	Items []TrendPoint `json:"items"`
}

// Trend 按天聚合流量趋势(补齐缺失日期, 返回 N 天)
// 入参 days 默认 7, 上限 90
func (s *TrafficService) Trend(days int) (*TrendResult, error) {
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	rows, err := s.trafficRepo.DailyTraffic(days)
	if err != nil {
		return nil, err
	}
	// 以"day -> TrendPoint"形式构造, 补齐缺失日期
	byDay := make(map[string]TrendPoint, len(rows))
	for _, r := range rows {
		byDay[r.Day] = TrendPoint{Day: r.Day, Up: r.UploadBytes, Down: r.DownBytes, Total: r.UploadBytes + r.DownBytes}
	}
	items := make([]TrendPoint, 0, days)
	// 修复 M4: 补齐日期统一用 UTC, 与 DailyTraffic 的 date_trunc(UTC) 对齐
	now := time.Now().UTC()
	var totalUp, totalDn int64
	for i := days - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		p, ok := byDay[d]
		if !ok {
			p = TrendPoint{Day: d}
		}
		items = append(items, p)
		totalUp += p.Up
		totalDn += p.Down
	}
	return &TrendResult{
		Days:  days,
		Total: totalUp + totalDn,
		Up:    totalUp,
		Down:  totalDn,
		Items: items,
	}, nil
}
