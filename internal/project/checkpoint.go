// 检查点提交信息的组装（Conventional Commits + [checkpoint] 标记）。
//
// 为什么要有这一层：检查点提交是**本地回滚粒度**（一轮一个），但它的历史会被
// 推送出去——`第 N 轮：<用户首行>` 这种格式在公开仓库里既读不懂也检索不到。
// 所以本地保留逐轮提交，提交信息统一成 `<type>(<scope>): <摘要>` + 正文（用户
// 原话 + 改动统计），并在正文首行打 `[checkpoint]` 标记：发版聚合推送时靠它
// 把检查点与真正的发版/手工提交分开（scripts/push-release.mjs 同款判定）。
//
// 纪律：
//   - **纯规则、不调模型**：每轮多一次 LLM 调用换一个提交信息不划算，规则
//     误判的代价只是提交信息不够贴切，回滚能力不受影响；
//   - **老提交不动**：本格式只对今后产生的检查点生效（不改写历史）；
//   - **正文不塞爆**：用户原话与文件清单都有上限（提交信息是给人看的摘要，
//     不是日志备份）。
package project

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CheckpointMarker 是检查点提交的正文标记（push-release 聚合与 `git log --grep`
// 共用这一个字面量）。
const CheckpointMarker = "[checkpoint]"

const (
	// checkpointSubjectMax 是提交标题里摘要部分的 rune 上限（留出 type/scope 前缀）。
	checkpointSubjectMax = 50
	// checkpointPromptMax 是正文里「用户原话」的总 rune 上限。
	checkpointPromptMax = 500
	// checkpointPromptLines 是正文里保留的用户原话行数上限。
	checkpointPromptLines = 6
	// checkpointFileListMax 是正文里逐个列出的文件数上限。
	checkpointFileListMax = 8
)

// FileChange 是一个被改动文件的统计（二进制文件增删行为 -1）。
type FileChange struct {
	Path    string
	Added   int
	Deleted int
}

// CommitCheckpoint 把工作树 dir 里的全部改动提交为一个检查点提交：先 `add -A`，
// 再按索引里的改动统计组装信息，最后提交。
//
// 语义与 CommitAll 完全一致（git add -A 会把用户手工改动一起收进去；无改动返回
// (false, nil) 不产生空提交；绝不 reset/checkout/stash/push）。区别只在提交信息
// 由「轮次 + 用户原话 + 改动清单」规则生成，而不是调用方拼好的单行字符串。
func CommitCheckpoint(ctx context.Context, dir string, turn int, prompt string) (committed bool, err error) {
	if dir == "" {
		return false, fmt.Errorf("工作目录为空")
	}
	ctx, cancel := context.WithTimeout(ctx, commitTimeout)
	defer cancel()
	status, err := gitOutputCtx(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	if _, err := gitOutputCtx(ctx, dir, "add", "-A"); err != nil {
		return false, err
	}
	// 统计失败不挡提交：检查点的价值在「有痕迹」，不在信息多漂亮。
	files, _ := stagedChanges(ctx, dir)
	subject, body := CheckpointMessage(turn, prompt, files)
	args := []string{"commit", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}
	if _, err := gitOutputCtx(ctx, dir, args...); err != nil {
		return false, err
	}
	return true, nil
}

// stagedChanges 读索引相对 HEAD 的逐文件增删统计（只读；空仓库回落空树比较）。
func stagedChanges(ctx context.Context, dir string) ([]FileChange, error) {
	out, err := gitOutputCtx(ctx, dir, "diff", "--cached", "--numstat", "HEAD")
	if err != nil {
		// 零提交仓库没有 HEAD：--cached 不带基线即与空树比较。
		out, err = gitOutputCtx(ctx, dir, "diff", "--cached", "--numstat")
		if err != nil {
			return nil, err
		}
	}
	return parseNumstat(out), nil
}

// parseNumstat 解析 `git diff --numstat` 的输出（二进制记 -1；重命名取目标路径）。
func parseNumstat(out string) []FileChange {
	var files []FileChange
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		files = append(files, FileChange{
			Path:    renameTarget(parts[2]),
			Added:   atoiOrNeg(parts[0]),
			Deleted: atoiOrNeg(parts[1]),
		})
	}
	return files
}

// renameTarget 把 numstat 的重命名写法收敛成目标路径：
// `old => new` / `dir/{old => new}/file`。
func renameTarget(p string) string {
	i := strings.Index(p, " => ")
	if i < 0 {
		return p
	}
	oldPart, newPart := p[:i], p[i+4:]
	// `dir/{old => new}/file`：oldPart 里最后一段以 `{` 开头，newPart 里带闭合 `}`。
	if brace := strings.LastIndex(oldPart, "{"); brace >= 0 && strings.Contains(newPart, "}") {
		return oldPart[:brace] + strings.Replace(newPart, "}", "", 1)
	}
	return newPart
}

func atoiOrNeg(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return -1
	}
	return n
}

// CheckpointMessage 组装检查点提交的标题与正文（纯函数，便于单测与预览）。
func CheckpointMessage(turn int, prompt string, files []FileChange) (subject, body string) {
	typ, scope := checkpointType(prompt, files), checkpointScope(files)
	if scope == "" {
		// 改动全在仓库根（或还没有改动可看）：不写空的 `()`。
		subject = fmt.Sprintf("%s: %s", typ, checkpointSubject(prompt, files))
	} else {
		subject = fmt.Sprintf("%s(%s): %s", typ, scope, checkpointSubject(prompt, files))
	}
	body = checkpointBody(turn, prompt, files)
	return subject, body
}

