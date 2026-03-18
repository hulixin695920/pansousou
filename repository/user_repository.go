package repository

import (
	"database/sql"
	"errors"

	"pansou/model"
)

var (
	ErrUserNotFound = errors.New("用户不存在")
)

// CreateUser 创建用户
func CreateUser(username, passwordHash, email, role string) (*model.User, error) {
	db, err := GetDB()
	if err != nil || db == nil {
		return nil, err
	}

	result, err := db.Exec(
		"INSERT INTO users (username, password_hash, email, role) VALUES (?, ?, ?, ?)",
		username, passwordHash, email, role,
	)
	if err != nil {
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return GetUserByID(id)
}

// GetUserByUsername 根据用户名获取用户
func GetUserByUsername(username string) (*model.User, error) {
	db, err := GetDB()
	if err != nil || db == nil {
		return nil, err
	}

	user := &model.User{}
	err = db.QueryRow(
		"SELECT id, username, password_hash, email, role, created_at, updated_at FROM users WHERE username = ?",
		username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Email, &user.Role, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return user, nil
}

// GetUserByID 根据ID获取用户
func GetUserByID(id int64) (*model.User, error) {
	db, err := GetDB()
	if err != nil || db == nil {
		return nil, err
	}

	user := &model.User{}
	err = db.QueryRow(
		"SELECT id, username, password_hash, email, role, created_at, updated_at FROM users WHERE id = ?",
		id,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.Email, &user.Role, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return user, nil
}

// UsernameExists 检查用户名是否已存在
func UsernameExists(username string) (bool, error) {
	_, err := GetUserByUsername(username)
	if err == ErrUserNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
