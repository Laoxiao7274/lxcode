package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// shrinkRatio 是"内容显著变短"的判定阈值：新内容不足原文件的 50% 时，说明
// 这多半不是一次正常修改（更可能是模型只回填了一部分），必须人工确认。
const shrinkRatio = 0.5

// writeFileDef：write_file，风险等级 高危（覆盖已有文件有数据破坏面）。
//
// 确认策略（两道）：
//   - 覆盖已有文件 → 确认，提示里带上现大小；
//   - 新内容比原文件少一半以上 → 确认提示里明确写出"将减少 N 字节"。
//
// 为什么加第二道：全量覆盖模式下，本地小模型最常见的失败不是"写错路径"，而是
// "只回填了一部分内容"（它以为在打补丁）。5000 字节的配置被写成 "{}" 这种事故
// 是静默的——旧实现只回一句"已写入（2 字节）"，谁都不会注意到。
//
// 为什么必须原子写：os.WriteFile 是 O_TRUNC 直写目标文件，中途失败（进程被杀、
// 磁盘满）会留下半截文件；对服务器配置文件来说这等于把服务写坏。改为
// 同目录临时文件 + Sync + rename 原子替换——读者要么看到旧内容，要么看到新内容。
func writeFileDef() *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "目标文件路径（父目录自动创建）"},
			"content": {"type": "string", "description": "要写入的完整内容（全量覆盖，不是补丁）"}
		},
		"required": ["path", "content"]
	}`)
	return &Def{
		Name:        "write_file",
		Description: "把内容写入本地文件（全量覆盖，父目录自动创建；写入是原子替换）。注意 content 必须是完整文件内容，不能只给改动片段。修改已有文件优先用 edit 工具（只替换匹配片段，无需整体重写）。",
		Parameters:  schema,
		Risk:        RiskHigh,
		Confirm: func(ctx context.Context, args json.RawMessage) string {
			var a struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(args, &a); err != nil || a.Path == "" {
				return "写入文件（参数不完整，建议拒绝）"
			}
			// 与执行层解析同一个文件（相对路径按会话工作目录）——
			// 两边各解析各的会把项目里的既有文件误判成新文件，确认门失效
			path := resolveToolPath(ctx, a.Path)
			st, err := os.Stat(path)
			if err != nil || st.IsDir() {
				return "" // 新文件：无需确认
			}
			newSize := len(a.Content)
			if st.Size() > 0 && float64(newSize) < float64(st.Size())*shrinkRatio {
				return fmt.Sprintf("⚠ 覆盖 %s：内容将显著变短（%d 字节 → %d 字节，减少 %d 字节）。"+
					"若模型只回填了部分内容，同意后会丢失原有数据，建议拒绝并要求它读全文后重写",
					path, st.Size(), newSize, st.Size()-int64(newSize))
			}
			return fmt.Sprintf("将覆盖已有文件 %s（现 %d 字节 → 新 %d 字节，内容会被整体替换）",
				path, st.Size(), newSize)
		},
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if a.Path == "" {
				return "", fmt.Errorf("path 不能为空")
			}
			// 相对路径按会话工作目录解析（项目会话 = 项目根）
			path := resolveToolPath(ctx, a.Path)
			if dir := filepath.Dir(path); dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return "", fmt.Errorf("创建目录失败: %w", err)
				}
			}
			if err := atomicWriteFile(path, []byte(a.Content)); err != nil {
				return "", fmt.Errorf("写入 %s 失败: %w", a.Path, err)
			}
			return fmt.Sprintf("已写入 %s（%d 字节）", a.Path, len(a.Content)), nil
		},
	}
}

// atomicWriteFile 以"同目录临时文件 + fsync + rename"方式原子替换目标文件。
// 保留目标文件原有的权限位（新建则 0644）。同目录是关键：跨文件系统的 rename
// 不是原子的，而 /tmp 往往与目标不同盘。
func atomicWriteFile(path string, data []byte) error {
	// 目标是目录时直接拒绝：留给后面会变成"删掉目录"或"写到目录里"的诡异行为
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return fmt.Errorf("%s 是目录，write_file 只能写文件", path)
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
