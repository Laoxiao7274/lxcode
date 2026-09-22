package project

import (
	"fmt"
	"os"
	"path/filepath"
)

// 项目守则文件（项目根的 AGENTS.md）——「项目约定」进入会话上下文的唯一来源。
//
// 为什么归 project 包：与目录校验、git init 同属「项目目录的业务规则」，
// 且读取实现（文件名、上限、失败语义）必须单处定义——注入侧（server/agent）
// 与将来的编辑侧（协议）看的是同一份规则，不各写一遍。

// InstructionFileName 是项目守则的文件名。项目级 = 项目根下的这一个文件，
// 不向上找父目录、不读全局（2026-09-21 用户拍板：范围只限项目级）。
const InstructionFileName = "AGENTS.md"

// maxInstructionSourceBytes 是读取上限：超过就整份跳过（守则文件是给人读的
// markdown，1MB 以上不是守则而是事故——塞进上下文会直接顶爆窗口）。
const maxInstructionSourceBytes = 1 << 20

// Instructions 是守则文件的读取结果。
type Instructions struct {
	Path    string // 绝对路径（提示词里标明来源用）
	Content string // 原文；Exists=false 或 Note 非空时为空
	Exists  bool   // 文件是否存在（不存在是正常态，不是错误）
	Note    string // 读取异常/跳过的说明（空 = 正常）——注入侧据此如实告知模型
}

// LoadInstructions 读项目根的守则文件。
//
// 语义（调用方按此接线，不要各自解释）：
//   - 目录或文件不存在 → Exists=false，不是错误（绝大多数项目没有这个文件）；
//   - 读取失败/是目录/超大 → Exists=true + Note 说明，Content 为空；
//   - 绝不返回 error：守则读不到不该打断这一轮生成（用户要的是「有就用上」，
//     不是「读不到就报错」）。
func LoadInstructions(projectRoot string) Instructions {
	if projectRoot == "" {
		return Instructions{}
	}
	path := filepath.Join(projectRoot, InstructionFileName)
	info, err := os.Stat(path)
	if err != nil {
		return Instructions{Path: path} // 不存在（或不可访问）→ 不注入、不报错
	}
	if info.IsDir() {
		return Instructions{Path: path, Exists: true, Note: "同名路径是目录，已跳过"}
	}
	if info.Size() > maxInstructionSourceBytes {
		return Instructions{
			Path: path, Exists: true,
			Note: fmt.Sprintf("文件 %d 字节，超过 %d 字节上限，已跳过",
				info.Size(), maxInstructionSourceBytes),
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Instructions{Path: path, Exists: true, Note: "读取失败: " + err.Error()}
	}
	return Instructions{Path: path, Content: string(data), Exists: true}
}
