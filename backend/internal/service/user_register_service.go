package service

import (
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

type UserRegisterService struct {
	userRepo *repo.UserRepo
	planRepo *repo.PlanRepo
}

// [S9 fix 2026-07-14] 邀请码相关错误哨兵
var (
	ErrInviteCodeInvalid  = errors.New("邀请码无效")
	ErrInviteCodeExpired  = errors.New("邀请码已过期")
	ErrInviteCodeExhausted = errors.New("邀请码已被使用完")
)

func NewUserRegisterService(u *repo.UserRepo, p *repo.PlanRepo) *UserRegisterService {
	return &UserRegisterService{userRepo: u, planRepo: p}
}

type RegisterInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	// [S9 fix 2026-07-14] 可选事务 DB. 提供时在事务内创建用户, 否则走默认 DB.
	DB       *gorm.DB `json:"-"`
}

func (s *UserRegisterService) Register(in *RegisterInput) (*model.User, error) {
	if in.Username == "" || in.Password == "" {
		return nil, errors.New("用户名和密码不能为空")
	}
	if len(in.Password) < 8 {
		return nil, errors.New("密码长度至少 8 位")
	}
	hasLetter := false
	hasDigit := false
	for _, ch := range in.Password {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			hasLetter = true
		} else if ch >= '0' && ch <= '9' {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return nil, errors.New("密码必须包含字母和数字")
	}
	if existing, err := s.userRepo.GetByUsername(in.Username); err == nil && existing != nil {
		return nil, ErrDuplicate
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	// 默认 5GB 试用流量 (5 * 1024 * 1024 * 1024)
	const trialTrafficLimit int64 = 5 * 1024 * 1024 * 1024
	const trialDurationDays = 30

	u := &model.User{
		Username:     in.Username,
		PasswordHash: string(hash),
		TrafficLimit: trialTrafficLimit,
		Status:       "active",
	}
	// 注册即享试用: 查找试用套餐(name 含"试用"且 enabled), 找到则绑定
	trialPlan, pErr := s.planRepo.GetTrialPlan()
	if pErr == nil && trialPlan != nil {
		u.PlanID = &trialPlan.ID
		if trialPlan.TrafficLimit > 0 {
			u.TrafficLimit = trialPlan.TrafficLimit
		}
		if trialPlan.DurationDays > 0 {
			t := time.Now().AddDate(0, 0, trialPlan.DurationDays)
			u.ExpiredAt = &t
		}
	} else {
		// 没有试用套餐, 仍然给 5GB + 30 天试用
		t := time.Now().AddDate(0, 0, trialDurationDays)
		u.ExpiredAt = &t
	}

	db := in.DB
	if db == nil {
		db = s.userRepo.GetDB() // 通过新增的方法获取默认 DB
	}
	if err := s.userRepo.CreateInDB(db, u); err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return u, nil
}

func (s *UserRegisterService) SetUserPlan(userID string, planID string) error {
	plan, err := s.planRepo.GetByID(planID)
	if err != nil {
		return err
	}
	u, err := s.userRepo.GetByID(userID)
	if err != nil {
		return err
	}
	u.PlanID = &plan.ID
	u.TrafficLimit = plan.TrafficLimit
	u.TrafficUsed = 0
	u.UploadBytes = 0
	u.DownloadBytes = 0
	if plan.DurationDays > 0 {
		t := time.Now().AddDate(0, 0, plan.DurationDays)
		u.ExpiredAt = &t
	} else {
		u.ExpiredAt = nil
	}
	if u.Status != "disabled" {
		u.Status = "active"
	}
	return s.userRepo.Update(u)
}


// GetUserRepo [S9 fix 2026-07-14] 暴露 userRepo, 用于 handler 获取 db 句柄
func (s *UserRegisterService) GetUserRepo() *repo.UserRepo {
	return s.userRepo
}

// RegisteredUser [S9 fix 2026-07-14] 注册结果类型 (handler 内部使用)
type RegisteredUser = model.User
