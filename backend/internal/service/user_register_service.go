package service

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
)

// bcrypt 计算成本 (与安全加固标准一致, cost=12)
const bcryptCost = 12

// username 合法字符: 字母/数字/下划线, 长度 3-20 (与前端校验一致, 后端不可信前端)
var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,20}$`)

type UserRegisterService struct {
	userRepo *repo.UserRepo
	planRepo *repo.PlanRepo
}

func NewUserRegisterService(u *repo.UserRepo, p *repo.PlanRepo) *UserRegisterService {
	return &UserRegisterService{userRepo: u, planRepo: p}
}

type RegisterInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	// [S9 fix 2026-07-14] 可选事务 DB. 提供时在事务内创建用户, 否则走默认 DB.
	DB *gorm.DB `json:"-"`
}

func (s *UserRegisterService) Register(in *RegisterInput) (*model.User, error) {
	if in.Username == "" || in.Password == "" {
		return nil, errors.New("用户名和密码不能为空")
	}
	// 后端必须重复校验用户名规则 (前端校验不可信)
	if !usernameRegex.MatchString(in.Username) {
		return nil, errors.New("用户名长度 3-20 个字符, 仅支持字母、数字和下划线")
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
	// 修复 P0-10: 旧实现先查重再 bcrypt(250ms), 放大 TOCTOU 竞态窗口。
	// 现改为先 bcrypt 再查重, 缩短查重与创建之间的时间窗口。
	// 最终一致性由 DB 唯一索引保证, 查重仅为给出友好提示。
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		return nil, err
	}

	if existing, err := s.userRepo.GetByUsername(in.Username); err == nil && existing != nil {
		return nil, ErrDuplicate
	}
	// 修复 CRITICAL 2026-07-19: 旧版只查 username 重复, 不查 email。
	// 历史软删用户 email 仍占唯一索引, INSERT 时直接撞约束, 报"重复"但前端看不出原因。
	// 现在显式查 email(带 is_deleted=false 过滤), 命中则返回明确的"邮箱已注册"错误。
	if existing, err := s.userRepo.GetByEmail(in.Email); err == nil && existing != nil {
		return nil, ErrDuplicateEmail
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
		// 修复 P0-10: 用 errors.Is 判断唯一约束冲突, 替代脆弱的字符串匹配。
		// gorm v2 支持 gorm.ErrDuplicatedKey (需配置 TranslateError)。
		if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateError(err) {
			// 区分是 username 还是 email 冲���: 再查一次定位
			if ex, e2 := s.userRepo.GetByEmail(in.Email); e2 == nil && ex != nil {
				return nil, ErrDuplicateEmail
			}
			return nil, ErrDuplicate
		}
		return nil, err
	}
	return u, nil
}

// isDuplicateError 兼容老版本驱动: 判断是否为唯一约束冲突
// gorm.ErrDuplicatedKey 需要配置 TranslateError, 未配置时回退到字符串匹配
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "23505")
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
