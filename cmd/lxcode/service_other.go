//go:build !windows

// service_other.go 是非 Windows 平台的桩：本项目的服务形态只做 Windows SCM
// （用户主平台）。类 Unix 上的等价物（systemd unit）将来需要时再加，
// 接口面（isWindowsService/runAsService）先占住。
package main

import "errors"

func isWindowsService() bool { return false }

func runAsService(path, addr, sessionsDir string) error {
	return errors.New("本平台不支持服务形态（只实现 Windows SCM）")
}
