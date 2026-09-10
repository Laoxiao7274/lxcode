package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// readMaxBytes 是单次返回的字节上限：本地慢端点上下文有限，读太多会拖垮推理质量。
	readMaxBytes = 256 * 1024
	// readDefaultLines / readMaxLines 是行页大小：默认 2000 行与主流 agent 一致
	// （够用又不至于把上下文挤爆），模型可用 limit 显式放大。
	readDefaultLines = 2000
	readMaxLines     = 10000
	// readSniffBytes 是二进制嗅探窗口：文件开头这段里出现 NUL 就按二进制处理。
	readSniffBytes = 8192
)

// readFileDef：read_file，风险等级 低危（只读）。
//
// 为什么按行分页 + 带行号：模型改文件前必须先"看到真实内容"，而 256KB 一刀切的
// 字节截断有两个硬伤——截断后没法读到后半部分（没有续读入口），且截断点会落在
// 行中间产生语法碎片。行号则给了模型与用户共同的坐标系（"第 42 行"），也是
// 后续精确编辑的前提。二者都是主流 agent 的既定做法。
func readFileDef() *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "要读取的文件路径（绝对或相对工作目录）"},
			"offset": {"type": "integer", "description": "起始行号（1 起，默认 1）"},
			"limit": {"type": "integer", "description": "最多读取行数（默认 2000，上限 10000）"}
		},
		"required": ["path"]
	}`)
	return &Def{
		Name: "read_file",
		Description: fmt.Sprintf("读取文本文件内容，按行输出（每行前缀 `行号→`）。大文件用 offset/limit 分页，"+
			"单次默认最多 %d 行且不超过 %dKB；超出时会告诉你总行数与续读参数。"+
			"行号只是阅读坐标，原样写回文件是错误的（写入前请去掉行号）。",
			readDefaultLines, readMaxBytes/1024),
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if a.Path == "" {
				return "", fmt.Errorf("path 不能为空")
			}
			if a.Offset < 0 || a.Limit < 0 {
				return "", fmt.Errorf("offset/limit 不能为负数（offset 从 1 开始）")
			}
			if a.Offset == 0 {
				a.Offset = 1
			}
			if a.Limit == 0 {
				a.Limit = readDefaultLines
			}
			if a.Limit > readMaxLines {
				a.Limit = readMaxLines
			}

			st, err := os.Stat(a.Path)
			if err != nil {
				return "", fmt.Errorf("读取 %s 失败: %w", a.Path, err)
			}
			// 目录点名说清：OS 原生报错（Windows 下是 "Incorrect function."）模型看不懂，会重试同一路径
			if st.IsDir() {
				return "", fmt.Errorf("%s 是目录不是文件；列目录请用 bash 执行 ls", a.Path)
			}

			head, err := readHead(a.Path)
			if err != nil {
				return "", fmt.Errorf("读取 %s 失败: %w", a.Path, err)
			}
			if isBinary(head) {
				return fmt.Sprintf("（二进制文件，未返回内容：%s，%d 字节。文本工具无法展示，"+
					"请用 bash 的 file/xxd/od 等命令按需查看）", a.Path, st.Size()), nil
			}

			return readLines(a.Path, a.Offset, a.Limit)
		},
	}
}

// readHead 读取文件开头一小段用于二进制嗅探（空文件返回 nil, nil）。
func readHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, readSniffBytes)
	n, err := f.Read(buf)
	if n > 0 {
		return buf[:n], nil
	}
	if err == io.EOF {
		return nil, nil
	}
	return nil, err
}

// isBinary 判定二进制：嗅探窗口内出现 NUL 字节（文本文件不会含 NUL）。
func isBinary(head []byte) bool {
	return bytes.IndexByte(head, 0) >= 0
}

// readLines 按行分页读取，返回带行号的文本。
func readLines(path string, offset, limit int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 %s 失败: %w", path, err)
	}
	// Split 后末元素是 EOF 之后的空串，去掉以免多算一行
	lines := strings.Split(string(data), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total := len(lines)

	if offset > total {
		return fmt.Sprintf("（文件共 %d 行，offset=%d 超出范围；offset 从 1 开始）", total, offset), nil
	}

	var b strings.Builder
	end := offset - 1 + limit
	if end > total {
		end = total
	}
	for i := offset - 1; i < end; i++ {
		// 行号 + 制表符分隔：既是坐标，也便于模型与用户对齐
		fmt.Fprintf(&b, "%d→%s\n", i+1, lines[i])
		if b.Len() > readMaxBytes {
			end = i + 1
			break
		}
	}
	out := b.String()
	if len(out) > readMaxBytes {
		// 兜底：单行超长时按字节截断（极少见，如压缩过的单行 JSON）
		out = out[:readMaxBytes] + "\n…（单行内容过长，已按字节截断）"
	}
	var notes []string
	if end < total {
		notes = append(notes, fmt.Sprintf("已显示第 %d-%d 行（共 %d 行）；续读：offset=%d",
			offset, end, total, end+1))
	}
	if len(data) > readMaxBytes {
		notes = append(notes, fmt.Sprintf("文件共 %d 字节（本次输出已限制在 %dKB 内）", len(data), readMaxBytes/1024))
	}
	if len(notes) > 0 {
		out += "\n…（" + strings.Join(notes, "；") + "）"
	}
	if out == "" {
		return "（文件为空）", nil
	}
	return out, nil
}
