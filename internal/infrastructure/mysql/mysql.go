package mysql

import (
	"agentcanvas/internal/pkg/config"
	"context"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func New(cfg config.MySQLConfig) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	// 用来设置连接池
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}

	sqlDB.SetConnMaxLifetime(time.Hour)
	return db, nil
}

func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}
