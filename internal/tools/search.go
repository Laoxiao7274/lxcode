package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// 搜索工具的各项上限。数值取自"够回答一个问题，又不至于把上下文冲垮"这条线：
// 主流实现（Vercel harness 教学、deepagents、Claude Code）都在 50~1000 条之间，
// 我们偏保守——本地小模型的上下文比云端模型紧张得多。
const (
	searchDefaultMax   = 50              // 默认最多返回多少条命中
	searchMaxMax       = 500             // 模型显式要求时的上限
	searchScanMax      = 2000            // 内部扫描上限：够判断"还有很多"，也不必扫全盘
	searchDefaultDepth = 12              // 最大递归深度
	searchMaxFileBytes = 4 * 1024 * 1024 // 单文件超过则跳过（日志/镜像动辄几百 MB）
	searchMaxLineBytes = 500             // 单行超长时截断展示（压缩过的单行 JSON 很常见）
	searchWorkers      = 8               // 并行扫描文件数：墙钟延迟取决于最慢分片
	searchTimeout      = 30 * time.Second
)

// searchDef：search，风险等级 低危——只读、不经过 shell、不写任何东西，可自动执行。
//
// 为什么要有它（而不是让模型写 bash grep）：
//  1. bash 恒高危、每次都要人工确认，而"在日志里找 ERROR"这类只读检索是高频动作，
//     让确认门被这类噪声占满，会稀释真正危险操作的注意力；
//  2. 设备上的 grep 是 BusyBox 版（-E/-P 不可用），模型按训练记忆写的 grep 会直接报错；
//     用 Go 的 regexp（RE2，线性时间、无回溯爆炸）实现则整类失败消失；
//  3. 输出无界：指望模型每次记得写 `| head -50` 不可靠（社区有过单次 grep 返回
//     772KB 直接炸上下文的案例）。工具侧强制定界 + 明确告知截断才可靠。
//
// 实现刻意不经过 shell：免转义（上游项目就栽过——单引号把 glob 的 * 变成字面量），
// 也免掉 BusyBox/GNU 的差异。
func searchDef() *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "搜索模式。mode=files 时是文件名的通配（如 *.log）；mode=content/count 时是正则（如 timeout|ERROR）"},
			"path": {"type": "string", "description": "要搜索的目录或文件（默认当前目录）"},
			"mode": {"type": "string", "description": "files=只列匹配的文件路径（默认，最省上下文）；content=列出匹配行（带行号）；count=只给每个文件的命中数"},
			"all": {"type": "boolean", "description": "true=连隐藏文件、.git、二进制文件一起搜（默认 false）"},
			"max": {"type": "integer", "description": "最多返回条数，默认 50，上限 500"}
		},
		"required": ["pattern"]
	}`)
	return &Def{
		Name: "search",
		Description: "搜索文件内容或文件名（纯 Go 实现，不经过 shell）。默认只返回匹配的文件路径列表，" +
			"要看具体内容用 mode=content（带行号，可直接用于 read_file 的 offset）。" +
			"默认跳过隐藏文件、.git、二进制文件与虚拟文件系统目录，超过 4MB 的单文件跳过；" +
			"命中被截断时会告知总数。要连这些一起搜传 all=true。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
				Mode    string `json:"mode"`
				All     bool   `json:"all"`
				Max     int    `json:"max"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			return runSearch(ctx, a.Pattern, a.Path, a.Mode, a.All, a.Max)
		},
	}
}

// searchHit 是一条命中：文件路径、行号（files 模式为 0）、展示用文本。
type searchHit struct {
	seq  int // 收集序号，用于稳定排序
	file string
	line int
	text string
}

// searchStats 记录过滤与截断情况——必须回填给模型，否则"搜不到"会变成玄学。
type searchStats struct {
	scanned    int
	skippedBin int
	skippedBig int
	truncated  bool
	timeout    bool
}

