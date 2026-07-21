package repo

import (
	"encoding/json"

	"gorm.io/gorm"

	"nexus-panel/internal/model"
)

// SettingRepo 系统设置仓储
type SettingRepo struct {
	db *gorm.DB
}

// NewSettingRepo 创建设置仓储
func NewSettingRepo(db *gorm.DB) *SettingRepo {
	return &SettingRepo{db: db}
}

// Get 获取单个设置(JSONB 解码到任意类型)
func (r *SettingRepo) Get(key string, out interface{}) error {
	var s model.Setting
	if err := r.db.Where("key = ?", key).First(&s).Error; err != nil {
		return err
	}
	if len(s.Value) == 0 {
		return nil
	}
	return json.Unmarshal(s.Value, out)
}

// GetString 获取字符串设置
func (r *SettingRepo) GetString(key string) (string, error) {
	var v string
	if err := r.Get(key, &v); err != nil {
		return "", err
	}
	return v, nil
}

// Set 设置值(JSONB 序列化)
// 修复 P1-repo-setting: 旧版 Select-then-Insert/Update 非原子, 高并发下两个请求
// 同时 First 都返回 NotFound 后各自 Create, 触发主键/唯一冲突。改为 upsert。
// 使用 CURRENT_TIMESTAMP 兼容 PostgreSQL 与 SQLite(测试用)
func (r *SettingRepo) Set(key string, value interface{}) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// upsert: 不存在则插入, 存在则更新 value 和 updated_at, 原子操作避免竞态
	result := r.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP`, key, b)
	return result.Error
}

// GetAll 获取全部设置
func (r *SettingRepo) GetAll() ([]model.Setting, error) {
	var list []model.Setting
	if err := r.db.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
