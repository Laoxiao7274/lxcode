// Package atomicfile 提供「同目录临时文件 + fsync + rename」的原子写。
//
// 抽成独立包的理由：这段逻辑有两处平台敏感点（Windows 的 rename 不覆盖、
// 跨盘 rename 非原子），复制第二份必然走样——tools 的 write_file 与 project 的
// 项目守则保存用的是同一份实现。
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write 原子替换目标文件，保留其原有权限位（新建则 0644）。
// 同目录是关键：跨文件系统的 rename 不是原子的，而系统临时目录往往与目标不同盘。
func Write(path string, data []byte) error {
	// 目标是目录时直接拒绝：留给后面会变成"删掉目录"或"写到目录里"的诡异行为
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return fmt.Errorf("%s 是目录，只能写文件", path)
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 失败路径统一清理临时文件，避免在宿主目录里留垃圾
	defer func() {
		if _, err := os.Stat(tmpName); err == nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Sync 后再 rename：保证 rename 成功后文件内容已落盘，断电不会留空文件
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err == nil {
		return nil
	}
	// 走到这里通常是 Windows（rename 不覆盖已存在文件）。回退策略必须是
	// "目标先改名备着"而不是"直接删目标"——后者若第二步失败就把用户的文件删了。
	return renameViaBackup(tmpName, path, dir)
}

// renameViaBackup 是 rename 不覆盖语义下的安全回退：先把目标挪成备份，
// 再把临时文件改名就位，最后删备份。任一步失败都尽量把目标还原回去。
func renameViaBackup(tmpName, path, dir string) error {
	backup := ""
	if _, err := os.Stat(path); err == nil {
		bak, err := os.CreateTemp(dir, "."+filepath.Base(path)+".bak*")
		if err != nil {
			return err
		}
		backup = bak.Name()
		bak.Close()
		_ = os.Remove(backup) // rename 需要目标不存在
		if err := os.Rename(path, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		if backup != "" {
			_ = os.Rename(backup, path) // 还原原文件，不留半成品
		}
		return err
	}
	if backup != "" {
		_ = os.Remove(backup)
	}
	return nil
}