// runSearch 是 search 工具的主流程：解析模式 → 遍历 → 并行匹配 → 有界输出。
func runSearch(ctx context.Context, pattern, path, mode string, all bool, max int) (string, error) {
	if pattern == "" {
		return "", fmt.Errorf("pattern 不能为空")
	}
	if path == "" {
		path = "."
	}
	if mode == "" {
		mode = "files"
	}
	switch mode {
	case "files", "content", "count":
	default:
		return "", fmt.Errorf("mode 只能是 files / content / count，收到 %q", mode)
	}
	if max <= 0 {
		max = searchDefaultMax
	}
	if max > searchMaxMax {
		max = searchMaxMax
	}

	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("路径不可访问: %s (%v)", path, err)
	}

	// files 模式且模式串像通配（含 * / ?）→ 按文件名匹配；否则一律按内容正则
	var namePat string
	var re *regexp.Regexp
	if mode == "files" && looksLikeGlob(pattern) {
		namePat = strings.ToLower(pattern)
	} else {
		re, err = regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("模式不是合法正则: %v（按文件名搜请用 mode=files 配 *.log 这类通配）", err)
		}
	}

	files, stats := collectFiles(ctx, path, st, all, namePat)
	hits, total, scanStats := scanFiles(ctx, files, re, mode, all)
	stats.skippedBin, stats.skippedBig = scanStats.skippedBin, scanStats.skippedBig

	// 稳定排序：按收集顺序（= 遍历顺序）再按行号
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].seq != hits[j].seq {
			return hits[i].seq < hits[j].seq
		}
		return hits[i].line < hits[j].line
	})

	return formatSearchResult(mode, pattern, path, hits, total, stats, max, all), nil
}

// looksLikeGlob 判断 pattern 更像文件名通配还是正则。
func looksLikeGlob(p string) bool {
	if !strings.ContainsAny(p, "*?") {
		return false
	}
	return !strings.ContainsAny(p, `()[]{}|\^$+`)
}

// collectFiles 遍历目录收集候选文件。用 WalkDir（走 DirEntry，不做多余 stat——
// ripgrep 作者点名过"多余的 stat 调用"是目录遍历最大的性能坑）。
func collectFiles(ctx context.Context, root string, st os.FileInfo, all bool, namePat string) ([]string, *searchStats) {
	stats := &searchStats{}
	var files []string

	// 单文件：直接用（与 ripgrep 一致——显式指定的文件绕过隐藏/二进制过滤）
	if !st.IsDir() {
		return append(files, root), stats
	}

	absRoot, _ := filepath.Abs(root)
	deadline := time.Now().Add(searchTimeout)

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 权限不足等：跳过该条，不影响整体搜索
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			stats.timeout = true
			return fs.SkipAll
		}
		name := d.Name()
		// 深度限制：避免一不小心走进 overlay2 那种极深目录
		if rel, rerr := filepath.Rel(absRoot, p); rerr == nil {
			if strings.Count(rel, string(os.PathSeparator)) > searchDefaultDepth {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			if !all && (name == ".git" || name == ".svn" || name == ".hg" || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			if !all && isPseudoFS(p) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // 符号链接/设备/管道：跳过，避免循环与阻塞
		}
		if !all && strings.HasPrefix(name, ".") {
			return nil
		}
		if !all && isPseudoFS(p) {
			return nil
		}
		if namePat != "" && !globMatch(namePat, strings.ToLower(name)) {
			return nil
		}
		files = append(files, p)
		return nil
	})
	return files, stats
}

// isPseudoFS 判断是否内核虚拟文件系统：递归进去要么卡死要么全是噪声。
func isPseudoFS(p string) bool {
	trimmed := strings.TrimPrefix(filepath.ToSlash(p), "/")
	for _, pre := range []string{"proc", "sys", "dev", "run", "host/proc", "host/sys", "host/dev", "host/run"} {
		if trimmed == pre || strings.HasPrefix(trimmed, pre+"/") {
			return true
		}
	}
	return false
}

