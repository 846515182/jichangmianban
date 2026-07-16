package security

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 角色常量
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// token 类型
const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"
)

// Claims 自定义 JWT 声明
type Claims struct {
	UserID      string `json:"uid"`
	Username    string `json:"usr"`
	Role        string `json:"role"`     // admin / user
	TokenType   string `json:"ttype"`   // access / refresh
	TokenVer    int64  `json:"tver,omitempty"` // token 版本号(注销所有设备时递增)
	jwt.RegisteredClaims
}

// JWTManager JWT 管理器
type JWTManager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewJWTManager 创建 JWT 管理器
func NewJWTManager(secret string, accessTTL, refreshTTL time.Duration) *JWTManager {
	return &JWTManager{
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

// GenerateAccessToken 生成 access token(默认 24h, ver=0)
func (m *JWTManager) GenerateAccessToken(userID, username, role string) (string, error) {
	return m.generate(userID, username, role, TokenTypeAccess, m.accessTTL, 0)
}

// GenerateRefreshToken 生成 refresh token(默认 7d, ver=0)
func (m *JWTManager) GenerateRefreshToken(userID, username, role string) (string, error) {
	return m.generate(userID, username, role, TokenTypeRefresh, m.refreshTTL, 0)
}

// GenerateTokenPair 同时生成 access 与 refresh token
func (m *JWTManager) GenerateTokenPair(userID, username, role string) (access, refresh string, err error) {
	access, err = m.GenerateAccessToken(userID, username, role)
	if err != nil {
		return "", "", err
	}
	refresh, err = m.GenerateRefreshToken(userID, username, role)
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// GenerateTokenPairWithVer 携带 token 版本号签发(用于注销所有设备后用户重新登录)
func (m *JWTManager) GenerateTokenPairWithVer(userID, username, role string, ver int64) (access, refresh string, err error) {
	access, err = m.generate(userID, username, role, TokenTypeAccess, m.accessTTL, ver)
	if err != nil {
		return "", "", err
	}
	refresh, err = m.generate(userID, username, role, TokenTypeRefresh, m.refreshTTL, ver)
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

func (m *JWTManager) generate(userID, username, role, ttype string, ttl time.Duration, ver int64) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		Username:  username,
		Role:      role,
		TokenType: ttype,
		TokenVer:  ver,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// Parse 解析并校验 token
func (m *JWTManager) Parse(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("签名算法不匹配")
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token 无效")
	}
	return claims, nil
}

// ValidateAccess 校验 access token
func (m *JWTManager) ValidateAccess(tokenStr string) (*Claims, error) {
	claims, err := m.Parse(tokenStr)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != TokenTypeAccess {
		return nil, errors.New("非 access token")
	}
	return claims, nil
}

// ValidateRefresh 校验 refresh token
func (m *JWTManager) ValidateRefresh(tokenStr string) (*Claims, error) {
	claims, err := m.Parse(tokenStr)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != TokenTypeRefresh {
		return nil, errors.New("非 refresh token")
	}
	return claims, nil
}
