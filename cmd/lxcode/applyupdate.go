// applyupdate.go —— 更新引导替换（2026-12 用户拍板：放弃 PowerShell 冷替换）。
//
// 背景：应用内更新要同时换后端 exe（热替换，壳侧 updater.ts 自己完成）与
// app.asar（运行中 asar 被 Chromium 映射，换不了——壳必须退出后才能换）。
// 旧链路是壳 spawn 一个脱离进程的 PowerShell 脚本（update-script.ts）等壳
// 退出后换 asar 拉新壳；新链路由**老后端进程**接管：
//
//	壳 applyUpdate → 把新 asar 与清单写进 userData/update-pending/ → 热替换
//	后端 exe + 重启 → app.quit() → 新后端 --stdin-watch 收到 EOF → 执行引导
//	（换 asar → 拉起新壳）。
//
// 挂载点（两条，互相独立、幂等）：
//  1. --stdin-watch 的 EOF 退出路径（控制台形态，cmd/lxcode/main.go）——正常
//     应用内更新的主路；
//  2. 进程启动时（runServe 装配前）——兜底上次没跑完的替换（SCM 服务没有
//     stdin watch；壳异常退出 EOF 也没来）。启动时检查放在会话存储打开之前：
//     引导若拉起新壳，新壳会探测 7789——本进程还没监听，新壳会自己 spawn
//     后端（新 exe），双实例在启动窗口内竞争端口是既有语义（sidecar 的
//     探测/直连逻辑处理），无新增风险。
//
// 关键约束：update-pending 目录的路径**不能**按「配置上级」推导——SCM 服务
// 形态的 config 在 %ProgramData%，而壳写的清单在 userData。所以目录路径必须
// 由调用方显式传入（--stdin-watch 场景是壳 spawn 时给的 --config；启动检查
// 场景同源），或 LXCODE_UPDATE_PENDING 环境变量兜底（服务形态可在注册表
// 服务参数里带上）。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// updatePendingExpire 是清单过期阈值：壳退出后清单超过这个时长就视为过期
// 放弃（防关机/注销场景半执行——用户下次开机看到的应是原样安装，而不是
// 半新半旧的替换残局）。清单时间戳取**写入完成**时刻。
const updatePendingExpire = 10 * time.Minute

// UpdateManifestV1 是 update-pending/manifest.json 的格式（v1）。版本号是
// 格式演进口：后端只认自己认识的 version，不认识的如实记日志放弃（宁可
// 不替换也不按猜的格式执行）。
type UpdateManifestV1 struct {
	Version int    `json:"version"`  // 格式版本，恒 1
	NewAsar string `json:"new_asar"` // 暂存的新 asar（update-pending/app.asar），绝对路径
	// TargetAsar 是现役 app.asar 绝对路径（<install>/resources/app.asar）。
	TargetAsar string `json:"target_asar"`
	// ShellExe 是壳可执行文件绝对路径（引导完成后拉起它）。
	ShellExe string `json:"shell_exe"`
	// ShellArgs 是拉起壳的参数（默认不带任何参数——Electron 主入口自己恢复）。
	ShellArgs []string `json:"shell_args,omitempty"`
	// ShellDir 是拉起壳的工作目录（默认取壳 exe 所在目录）。
	ShellDir string `json:"shell_dir,omitempty"`
	// ExpectedVersion 是新版本号（记日志用，不参与校验逻辑）。
	ExpectedVersion string `json:"expected_version,omitempty"`
	// CreatedAtUnix 是清单写入完成时刻（秒）：过期判定用。
	CreatedAtUnix int64 `json:"created_at_unix"`
}

// applyUpdateLog 是引导替换的日志文件名（update-pending 下，与壳侧 update.log
// 分开——这条只记后端引导做的事）。
const applyUpdateLog = "apply-update.log"

// applyUpdatePending 检查 pending 清单并执行引导（不存在/过期/损坏 = 什么都不做）。
// 幂等：替换完成的收尾就是删清单，下次进来什么也找不到。
func applyUpdatePending(pendingDir string) {
	m, err := readPendingManifest(pendingDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("更新引导: 读取清单失败（放弃，不影响现有安装）: %v", err)
		}
		return
	}
	if err := runApplyUpdate(pendingDir, m); err != nil {
		log.Printf("更新引导: %v", err)
	}
}

