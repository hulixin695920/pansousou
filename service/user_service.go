package service

import (
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"pansou/model"
	"pansou/repository"
)

var (
	ErrInvalidUsername = errors.New("用户名格式无效，需3-32位字母数字下划线")
	ErrInvalidPassword = errors.New("密码长度需至少6位")
	ErrUserExists      = errors.New("用户名已存在")
)

const (
	bcryptCost = 12
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)

// ValidateUsername 验证用户名格式
func ValidateUsername(username string) bool {
	username = strings.TrimSpace(username)
	return usernameRegex.MatchString(username)
}

// ValidatePassword 验证密码格式
func ValidatePassword(password string) bool {
	return len(password) >= 6
}

// RegisterUser 注册新用户
func RegisterUser(username, password, email string) (*model.User, error) {
	username = strings.TrimSpace(username)
	if !ValidateUsername(username) {
		return nil, ErrInvalidUsername
	}
	if !ValidatePassword(password) {
		return nil, ErrInvalidPassword
	}

	exists, err := repository.UsernameExists(username)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrUserExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, err
	}

	role := "user"
	if email == "" {
		email = ""
	}
	return repository.CreateUser(username, string(hash), strings.TrimSpace(email), role)
}

// ValidateUserPassword 验证用户密码
func ValidateUserPassword(username, password string) (*model.User, error) {
	user, err := repository.GetUserByUsername(username)
	if err != nil {
		return nil, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	if err != nil {
		return nil, repository.ErrUserNotFound
	}
	return user, nil
}

// GetUserProfile 获取用户资料（不含敏感信息）
func GetUserProfile(username string) (*model.UserProfile, error) {
	user, err := repository.GetUserByUsername(username)
	if err != nil {
		return nil, err
	}
	return &model.UserProfile{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Role:      user.Role,
		CreatedAt: user.CreatedAt,
	}, nil
}
