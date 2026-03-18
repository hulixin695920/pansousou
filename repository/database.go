package repository

import (
	"database/sql"
	"fmt"
	"log"
	"sync"

	_ "github.com/go-sql-driver/mysql"
	"pansou/config"
)

var (
	db     *sql.DB
	dbOnce sync.Once
)

// GetDB 获取数据库连接（单例）
func GetDB() (*sql.DB, error) {
	var err error
	dbOnce.Do(func() {
		if !config.AppConfig.DBEnabled || config.AppConfig.DBDSN == "" {
			return
		}
		db, err = sql.Open("mysql", config.AppConfig.DBDSN)
		if err != nil {
			log.Printf("数据库连接失败: %v", err)
			return
		}
		if err = db.Ping(); err != nil {
			log.Printf("数据库Ping失败: %v", err)
			db.Close()
			db = nil
			return
		}
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(5)
		log.Println("MySQL数据库连接成功")
	})
	if db == nil && config.AppConfig.DBEnabled {
		return nil, fmt.Errorf("数据库未正确初始化")
	}
	return db, err
}

// InitDatabase 初始化数据库（创建表）
func InitDatabase() error {
	database, err := GetDB()
	if err != nil || database == nil {
		return err
	}

	createTableSQL := `
	CREATE TABLE IF NOT EXISTS users (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		username VARCHAR(64) NOT NULL UNIQUE,
		password_hash VARCHAR(255) NOT NULL,
		email VARCHAR(128) DEFAULT '',
		role VARCHAR(32) DEFAULT 'user',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		INDEX idx_username (username)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`

	_, err = database.Exec(createTableSQL)
	if err != nil {
		return fmt.Errorf("创建users表失败: %w", err)
	}
	log.Println("数据库表初始化完成")
	return nil
}

// CloseDB 关闭数据库连接
func CloseDB() {
	if db != nil {
		db.Close()
		db = nil
	}
}
