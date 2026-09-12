package gormdb

import (
	"time"
)

// LoggerConfig 定义 GORM SQL 日志配置。
type LoggerConfig struct {
	Level                string
	SlowThreshold        time.Duration
	IgnoreRecordNotFound bool
}

type options struct {
	logger        LoggerConfig
	maxBindParams int
}

// Option 定义数据库适配器可选项。
type Option func(*options)

// WithLoggerConfig 设置 Open 创建连接时的 GORM SQL 日志；包装已有连接的 New 不修改其日志。
func WithLoggerConfig(cfg LoggerConfig) Option {
	return func(opts *options) {
		opts.logger = cfg
	}
}

// WithMaxBindParameters 设置单条语句占位参数上限。
func WithMaxBindParameters(limit int) Option {
	return func(opts *options) {
		opts.maxBindParams = limit
	}
}

func defaultOptions() options {
	return options{
		logger: LoggerConfig{
			Level:                "info",
			SlowThreshold:        200 * time.Millisecond,
			IgnoreRecordNotFound: false,
		},
	}
}
