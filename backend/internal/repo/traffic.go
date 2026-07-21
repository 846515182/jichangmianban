package repo

import (
	"time"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// TrafficRepo 流量仓储
type TrafficRepo struct {
	db *gorm.DB
}

// NewTrafficRepo 创建流量仓储
func NewTrafficRepo(db *gorm.DB) *TrafficRepo {
	return &TrafficRepo{db: db}
}

// TrafficTopRow 流量 TOP 用户行
type TrafficTopRow struct {
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
}

// TrafficTopNodeRow 流量 TOP 节点行
type TrafficTopNodeRow struct {
	NodeID        string `json:"node_id"`
	NodeName      string `json:"node_name"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
}

// DailyTrafficRow 每日流量行
type DailyTrafficRow struct {
	Day           string `json:"day"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownBytes     int64  `json:"down_bytes"`
}

// RecordLog 记录流量日志
func (r *TrafficRepo) RecordLog(userID, nodeID string, up, down int64, logTime time.Time) error {
	log := model.TrafficLog{
		UserID:        userID,
		NodeID:        nodeID,
		UploadBytes:   up,
		DownloadBytes: down,
		LogTime:       logTime.UTC(),
		LogDate:       logTime.UTC().Format("2006-01-02"),
	}
	return r.db.Create(&log).Error
}

// CreateLogTx 事务内创建流量日志
func (r *TrafficRepo) CreateLogTx(tx *gorm.DB, log *model.TrafficLog) error {
	if log.LogTime.IsZero() {
		log.LogTime = time.Now().UTC()
	}
	log.LogDate = log.LogTime.UTC().Format("2006-01-02")
	return tx.Create(log).Error
}

// SumByUsers 按用户ID批量汇总流量(返回 user_id -> total_bytes 映射)
func (r *TrafficRepo) SumByUsers(userIDs []string) (map[string]int64, error) {
	result := make(map[string]int64)
	if len(userIDs) == 0 {
		return result, nil
	}
	type row struct {
		UserID     string `gorm:"column:user_id"`
		TotalBytes int64  `gorm:"column:total_bytes"`
	}
	var rows []row
	err := r.db.Table("traffic_logs").
		Select("user_id, COALESCE(SUM(upload_bytes + download_bytes), 0) as total_bytes").
		Where("user_id IN ?", userIDs).
		Group("user_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.UserID] = r.TotalBytes
	}
	return result, nil
}

// TopUsers 流量 TOP 用户统计
// 修复 P1-repo-traffic: 旧版循环内逐条查 users 表(N+1), 现改为一次 IN 查询批量填充用户名
func (r *TrafficRepo) TopUsers(limit int, since time.Time) ([]TrafficTopRow, error) {
	var results []TrafficTopRow

	err := r.db.Table("traffic_logs").
		Select("user_id, COALESCE(SUM(upload_bytes), 0) as upload_bytes, COALESCE(SUM(download_bytes), 0) as download_bytes, COALESCE(SUM(upload_bytes + download_bytes), 0) as total_bytes").
		// 修复 2026-07-16: user_id 是 uuid 类型, 直接比较空字符串会让 Postgres 抛
		// "invalid input syntax for type uuid"; 改用 ::text 转换后比较
		Where("log_time >= ? AND user_id::text != '' AND user_id::text NOT LIKE '00000000-%'", since).
		Group("user_id").
		Order("total_bytes DESC").
		Limit(limit).
		Scan(&results).Error

	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return results, nil
	}

	// 修复 P1-repo-traffic: 批量查询用户名, 替代循环内 N+1 查询
	userIDs := make([]string, 0, len(results))
	for _, r := range results {
		userIDs = append(userIDs, r.UserID)
	}
	var users []struct {
		ID       string `gorm:"column:id"`
		Username string `gorm:"column:username"`
	}
	if err := r.db.Table("users").Select("id, username").
		Where("id IN ?", userIDs).Scan(&users).Error; err != nil {
		return nil, err
	}
	userMap := make(map[string]string, len(users))
	for _, u := range users {
		userMap[u.ID] = u.Username
	}
	for i := range results {
		results[i].Username = userMap[results[i].UserID]
	}

	return results, nil
}

