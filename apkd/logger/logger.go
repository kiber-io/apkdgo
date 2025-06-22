package logger

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

func logg(color string, v ...any) {
	msg := fmt.Sprint(v...)
	log.Println(color, msg, colorReset)
}

func Logi(v ...any) {
	if verbosity >= 1 {
		logg(colorInfo, v...)
	}
}

func Loge(v ...any) {
	if verbosity >= 1 {
		logg(colorError, v...)
	}
}

func Logw(v ...any) {
	if verbosity >= 1 {
		logg(colorWarning, v...)
	}
}

func Logd(v ...any) {
	if verbosity >= 2 {
		logg(colorDebug, v...)
	}
}

func Init(level int) {
	verbosity = level
}