// checkpointType 按「用户意图关键词优先、改动文件兜底」推断提交类型。
// 顺序即优先级：修复/文档/测试/重构/性能/杂务，都不命中就是 feat（新功能或改进）。
func checkpointType(prompt string, files []FileChange) string {
	p := strings.ToLower(prompt)
	switch {
	case containsAny(p, "修复", "修好", "改正", "纠正", "bug", "报错", "错误", "失败", "异常", "闪退", "崩溃", "不生效", "不工作", "问题"):
		return "fix"
	case containsAny(p, "文档", "注释", "说明", "readme", "agents.md"):
		return "docs"
	case containsAny(p, "测试", "单测", "用例", "test"):
		return "test"
	case containsAny(p, "重构", "整理", "梳理", "清理", "精简", "简化", "拆分", "重命名", "改名", "优化"):
		return "refactor"
	case containsAny(p, "性能", "提速", "加速", "太慢", "卡顿"):
		return "perf"
	case containsAny(p, "依赖", "升级", "版本号", "打包", "发版", "构建", "配置"):
		return "chore"
	}
	// 意图不明：看改了什么。只动文档 → docs；只动测试 → test；其余按 feat。
	if len(files) > 0 {
		docsOnly, testOnly := true, true
		for _, f := range files {
			if !isDocFile(f.Path) {
				docsOnly = false
			}
			if !isTestFile(f.Path) {
				testOnly = false
			}
		}
		if docsOnly {
			return "docs"
		}
		if testOnly {
			return "test"
		}
	}
	return "feat"
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func isDocFile(p string) bool {
	// 只认 Markdown/reStructuredText 与 docs/ 目录——.txt 常是数据或测试夹具，
	// 把它算成文档会让「改了个 txt」的检查点变成 docs 类型。
	ext := strings.ToLower(path.Ext(p))
	if ext == ".md" || ext == ".rst" {
		return true
	}
	return strings.HasPrefix(p, "docs/")
}

func isTestFile(p string) bool {
	return strings.HasSuffix(p, "_test.go") || strings.HasPrefix(p, "tests/") ||
		strings.Contains(p, "/tests/") || strings.HasSuffix(p, ".test.mjs") ||
		strings.HasSuffix(p, ".test.ts") || strings.HasSuffix(p, ".test.tsx")
}

// checkpointScope 取改动文件最多的顶层目录；根目录文件不参与（顶层文件改得再多
// 也不该把 scope 写成 "."）。并列时按名字排序取第一个，保证同一改动集合的结果稳定。
func checkpointScope(files []FileChange) string {
	counts := map[string]int{}
	for _, f := range files {
		if i := strings.IndexByte(f.Path, '/'); i > 0 {
			counts[f.Path[:i]]++
		}
	}
	best, bestN := "", 0
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if counts[name] > bestN {
			best, bestN = name, counts[name]
		}
	}
	return best
}

// checkpointSubject 从用户原话首行生成摘要：去掉寒暄/连接词与行尾标点，截断到
// checkpointSubjectMax。原话为空时回落改动清单（「更新 N 个文件」）。
func checkpointSubject(prompt string, files []FileChange) string {
	line := firstLine(prompt)
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"然后", "还有", "另外", "接着", "继续", "嗯", "那", "再", "帮我", "请", "麻烦"} {
		if strings.HasPrefix(line, prefix) {
			line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	line = strings.TrimRight(line, "。．.！!？?；;，,、 ")
	if line == "" {
		if len(files) > 0 {
			return fmt.Sprintf("更新 %d 个文件", len(files))
		}
		return "本轮改动"
	}
	return truncateRunes(line, checkpointSubjectMax)
}

// checkpointBody 组装正文：`[checkpoint] 第 N 轮` + 用户原话引用 + 改动统计。
func checkpointBody(turn int, prompt string, files []FileChange) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s 第 %d 轮", CheckpointMarker, turn)
	if quoted := quotePrompt(prompt); quoted != "" {
		b.WriteString("\n\n用户原话：\n")
		b.WriteString(quoted)
	}
	if len(files) > 0 {
		added, deleted := 0, 0
		for _, f := range files {
			if f.Added > 0 {
				added += f.Added
			}
			if f.Deleted > 0 {
				deleted += f.Deleted
			}
		}
		fmt.Fprintf(&b, "\n\n改动：%d 个文件（+%d/-%d）\n", len(files), added, deleted)
		for i, f := range files {
			if i == checkpointFileListMax {
				fmt.Fprintf(&b, "- …另有 %d 个文件\n", len(files)-checkpointFileListMax)
				break
			}
			b.WriteString("- " + f.Path)
			if f.Added >= 0 && f.Deleted >= 0 {
				fmt.Fprintf(&b, " (+%d/-%d)", f.Added, f.Deleted)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// quotePrompt 把用户原话变成引用块（限行数/字数——提交信息是摘要，不是全文备份）。
func quotePrompt(prompt string) string {
	text := strings.TrimSpace(prompt)
	if text == "" {
		return ""
	}
	text = truncateRunes(text, checkpointPromptMax)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var b strings.Builder
	for i, line := range lines {
		if i == checkpointPromptLines {
			b.WriteString("> …\n")
			break
		}
		b.WriteString("> " + strings.TrimSpace(line) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func firstLine(text string) string {
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		return text[:i]
	}
	return text
}

// truncateRunes 按 rune 截断（中文按字符算，避免砍成半个字）。
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:max])) + "…"
}
