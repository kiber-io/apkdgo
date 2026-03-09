package logging

import (
	"fmt"
	"log"
)

const (
	colorReset   = "\033[0m"
	colorError   = "\033[31m"
	colorSuccess = "\033[32m"
	colorWarning = "\033[33m"
	colorInfo    = "\033[34m"
	colorDebug   = "\033[90m"
)

var verbosity int

type Logger struct {
	name string
}

var defaultLogger = &Logger{}

func Named(name string) *Logger {
	return &Logger{name: name}
}

func Get(name string) *Logger {
	return Named(name)
}

func (l *Logger) messageWithName(v ...any) string {
	msg := fmt.Sprint(v...)
	if l == nil || l.name == "" {
		return msg
	}
	return fmt.Sprintf("[%s] %s", l.name, msg)
}

func (l *Logger) logg(color string, v ...any) {
	log.Println(color, l.messageWithName(v...), colorReset)
}

func (l *Logger) Logi(v ...any) {
	if verbosity >= 1 {
		l.logg(colorInfo, v...)
	}
}

func (l *Logger) Loge(v ...any) {
	if verbosity >= 1 {
		l.logg(colorError, v...)
	}
}

func (l *Logger) Logw(v ...any) {
	if verbosity >= 1 {
		l.logg(colorWarning, v...)
	}
}

func (l *Logger) Logd(v ...any) {
	if verbosity >= 2 {
		l.logg(colorDebug, v...)
	}
}

func Logi(v ...any) {
	defaultLogger.Logi(v...)
}

func Loge(v ...any) {
	defaultLogger.Loge(v...)
}

func Logw(v ...any) {
	defaultLogger.Logw(v...)
}

func Logd(v ...any) {
	defaultLogger.Logd(v...)
}

func Init(level int) {
	verbosity = level
}