// TopNodes 流量 TOP 节点统计
// 修复 P1-repo-traffic: 旧版循环内逐条查 nodes 表(N+1), 现改为一次 IN 查询批量填充节点名
func (r *TrafficRepo) TopNodes(limit int, since time.Time) ([]TrafficTopNodeRow, error) {
	var results []TrafficTopNodeRow

	err := r.db.Table("traffic_logs").
		Select("node_id, COALESCE(SUM(upload_bytes), 0) as upload_bytes, COALESCE(SUM(download_bytes), 0) as download_bytes, COALESCE(SUM(upload_bytes + download_bytes), 0) as total_bytes").
		Where("log_time >= ?", since).
		Group("node_id").
		Order("total_bytes DESC").
		Limit(limit).
		Scan(&results).Error

	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return results, nil
	}

	// 修复 P1-repo-traffic: 批量查询节点名, 替代循环内 N+1 查询
	nodeIDs := make([]string, 0, len(results))
	for _, r := range results {
		nodeIDs = append(nodeIDs, r.NodeID)
	}
	var nodes []struct {
		ID   string `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := r.db.Table("nodes").Select("id, name").
		Where("id IN ?", nodeIDs).Scan(&nodes).Error; err != nil {
		return nil, err
	}
	nodeMap := make(map[string]string, len(nodes))
	for _, n := range nodes {
		nodeMap[n.ID] = n.Name
	}
	for i := range results {
		results[i].NodeName = nodeMap[results[i].NodeID]
	}

	return results, nil
}

// TotalTrafficSince 统计指定时间之后的总流量
func (r *TrafficRepo) TotalTrafficSince(since time.Time) (upload, download int64, err error) {
	var result struct {
		Up   int64
		Down int64
	}
	
	err = r.db.Table("traffic_logs").
		Select("COALESCE(SUM(upload_bytes), 0) as up, COALESCE(SUM(download_bytes), 0) as down").
		Where("log_time >= ?", since).
		Scan(&result).Error
	
	return result.Up, result.Down, err
}

// ListByUser 查询用户流量记录（分页）
func (r *TrafficRepo) ListByUser(userID string, page, size int) ([]model.TrafficLog, int64, error) {
	var list []model.TrafficLog
	var total int64
	
	query := r.db.Where("user_id = ?", userID)
	
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	
	offset := (page - 1) * size
	if err := query.Order("log_time DESC").Offset(offset).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	
	return list, total, nil
}

// MultiNodeTrafficSince 批量查询多个节点在指定时间之后的流量
func (r *TrafficRepo) MultiNodeTrafficSince(nodeIDs []string, since time.Time) (map[string]struct{ Up, Dn int64 }, error) {
	if len(nodeIDs) == 0 {
		return make(map[string]struct{ Up, Dn int64 }), nil
	}
	
	type nodeTraffic struct {
		NodeID string
		Up     int64
		Dn     int64
	}
	
	var results []nodeTraffic
	
	err := r.db.Table("traffic_logs").
		Select("node_id, COALESCE(SUM(upload_bytes), 0) as up, COALESCE(SUM(download_bytes), 0) as dn").
		Where("node_id IN ?", nodeIDs).
		Where("log_time >= ?", since).
		Group("node_id").
		Scan(&results).Error
	
	if err != nil {
		return nil, err
	}
	
	result := make(map[string]struct{ Up, Dn int64 })
	for _, r := range results {
		result[r.NodeID] = struct{ Up, Dn int64 }{r.Up, r.Dn}
	}
	
	// 补充没有流量的节点
	for _, id := range nodeIDs {
		if _, ok := result[id]; !ok {
			result[id] = struct{ Up, Dn int64 }{0, 0}
		}
	}
	
	return result, nil
}

// DailyTraffic 按天聚合流量统计（最近 N 天）
func (r *TrafficRepo) DailyTraffic(days int) ([]DailyTrafficRow, error) {
	var results []DailyTrafficRow

	since := time.Now().UTC().AddDate(0, 0, -days)

	err := r.db.Table("traffic_logs").
		// 修复 2026-07-16: GORM 将 Postgres date 类型 scan 到 string 时, 会序列化为
		// "2026-07-12T00:00:00Z", 与 service.Trend 里的 byDay key "2026-07-12"
		// 不匹配, 导致所有点都查不到。改用 to_char 强制返回 "YYYY-MM-DD"。
		Select("to_char(log_date, 'YYYY-MM-DD') as day, COALESCE(SUM(upload_bytes), 0) as upload_bytes, COALESCE(SUM(download_bytes), 0) as down_bytes").
		Where("log_time >= ?", since).
		Group("log_date").
		Order("log_date ASC").
		Scan(&results).Error

	return results, err
}
