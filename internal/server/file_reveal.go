// file.reveal（A4 文件卡跳编辑器）：工作区文件交给客户端壳打开的白名单判定。
//
// **信任边界**：路径净化后必须仍在该会话工作目录之内——share_file 的
// resolveSharePath 同一条判据（`..` 按元素拒绝 + 前缀比对），单独提出来是
// 因为 reveal 没有"读文件"动作，判定要能独立测试（路径白名单测试只打这个
// 纯函数）。后端只校验并返回绝对路径；打开动作由客户端壳执行——后端不碰
// GUI（无头服务/远程访问时壳侧本来就不在，渲染层无壳桥时动作不可见）。

package server

import (
	"os"
	"path/filepath"
	"strings"
)

// resolveWorkspacePath 把产物卡里的相对路径解析到会话工作目录之内并做
// **穿越防护**（share_file 的 resolveSharePath 同一条纪律）：
//   - `..` 路径元素按元素拒绝（任何形式的 .. 都不是我们认可的引用形态）；
//   - 相对路径 join 工作目录后 Clean；绝对路径 Clean 后必须仍带工作目录前缀；
//   - 返回净化后的绝对路径。
//
// base 为空时用后端进程目录（未分组会话的既有语义：工具相对路径按进程
// 目录解析——prompt 的 workdirLine 同一句话）。解析不出进程目录时拒绝
//（没有基准就没有穿越判定，宁可拒绝也不猜——share_file 同款注释）。
func resolveWorkspacePath(base, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", os.ErrInvalid
	}
	absBase := base
	if absBase == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		absBase = wd
	}
	absBase, err := filepath.Abs(absBase)
	if err != nil {
		return "", err
	}
	var target string
	if filepath.IsAbs(path) {
		target = filepath.Clean(path)
	} else {
		target = filepath.Clean(filepath.Join(absBase, path))
	}
	for _, seg := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", os.ErrInvalid
		}
	}
	if target != absBase && !strings.HasPrefix(target, absBase+string(filepath.Separator)) {
		return "", os.ErrInvalid
	}
	return target, nil
}
