package service

import (
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// UserService 用户服务
type UserService struct {
        repo *repo.UserRepo
}

// NewUserService 创建用户服务
func NewUserService(r *repo.UserRepo) *UserService {
        return &UserService{repo: r}
}

// CreateUserInput 创建用户入参
type CreateUserInput struct {
        Username     string  `json:"username"`
        Password     string  `json:"password"`
        Email        string  `json:"email"`
        TrafficLimit int64   `json:"traffic_limit"`
        ExpireDays   int     `json:"expire_days"` // 有效天数，0 表示不限
        Remark       string  `json:"remark"`
        PlanID       *string `json:"plan_id"`     // 套餐ID
}

// CreateUser 创建用户
func (s *UserService) CreateUser(in *CreateUserInput) (*model.User, error) {
        if in.Username == "" || in.Password == "" {
                return nil, errors.New("用户名和密码不能为空")
        }
        // 检查重复
        if existing, err := s.repo.GetByUsername(in.Username); err == nil && existing != nil {
                return nil, ErrDuplicate
        }
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		return nil, err
	}
        u := &model.User{
                Username:     in.Username,
                PasswordHash: string(hash),
                Email:        in.Email,
                TrafficLimit: in.TrafficLimit,
                Status:       "active",
                Remark:       in.Remark,
        }
        if in.PlanID != nil && *in.PlanID != "" {
                u.PlanID = in.PlanID
        }
        if in.ExpireDays > 0 {
                t := time.Now().AddDate(0, 0, in.ExpireDays)
                u.ExpiredAt = &t
        }
        if err := s.repo.Create(u); err != nil {
		// 修复 P0-10: 用 errors.Is 判断唯一约束冲突, 替代脆弱的字符串匹配
		if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return u, nil
}

// BatchCreateUserInput 批量创建用户(导入)
type BatchCreateUserInput struct {
        Users []struct {
                Username     string  `json:"username"`
                Password     string  `json:"password"`
                Email        string  `json:"email"`
                TrafficLimit int64   `json:"traffic_limit"`
                ExpireDays   int     `json:"expire_days"`
                Remark       string  `json:"remark"`
                PlanID       *string `json:"plan_id"`
        } `json:"users"`
}

// BatchCreate 批量创建用户，返回成功与失败列表
func (s *UserService) BatchCreate(in *BatchCreateUserInput) (success []*model.User, failed []BatchFailItem) {
        for _, item := range in.Users {
                u, err := s.CreateUser(&CreateUserInput{
                        Username:     item.Username,
                        Password:      item.Password,
                        Email:         item.Email,
                        TrafficLimit:  item.TrafficLimit,
                        ExpireDays:    item.ExpireDays,
                        Remark:        item.Remark,
                        PlanID:        item.PlanID,
                })
                if err != nil {
                        failed = append(failed, BatchFailItem{Username: item.Username, Reason: err.Error()})
                        continue
                }
                success = append(success, u)
        }
        return success, failed
}

// BatchFailItem 批量操作失败项
type BatchFailItem struct {
        Username string `json:"username"`
        Reason   string `json:"reason"`
}

// UpdateUserInput 更新用户入参
type UpdateUserInput struct {
        Email        *string `json:"email"`
        TrafficLimit *int64  `json:"traffic_limit"`
        ExpireDays   *int    `json:"expire_days"` // 0 表示清除过期时间，正数表示从现在起 N 天
        Remark       *string `json:"remark"`
        Password     *string `json:"password"`    // 重置密码
        PlanID       *string `json:"plan_id"`     // 套餐ID
}

// UpdateUser 更新用户
func (s *UserService) UpdateUser(id string, in *UpdateUserInput) (*model.User, error) {
        u, err := s.repo.GetByID(id)
        if err != nil {
                return nil, err
        }
        if in.Email != nil {
                u.Email = *in.Email
        }
        if in.TrafficLimit != nil {
                u.TrafficLimit = *in.TrafficLimit
        }
        if in.Remark != nil {
                u.Remark = *in.Remark
        }
	if in.Password != nil && *in.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), bcryptCost)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = string(hash)
	}
        if in.ExpireDays != nil {
                if *in.ExpireDays <= 0 {
                        u.ExpiredAt = nil
                } else {
                        t := time.Now().AddDate(0, 0, *in.ExpireDays)
                        u.ExpiredAt = &t
                }
        }
        if in.PlanID != nil {
                if *in.PlanID != "" {
                        u.PlanID = in.PlanID
                }
        }
        if err := s.repo.Update(u); err != nil {
                return nil, err
        }
        return u, nil
}

// DeleteUser 软删除用户
func (s *UserService) DeleteUser(id string) error {
        if _, err := s.repo.GetByID(id); err != nil {
                return err
        }
        return s.repo.SoftDelete(id)
}

// HardDeleteUser 硬删除用户(物理删除, 用于测试数据彻底清理)
// 与 DeleteUser 不同: 不留 is_deleted=true 痕迹, 释放所有唯一索引,
// 重新注册同 username/email 不会冲突。
// 级联清理: traffic_logs, user_nodes, subscriptions, orders(软删保留)
func (s *UserService) HardDeleteUser(id string) error {
        // 允许硬删已软删的用户(用 Unscoped 查询)
        var u model.User
        if err := s.repo.GetDB().Unscoped().Where("id = ?", id).First(&u).Error; err != nil {
                return err
        }
        return s.repo.HardDelete(id)
}

// ResetTraffic 重置用户流量
func (s *UserService) ResetTraffic(id string) error {
        if _, err := s.repo.GetByID(id); err != nil {
                return err
        }
        return s.repo.ResetTraffic(id)
}

// DisableUser 禁用用户
func (s *UserService) DisableUser(id string) error {
        if _, err := s.repo.GetByID(id); err != nil {
                return err
        }
        return s.repo.UpdateStatus(id, "disabled")
}

// EnableUser 启用用户
func (s *UserService) EnableUser(id string) error {
        if _, err := s.repo.GetByID(id); err != nil {
                return err
        }
        return s.repo.UpdateStatus(id, "active")
}

// VerifyPassword 校验密码
func (s *UserService) VerifyPassword(u *model.User, password string) error {
        return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
}

// ErrDuplicate 重复错误(用户名)
var ErrDuplicate = errors.New("duplicate")

// ErrDuplicateEmail 邮箱已注册
var ErrDuplicateEmail = errors.New("email already registered")

// ErrValidation 输入校验错误(用户名/密码格式等), 应由 handler 转为 400
var ErrValidation = errors.New("validation failed")
