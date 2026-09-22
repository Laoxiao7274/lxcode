package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/moyunteng/lxcode/internal/atomicfile"
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
		Mutates:     true, // 全量覆盖写文件（strict 只读模式拒绝）
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
			if err := atomicfile.Write(path, []byte(a.Content)); err != nil {
				return "", fmt.Errorf("写入 %s 失败: %w", a.Path, err)
			}
			return fmt.Sprintf("已写入 %s（%d 字节）", a.Path, len(a.Content)), nil
		},
	}
}

// 原子写与 Windows 回退策略已抽到 internal/atomicfile（tools 与 project 共用——
// 这段逻辑有两处平台敏感点，复制第二份必然走样）。
