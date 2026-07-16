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
func (r *SettingRepo) Set(key string, value interface{}) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var s model.Setting
	result := r.db.Where("key = ?", key).First(&s)
	if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
		return result.Error
	}
	if result.Error == gorm.ErrRecordNotFound {
		s = model.Setting{Key: key, Value: b}
		return r.db.Create(&s).Error
	}
	return r.db.Model(&s).Update("value", b).Error
}

// GetAll 获取全部设置
func (r *SettingRepo) GetAll() ([]model.Setting, error) {
	var list []model.Setting
	if err := r.db.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