// readPendingManifest 读清单并做基本合法性检查（过期 / 必要字段 / asar 在场）。
// 过期清单删除（别让下次启动再走一遍同样的放弃路径）。
func readPendingManifest(pendingDir string) (*UpdateManifestV1, error) {
	data, err := os.ReadFile(filepath.Join(pendingDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m UpdateManifestV1
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("清单不是合法 JSON（%w）——可能来自不认识的新版壳，放弃", err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("清单格式版本 %d 不认识（本后端只认 1），放弃", m.Version)
	}
	if age := time.Since(time.Unix(m.CreatedAtUnix, 0)); age > updatePendingExpire {
		_ = os.RemoveAll(pendingDir) // 过期清单清掉，别挡下次更新
		return nil, fmt.Errorf("清单已过期（写入于 %s 前，阈值 %s）——视为半执行的残局，放弃", age.Round(time.Second), updatePendingExpire)
	}
	if m.NewAsar == "" || m.TargetAsar == "" || m.ShellExe == "" {
		return nil, errors.New("清单缺必要字段（new_asar/target_asar/shell_exe）")
	}
	if _, err := os.Stat(m.NewAsar); err != nil {
		return nil, fmt.Errorf("暂存的新 asar 不在场（%s）: %w", m.NewAsar, err)
	}
	return &m, nil
}

// runApplyUpdate 执行引导：换 asar → 拉壳 → 收尾。返回 error 时清单保留
// （下次启动再试——中途失败的替换重跑是幂等的：rename 目标不存在就跳过）。
func runApplyUpdate(pendingDir string, m *UpdateManifestV1) (err error) {
	aLog := newApplyLogger(pendingDir)
	// 失败路径也要关日志句柄——Windows 上打开中的文件不可删，句柄不关
	// pending 目录清不掉（连测试的 TempDir 清理都会被卡死）。
	defer closeApplyLogger(pendingDir)
	defer func() {
		if err != nil {
			aLog("引导替换失败: %v", err)
		}
	}()
	aLog("引导替换开始: 目标=%s 新=%s 壳=%s 版本=%s", m.TargetAsar, m.NewAsar, m.ShellExe, m.ExpectedVersion)

	// 壳 exe 必须在场（不然拉不起来——替换就别做了，省得白换）。
	if _, err := os.Stat(m.ShellExe); err != nil {
		aLog("壳 exe 不在场，放弃替换")
		return fmt.Errorf("壳 exe 不在场（%s）", m.ShellExe)
	}

	// 等壳真的退出：运行中的 asar 被 Chromium 映射着，Windows 上 rename 运行中
	// 的映像文件**可以**（exe 热替换就是这么做的），但 asar 是被映射的数据文件
	// —— rename 是否成功取决于句柄是否已释放，所以 rename 成功本身就是「壳
	// 已退出」的天然同步点。重试窗给足（1s × 60 = 60s，覆盖壳优雅退出 + 杀软
	// 扫描新 asar 的场景）。
	if err := awaitTargetReleasable(m.TargetAsar, aLog); err != nil {
		aLog("等待 asar 释放超时，放弃（不动现有安装）")
		return err
	}

	// 换 asar：旧改名 .old（兜底回滚用）→ 新落位 → 校验 → 删 .old。
	old := m.TargetAsar + ".old"
	if err := os.Rename(m.TargetAsar, old); err != nil {
		aLog("旧 asar 改名失败: %v", err)
		return fmt.Errorf("旧 asar 改名失败: %w", err)
	}
	aLog("旧 asar 已改名 .old")
	if err := copyFile(m.NewAsar, m.TargetAsar); err != nil {
		aLog("新 asar 落位失败，回滚: %v", err)
		_ = os.Rename(old, m.TargetAsar) // 宁旧勿无
		return fmt.Errorf("新 asar 落位失败（已回滚旧 asar）: %w", err)
	}
	aLog("新 asar 已落位")
	if err := verifySameBytes(m.NewAsar, m.TargetAsar); err != nil {
		aLog("新 asar 校验失败，回滚: %v", err)
		_ = os.Remove(m.TargetAsar)
		_ = os.Rename(old, m.TargetAsar)
		return fmt.Errorf("新 asar 校验失败（已回滚旧 asar）: %w", err)
	}
	aLog("新 asar 校验通过（大小与 sha256 一致）")
	_ = os.Remove(old)

	// 拉起新壳。无论成败都要收尾：拉不起来用户手动开（老 asar 已经换成新的
	// 了——替换本身是成功的，别因为拉壳失败回滚掉它）。
	if serr := startShell(m, aLog); serr != nil {
		aLog("拉起新壳失败（用户需手动打开）: %v", serr)
	}
	// 收尾前先关日志句柄——Windows 上打开中的文件不可删，日志句柄不关，
	// pending 目录永远清不掉（首个真机试跑实测抓到）。
	closeApplyLogger(pendingDir)
	if err := os.RemoveAll(pendingDir); err != nil {
		aLog("清理 pending 目录失败（留待下次清理）: %v", err)
	} else {
		log.Printf("更新引导: pending 目录已清理")
	}
	log.Printf("更新引导: asar 替换完成（版本 %s）", m.ExpectedVersion)
	return nil
}

// rename 会失败（Access Denied），成功即已释放。重试到 60s。
func awaitTargetReleasable(target string, aLog func(string, ...any)) error {
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("现役 asar 不在场（%s）: %w", target, err)
	}
	probe := target + ".rename-probe"
	for i := 0; i < 120; i++ {
		if err := os.Rename(target, probe); err == nil {
			_ = os.Rename(probe, target) // 还回去——真正的换在 runApplyUpdate 里做
			return nil
		}
		if i%20 == 19 {
			aLog("asar 仍被占用（第 %d 秒），继续等待", i+1)
		}
		time.Sleep(time.Second)
	}
	return errors.New("等待 asar 释放超时（60s）——壳可能没有退出")
}

// copyFile 原样复制（不跟随归档语义——Go 的 os 包读的是原始字节）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// verifySameBytes 校验两文件大小 + sha256 一致（引导是最后一道防线——壳侧
// 已经校验过整包与逐文件，这里防的是复制过程本身的损坏）。
func verifySameBytes(a, b string) error {
	ha, la, err := hashFile(a)
	if err != nil {
		return fmt.Errorf("读 %s: %w", a, err)
	}
	hb, lb, err := hashFile(b)
	if err != nil {
		return fmt.Errorf("读 %s: %w", b, err)
	}
	if la != lb {
		return fmt.Errorf("大小不一致（%d vs %d）", la, lb)
	}
	if ha != hb {
		return errors.New("sha256 不一致")
	}
	return nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// startShell 拉起壳 exe（工作目录/参数按清单）。失败只报错不回滚——asar 已
// 换好，回滚只会把「新版可用、壳没起来」恶化成「旧版也回不去」。
func startShell(m *UpdateManifestV1, aLog func(string, ...any)) error {
	dir := m.ShellDir
	if dir == "" {
		dir = filepath.Dir(m.ShellExe)
	}
	args := m.ShellArgs
	// Windows 的 os.StartProcess 把第一个参数同时当 argv[0]（程序名）与命令行
	// 第一段——传参必须把 exe 路径放回 args[0]，否则 fakeshell 的 -selftest 会
	// 被当成程序名（首个真机试跑实测抓到：StartProcess(exe, ["-selftest", …])
	// 实际执行的是「-selftest 这个程序」）。
	if len(args) == 0 || args[0] != m.ShellExe {
		args = append([]string{m.ShellExe}, args...)
	}
	// os.StartProcess 不走 PATH 解析之外的 shell，参数按 argv 原样传——
	// Windows 上 StartProcess 会把 args 拼进命令行（引号转义由 Go 处理）。
	p, err := os.StartProcess(m.ShellExe, args, &os.ProcAttr{
		Dir:   dir,
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return err
	}
	_ = p.Release() // 不等它、不收尸——壳是长命进程，归用户管
	aLog("已拉起新壳: %s", m.ShellExe)
	return nil
}

// newApplyLogger 返回同时写 stderr（服务/控制台日志）与 update-pending 下
// apply-update.log 的日志函数（写不进去不反噬主流程）。日志句柄由本函数
// 注册进 closeHook，pending 目录收尾前由 closeApplyLogger 释放——Windows
// 上打开中的文件不可删，句柄不关 RemoveAll 永远清不掉目录。
func newApplyLogger(pendingDir string) func(string, ...any) {
	f, err := os.OpenFile(filepath.Join(pendingDir, applyUpdateLog), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	closed := false
	prev := closeApplyLoggerHook
	closeApplyLoggerHook = func(string) {
		if err == nil && !closed {
			_ = f.Close()
			closed = true
		}
		closeApplyLoggerHook = prev // 恢复（多层嵌套场景防御，通常只有一层）
	}
	if err != nil {
		return func(format string, args ...any) {
			log.Printf("更新引导: "+format, args...)
		}
	}
	return func(format string, args ...any) {
		if closed {
			log.Printf("更新引导: "+format, args...)
			return
		}
		line := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
		log.Print("更新引导: " + fmt.Sprintf(format, args...))
		if _, werr := f.WriteString(line); werr != nil {
			// 写失败就退化为纯 stderr 日志，别让日志问题反噬主流程
			closeApplyLogger(pendingDir)
		}
	}
}

// closeApplyLogger 在 pending 目录收尾前调用：释放日志句柄，RemoveAll 才
// 能成功（Windows 上打开中的文件不可删——与 asar 同一条铁律）。真正的关闭
// 逻辑在 newApplyLogger 闭包里持有句柄——用包级 hook 让单测能驱动，同时
// 保持 runApplyUpdate 的签名干净。
var closeApplyLoggerHook = func(pendingDir string) {}

func closeApplyLogger(pendingDir string) { closeApplyLoggerHook(pendingDir) }

// applyUpdateOnServeExit 是 --stdin-watch 退出路径的挂载：ctx 取消（EOF）后
// 在主 goroutine 调用（main.go 的 stdin-watch 分支——runServe 返回之后），
// 阻塞直到引导完成。本进程已是「老壳拉起的老后端」，stdin 已 EOF（壳在
// 退出），引导完成后进程退出，新壳拉起自己的新后端。
func applyUpdateOnServeExit(pendingDir string) {
	applyUpdatePending(pendingDir)
}
