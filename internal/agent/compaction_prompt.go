package agent

import "strings"

// 压缩检查点的提示词与框定（对齐 DSH compaction-basic 的 summarizer 设计）：
//
//   - 摘要指令作为**重放历史之后追加的最后一条 user 消息**，而不是另起一个
//     "摘要器 system prompt"——把会话自己的 system 提示词与消息前缀原样摆在前面，
//     这次辅助调用就成了上一次真实请求的前缀，provider 的 KV 缓存能复用
//     （上下文越长这一步省得越多）；
//   - 摘要是**一段文本**，落回历史时包成一条 user 消息（不是 system：我们的
//     anthropic 适配器会把所有 system 消息提到顶层 system 字段，中途插 system
//     会被挪到最前面，语义错位）。
const (
	// checkpointOpenTag / checkpointCloseTag 是检查点正文的定界标记
	//（前端据此渲染"已压缩历史"块，模型据此识别既有检查点并合并）。
	checkpointOpenTag  = "<compacted-summary>"
	checkpointCloseTag = "</compacted-summary>"
)

// checkpointPreamble 是检查点消息的前言：让模型把摘要当作既成背景，
// 而不是"用户刚说了一段奇怪的话"。
const checkpointPreamble = "这是自动生成的检查点，浓缩了之前的一段对话以腾出上下文空间。" +
	"把其中记录的上下文当作既成背景直接使用，不要复述它。" +
	"直接从后面的消息继续任务，不要提及这份检查点。"

// compactionInstruction 是摘要指令。八段结构与 DSH 一致——同一套结构在多轮
// 压缩间可累积合并：模型看到既有检查点时就地合并，而不是照抄一份新的。
//
// 用变量而非常量：正文里要嵌 checkpointOpenTag（标记单源，避免两处漂移）。
var compactionInstruction = `你现在充当这个 AI 编程助手的上下文压缩引擎。把上面的对话浓缩成一份结构化检查点，让另一个模型能无损耗地接手工作。

严格按下面的 Markdown 结构输出，每一节都要保留、顺序不变。用简短的项目符号，不要成段散文。没有内容的节写「（无）」，绝不允许省略任何一节。

## 主要请求与意图
- [用户最初与演进的意图；措辞本身重要时逐字引用]

## 关键技术概念
- [涉及的技术、框架、模式与约定]

## 文件与代码
- [精确路径：为什么重要、关键改动或片段]

## 错误与修复
- [错误：怎么解决的，以及相关的用户反馈]

## 待办
- [明确要求但尚未完成的工作]

## 当前工作
- [这个检查点时刻正在进行的精确内容]

## 下一步
- [单个下一步动作，直接对齐最近一次请求；没有则写「（无）」]

## 关键上下文
- [决策与理由、约束、用户偏好、未决问题、继续所需的资料]

规则：
- 用简洁的中文工程语言书写。精确保留文件路径、命令、错误串、标识符、数值、函数签名与语法片段。
- 忠实记录用户反馈与明确指令，尤其是纠正意见。
- 不要提到这次摘要请求，也不要提到上下文被压缩过。
- 只输出检查点正文：不要调用任何工具，也不要执行其它动作。
- 如果上面的对话里已经有一个 ` + checkpointOpenTag + ` 块，那是**先前的检查点**：不要逐字照抄，保留仍然成立的事实、丢掉过期的，把新信息合并进同一份结构。`

// buildCheckpoint 把摘要正文包成检查点消息内容（前言 + 定界标记）。
func buildCheckpoint(summary string) string {
	return checkpointPreamble + "\n\n" + checkpointOpenTag + "\n" + summary + "\n" + checkpointCloseTag
}

// isCheckpointContent 判断一段消息内容是否是压缩检查点。
// 历史回放与前端渲染都靠它——历史消息就是普通 llm.Message，没有类型字段可依赖。
func isCheckpointContent(content string) bool {
	return strings.Contains(content, checkpointOpenTag) && strings.Contains(content, checkpointCloseTag)
}
