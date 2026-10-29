// 提交历史与单文件差异的只读 git 封装：服务 Git 管理页（git.overview / git.diff）。
// 与 status.go 同一套纪律：全部带 ctx + 超时（statusTimeout），绝不碰工作区。
package project

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// diffLimit 是单文件 diff 的字节上限：diff 是给前端渲染的文本，几 MB 的
// 生成文件会把整个应答撑爆——截断并注明。
const diffLimit = 32 * 1024

// CommitInfo 是一条提交的展示字段（git log 条目；调用方负责 wire 映射）。
type CommitInfo struct {
	Hash    string
	Message string // 标题行（首行）
	Author  string
	When    string // ISO 8601（作者时间）
}

// LogRecent 返回工作树 dir 当前 HEAD 最近 n 条提交（git log --pretty=format，
// \x1f 分隔解析；只读）。空仓库（还没有任何提交）返回空切片、不报错。
func LogRecent(ctx context.Context, dir string, n int) ([]CommitInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	if n <= 0 {
		return nil, nil
	}
	out, err := gitOutputCtx(ctx, dir,
		"log", "--pretty=format:%H%x1f%an%x1f%aI%x1f%s", "-n", fmt.Sprint(n))
	if err != nil {
		// 空仓库：git log 以「fatal: your current branch does not have any commits yet」
		// 收场——这是正常结论（还没有历史），不是查询失败。
		if strings.Contains(out, "does not have any commits yet") ||
			strings.Contains(out, "ambiguous argument 'HEAD'") {
			return nil, nil
		}
		return nil, fmt.Errorf("git log: %w", err)
	}
	var commits []CommitInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) != 4 {
			return nil, fmt.Errorf("git log 输出格式异常: %q", line)
		}
		commits = append(commits, CommitInfo{
			Hash:    parts[0],
			Author:  parts[1],
			When:    parts[2],
			Message: parts[3],
		})
	}
	return commits, nil
}

// DiffFile 返回工作树 dir 里单个文件的未提交差异（git diff -- <path>；只读）。
//
// 未跟踪文件没有 diff 输出——按「整文件新增」给全量内容标记（每行 + 前缀），
// 让前端能看到文件内容而不是一片空白。两种路径都截到 diffLimit 字节。
// 路径必须是仓库内相对路径；包含 ".." 的输入直接拒绝（防越界）。
func DiffFile(ctx context.Context, dir, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("缺少文件路径")
	}
	if path == ".." || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") ||
		path == `..` || strings.HasPrefix(path, `..\`) || strings.Contains(path, `\..\`) {
		return "", fmt.Errorf("非法路径: %q", path)
	}
	// 未跟踪判定：status --porcelain 对该文件给出 "??"。
	status, err := gitOutputCtx(ctx, dir, "status", "--porcelain", "--", path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.TrimSpace(status), "??") {
		return untrackedDiff(dir, path)
	}
	out, err := gitOutputCtx(ctx, dir, "diff", "--", path)
	if err != nil {
		return "", err
	}
	return truncateDiff(out), nil
}

// untrackedDiff 把未跟踪文件的内容格式成「整文件新增」的 diff 形态
//（+++/+++ 头 + 每行 + 前缀），与真实 diff 的渲染逻辑共用同一套展示。
func untrackedDiff(dir, path string) (string, error) {
	data, err := os.ReadFile(joinPath(dir, path))
	if err != nil {
		return "", fmt.Errorf("读取未跟踪文件失败: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "+++ b/%s（未跟踪文件，以下为全部内容）\n", path)
	content := string(data)
	truncated := false
	if len(content) > diffLimit {
		content = content[:diffLimit]
		truncated = true
	}
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		b.WriteString("+" + line + "\n")
	}
	if truncated {
		b.WriteString("…（内容超过 32KB，已截断）\n")
	}
	return b.String(), nil
}

// truncateDiff 把真实 diff 输出截到上限并注明（按字节，不撕 UTF-8 字符）。
func truncateDiff(out string) string {
	if len(out) <= diffLimit {
		return out
	}
	cut := out[:diffLimit]
	// 避免把多字节字符切半：回退到上一个合法 UTF-8 边界。
	for i := 0; i < 3 && len(cut) > 0 && !validUTF8Tail(cut); i++ {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n…（diff 超过 32KB，已截断）\n"
}

func validUTF8Tail(s string) bool {
	// strings 标准库无法直接判断「结尾完整」；用 DecodeLastRune 的合法性近似：
	// 最后一个 rune 解码成功且不是替换符即认为边界完整。
	for len(s) > 0 {
		r := []rune(s[len(s)-1:])
		if len(r) == 1 && r[0] != 0xFFFD {
			return true
		}
		s = s[:len(s)-1]
	}
	return false
}

// joinPath 拼仓库内文件路径（仅限本文件内部使用；不引入 filepath 以保持
// 与 git 传入路径一致的正斜杠形态——Windows 上 filepath 会产出反斜杠）。
func joinPath(dir, path string) string {
	sep := "/"
	if strings.HasSuffix(dir, "/") || strings.HasSuffix(dir, "\\") {
		sep = ""
	}
	return dir + sep + path
}
