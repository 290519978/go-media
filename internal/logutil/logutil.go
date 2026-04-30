package logutil

import (
	"log"
	"strings"
	"sync/atomic"
)

type Level int32

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var currentLevel atomic.Int32

func init() {
	currentLevel.Store(int32(LevelInfo))
}

func ParseLevel(raw string) Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	case "info":
		fallthrough
	default:
		return LevelInfo
	}
}

func NormalizeLevel(raw string) string {
	switch ParseLevel(raw) {
	case LevelDebug:
		return "debug"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

func SetLevel(raw string) {
	currentLevel.Store(int32(ParseLevel(raw)))
}

func CurrentLevel() Level {
	return Level(currentLevel.Load())
}

func Enabled(level Level) bool {
	return level >= CurrentLevel()
}

func Debugf(format string, args ...any) {
	logf(LevelDebug, format, args...)
}

func Infof(format string, args ...any) {
	logf(LevelInfo, format, args...)
}

func Warnf(format string, args ...any) {
	logf(LevelWarn, format, args...)
}

func Errorf(format string, args ...any) {
	logf(LevelError, format, args...)
}

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func logf(level Level, format string, args ...any) {
	if !Enabled(level) {
		return
	}
	log.Printf("["+level.String()+"] "+format, args...)
}
