// share_file：把一个已存在的文件共享到对话（低危，只读语义）。
//
// 只读的含义：它**不修改任何工作区文件**——读源文件、复制进会话附件目录、
// 把元数据随工具结果流动（前端渲染附件卡）。strict 只读模式下可用
//（Mutates=false）：「把这个结果发给用户看」本来就是只读动作。
//
// 安全边界：路径相对会话工作目录解析，**净化后必须仍在工作目录之内**——
// 含 `..` 逃出工作目录的路径一律拒绝（穿越防护与 store.ResolveAttachmentPath
// 同一套判定思路：我们共享的是 agent 在本会话工作目录里产出的东西）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/moyunteng/lxcode/internal/llm"
)

// shareFileParams 是 share_file 的参数形状。
type shareFileParams struct {
	Path    string `json:"path"`
	Caption string `json:"caption"`
}

func shareFileDef() *Def {
	return &Def{
		Name: "share_file",
		Description: "把一个已存在的文件共享到对话里给用户看（低危只读：不修改任何文件）。" +
			"适合展示你刚生成的产物——图表、报告、导出的数据文件、截图等。" +
			"图片（png/jpeg/webp/gif）渲染为可点击放大的缩略图，其余文件渲染为可下载的文件卡。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "文件路径（相对会话工作目录解析；必须在工作目录之内，目录穿越会被拒绝）"},
    "caption": {"type": "string", "description": "一句话说明（可选）：这个文件是什么、用户该看哪里"}
  },
  "required": ["path"]
}`),
		Risk:    RiskLow,
		Mutates: false,
		Exec:    execShareFile,
	}
}

func execShareFile(ctx context.Context, args json.RawMessage) (string, error) {
	var p shareFileParams
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %v", err)
	}
	p.Path = strings.TrimSpace(p.Path)
	if p.Path == "" {
		return "", fmt.Errorf("缺少 path 参数（要共享的文件路径）")
	}
	// 工作目录基准（会话注入；未注入 = 会话没有工作目录——没有基准就没有
	// 穿越判定，宁可拒绝也不猜）。
	base := WorkDir(ctx)
	if base == "" {
		return "", fmt.Errorf("会话没有工作目录，无法解析相对路径——请用绝对路径重试，或先确认工作目录")
	}
	src, err := resolveSharePath(base, p.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("读取文件信息失败: %v", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s 是目录——share_file 只接受文件", p.Path)
	}
	// 大小上限前置（读之前就知道）：超限直接拒绝，不白读 20MB 进内存。
	// 写入边界（store）还有一道同样的校验——这里是体验层（报错更快、不白读）。
	if info.Size() > llm.MaxFileAttachmentBytes {
		return "", fmt.Errorf("文件超过大小上限（%d MB，当前 %d MB）",
			llm.MaxFileAttachmentBytes/(1<<20), info.Size()/(1<<20))
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %v", err)
	}
	save := AttachmentSave(ctx)
	if save == nil {
		return "", fmt.Errorf("会话存储未启用，无法共享附件")
	}
	// 落盘 + 登记（净化后的展示名 / 相对 attachments 根的路径 / 分类都由实现方
	// 给出——工具层不知道磁盘布局，也不该知道）。
	ref, err := save(filepath.Base(src), data)
	if err != nil {
		return "", err
	}
	ref.Caption = strings.TrimSpace(p.Caption)
	// 元数据随结果流动（内核把它挂到这条 tool 消息上——前端实时与回放同一份）
	AttachmentCollectorFrom(ctx).Add(ref)

	var b strings.Builder
	fmt.Fprintf(&b, "已把文件共享到对话：%s（%s）", ref.Name, humanBytes(info.Size()))
	if ref.Caption != "" {
		fmt.Fprintf(&b, "\n说明：%s", ref.Caption)
	}
	b.WriteString("\n文件已复制进会话附件目录，用户界面会渲染成附件卡；源文件未改动。")
	return b.String(), nil
}

// resolveSharePath 把共享路径解析到工作目录之内并做**穿越防护**：
//   - 相对路径：Join(base, path) 后 Clean，结果必须仍在 base 之内；
//   - 绝对路径：Clean 后必须仍在 base 之内（共享本会话工作目录里的产物才在
//     语义内；工作目录外的文件用户自己就能打开，不需要经对话中转）。
//
// 判定与 store.ResolveAttachmentPath 同一套：按 `..` 路径元素拒绝 + Clean 后
// 前缀比对双保险。统一转绝对路径再比（base 本身就是绝对路径）。
func resolveSharePath(base, path string) (string, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("解析工作目录失败: %v", err)
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("文件路径为空")
	}
	var target string
	if filepath.IsAbs(path) {
		target = filepath.Clean(path)
	} else {
		target = filepath.Clean(filepath.Join(absBase, path))
	}
	// `..` 路径元素按元素拒绝（与 store.ResolveAttachmentPath 同一条纪律：
	// 任何形式的 .. 都不是我们认可的引用形态）
	for _, seg := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "", fmt.Errorf("路径不合法（不允许 ..）: %q", path)
		}
	}
	if target != absBase && !strings.HasPrefix(target, absBase+string(filepath.Separator)) {
		return "", fmt.Errorf("路径越出工作目录，拒绝共享: %q（工作目录：%s）", path, absBase)
	}
	return target, nil
}

// humanBytes 与前端 formatBytes 同口径的极简版（1024 进制；工具结果里给
// 模型一个量级感即可，精确展示是前端的事）。
func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%d KB", n/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
