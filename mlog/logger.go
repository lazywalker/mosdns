/*
 * Copyright (C) 2020-2022, IrineSistiana
 *
 * This file is part of mosdns.
 *
 * mosdns is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * mosdns is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package mlog

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type LogConfig struct {
	// Level, See also zapcore.ParseLevel.
	Level string `yaml:"level"`

	// File that logger will be writen into.
	// Default is stderr.
	File string `yaml:"file"`

	// Production enables json output.
	Production bool `yaml:"production"`

	// TimeFormat controls how the timestamp (`ts`) field is encoded for
	// structured logs. Supported values:
	//  - "timestamp" (default): numeric epoch timestamp (existing behavior)
	//  - "iso8601": human-readable ISO8601 timestamps
	//  - "rfc3339": RFC3339 timestamps
	//  - "custom:<layout>": use a custom Go time layout string after the
	//    `custom:` prefix (e.g. `custom:2006-01-02 15:04:05`)
	TimeFormat string `yaml:"time_format"`
}

var (
	stderr = zapcore.Lock(os.Stderr)
	lvl    = zap.NewAtomicLevelAt(zap.InfoLevel)
	l      = zap.New(zapcore.NewCore(zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()), stderr, lvl))
	s      = l.Sugar()

	nop = zap.NewNop()
)

func NewLogger(lc LogConfig) (*zap.Logger, error) {
	lvl, err := zapcore.ParseLevel(lc.Level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
	}

	var out zapcore.WriteSyncer
	if lf := lc.File; len(lf) > 0 {
		f, _, err := zap.Open(lf)
		if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
		out = zapcore.Lock(f)
	} else {
		out = stderr
	}

	// Configure encoder based on requested time format.
	var devCfg zapcore.EncoderConfig
	if lc.Production {
		cfg := zap.NewProductionEncoderConfig()
		// decide time encoder based on configuration
		switch strings.ToLower(lc.TimeFormat) {
		case "", "timestamp":
			// keep default (epoch) behavior
		case "iso8601":
			cfg.EncodeTime = zapcore.ISO8601TimeEncoder
		case "rfc3339":
			cfg.EncodeTime = zapcore.RFC3339TimeEncoder
		default:
			if strings.HasPrefix(lc.TimeFormat, "custom:") {
				layout := strings.TrimPrefix(lc.TimeFormat, "custom:")
				cfg.EncodeTime = zapcore.TimeEncoderOfLayout(layout)
			}
		}
		return zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(cfg), out, lvl)), nil
	}
	devCfg = zap.NewDevelopmentEncoderConfig()
	switch strings.ToLower(lc.TimeFormat) {
	case "", "timestamp":
		// leave development default
	case "iso8601":
		devCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	case "rfc3339":
		devCfg.EncodeTime = zapcore.RFC3339TimeEncoder
	default:
		if strings.HasPrefix(lc.TimeFormat, "custom:") {
			layout := strings.TrimPrefix(lc.TimeFormat, "custom:")
			devCfg.EncodeTime = zapcore.TimeEncoderOfLayout(layout)
		}
	}
	return zap.New(zapcore.NewCore(zapcore.NewConsoleEncoder(devCfg), out, lvl)), nil
}

// L is a global logger.
func L() *zap.Logger {
	return l
}

// SetLevel sets the log level for the global logger.
func SetLevel(l zapcore.Level) {
	lvl.SetLevel(l)
}

// S is a global logger.
func S() *zap.SugaredLogger {
	return s
}

// Nop is a logger that never writes out logs.
func Nop() *zap.Logger {
	return nop
}