// globMatch 支持 * 与 ? 的文件名匹配（大小写不敏感，双方已转小写）。
func globMatch(pat, name string) bool {
	if ok, err := filepath.Match(pat, name); err == nil && ok {
		return true
	}
	if pat != "" && !strings.ContainsAny(pat, `/\`) {
		if ok, err := filepath.Match(pat, filepath.Base(name)); err == nil && ok {
			return true
		}
	}
	return false
}

// scanFiles 并行读取候选文件做匹配。total 是"已知命中总数"：达到内部扫描上限后
// 停止累计（不为一个精确数字把整盘读完）。
func scanFiles(ctx context.Context, files []string, re *regexp.Regexp, mode string, all bool) ([]searchHit, int, searchStats) {
	type result struct {
		hits  []searchHit
		count int
		stats searchStats
	}
	results := make([]result, len(files))

	sem := make(chan struct{}, searchWorkers)
	var wg sync.WaitGroup
	for i, f := range files {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, file string) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			st, err := os.Stat(file)
			if err != nil {
				return
			}
			if st.Size() > searchMaxFileBytes {
				results[idx].stats.skippedBig = 1
				return
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return
			}
			results[idx].stats.scanned = 1
			if !all && isBinary(data[:min(len(data), readSniffBytes)]) {
				results[idx].stats.skippedBin = 1
				return
			}
			// re == nil 表示 files 模式里已按文件名命中：只需记下这个文件
			if re == nil {
				results[idx].count = 1
				results[idx].hits = append(results[idx].hits, searchHit{seq: idx, file: file})
				return
			}
			lineNo := 0
			for _, raw := range bytes.Split(data, []byte("\n")) {
				lineNo++
				if !re.Match(raw) {
					continue
				}
				results[idx].count++
				line := string(raw)
				if len(line) > searchMaxLineBytes {
					line = line[:searchMaxLineBytes] + "…（本行已截断）"
				}
				results[idx].hits = append(results[idx].hits, searchHit{seq: idx, file: file, line: lineNo, text: strings.TrimRight(line, "\r")})
			}
		}(i, f)
	}
	wg.Wait()

	var hits []searchHit
	total, scanned := 0, 0
	for _, r := range results {
		total += r.count
		scanned += r.stats.scanned
		hits = append(hits, r.hits...)
		if total > searchScanMax && mode != "count" {
			break
		}
	}
	stats := searchStats{scanned: scanned}
	for _, r := range results {
		stats.skippedBin += r.stats.skippedBin
		stats.skippedBig += r.stats.skippedBig
	}
	return hits, total, stats
}

// formatSearchResult 渲染给模型的文本，并**显式交代截断与过滤**——
// "静默截断比不截断更糟"：模型会以为看到了全貌，然后基于残缺信息下结论。
func formatSearchResult(mode, pattern, path string, hits []searchHit, total int, stats *searchStats, max int, all bool) string {
	var b strings.Builder
	shown := 0
	switch mode {
	case "count":
		fmt.Fprintf(&b, "搜索 %q 于 %s 的命中统计：\n", pattern, path)
		perFile := map[string]int{}
		for _, h := range hits {
			if h.line == 0 {
				perFile[h.file] = 1
				continue
			}
			perFile[h.file]++
		}
		for _, f := range sortedKeys(perFile) {
			fmt.Fprintf(&b, "%s: %d\n", f, perFile[f])
		}
		fmt.Fprintf(&b, "合计命中 %d 条（已扫描 %d 个文件）\n", total, stats.scanned)
	default:
		for _, h := range hits {
			if shown >= max {
				stats.truncated = true
				break
			}
			if mode == "content" {
				fmt.Fprintf(&b, "%s:%d:%s\n", h.file, h.line, h.text)
			} else {
				b.WriteString(h.file + "\n")
			}
			shown++
		}
		if shown == 0 {
			b.WriteString("没有匹配。")
		}
		if total > shown {
			fmt.Fprintf(&b, "\n…（共 %d 条命中，显示前 %d 条：缩小 pattern、加 path 限定，或用 mode=count 先看分布）",
				total, shown)
		}
	}
	// 过滤情况必须说清楚：模型不知道被过滤，会把"没搜到"当成"不存在"
	if !all {
		var notes []string
		if stats.skippedBin > 0 {
			notes = append(notes, fmt.Sprintf("跳过二进制文件 %d 个", stats.skippedBin))
		}
		if stats.skippedBig > 0 {
			notes = append(notes, fmt.Sprintf("跳过超过 %dMB 的文件 %d 个", searchMaxFileBytes/1024/1024, stats.skippedBig))
		}
		if stats.timeout {
			notes = append(notes, "达到时间上限，结果可能不完整")
		}
		base := "已跳过隐藏文件、.git、二进制文件与 /proc /sys /dev"
		if len(notes) > 0 {
			base += "；" + strings.Join(notes, "；")
		}
		if shown > 0 || len(notes) > 0 {
			b.WriteString("\n（默认过滤：" + base + "；要连这些一起搜传 all=true）")
		}
	}
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
