package main

// 登录链路诊断日志。只记录状态与长度，不记录密码或完整 Cookie。
// 路径：%LOCALAPPDATA%\sfjth\login-debug.log

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var diagMu sync.Mutex

func diagPath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "sfjth", "login-debug.log")
}

func diagLog(component, format string, args ...interface{}) {
	diagMu.Lock()
	defer diagMu.Unlock()
	path := diagPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s [%s] %s\n", time.Now().Format("15:04:05.000"), component, fmt.Sprintf(format, args...))
}
