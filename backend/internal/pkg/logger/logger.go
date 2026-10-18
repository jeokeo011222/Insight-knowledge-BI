// Package logger provides structured logging via zap.
package logger

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds a zap logger from level/format configuration.
func New(level, format string) (*zap.Logger, error) {
	lvl := zapcore.InfoLevel
	switch strings.ToLower(level) {
	case "debug":
		lvl = zapcore.DebugLevel
	case "info":
		lvl = zapcore.InfoLevel
	case "warn", "warning":
		lvl = zapcore.WarnLevel
	case "error":
		lvl = zapcore.ErrorLevel
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "asctime"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeDuration = zapcore.StringDurationEncoder

	var encoder zapcore.Encoder
	if strings.EqualFold(format, "json") {
		encoder = zapcore.NewJSONEncoder(encoderCfg)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderCfg)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), lvl)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), nil
}
