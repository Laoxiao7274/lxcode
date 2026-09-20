// 演示数据源（M3 叙事形态）：主 Agent 只调度——思考选人 → agent.dispatch
// → dispatch 卡（子 Agent 执行全套：思考/工具/确认门）→ 主验收汇总。
// 覆盖 UI 全部状态：流式正文/思考链、dispatch 卡嵌套子时间线、高危
// 确认门、任务清单、产物卡、完成/取消。事件形状与后端协议 1:1。
import type { FileChange, SessionMeta, TodoItem } from "../../shared/types";

// 主 Agent 的思考（调度视角——分析任务、选人、拟验收标准，不碰实现）
export const MAIN_REASONING = [
  "用户想给工具循环加个超时保护。",
  "这是明确的编码任务——读代码、改实现、补测试、跑全量，适合派给代码 Agent。",
  "任务描述要自包含：目标（per-tool 超时兜底）、约束（bash 超时逻辑不动）、验收（回归用例 + 全量绿）。",
  "我等它的结果，验收后汇总给用户。",
];

// 子 Agent 的思考（执行视角——在 dispatch 卡内展示）
export const SUB_REASONING = [
  "任务：给 runTools 的工具调用加 120s 超时兜底，超时回填错误不中断整轮。",
  "先看 session.go 的工具循环现状。",
  "方案：Execute 外包一层 context.WithTimeout，超时转工具错误回填模型。",
  "改完补一个假工具 sleep 的回归用例，再跑全量确认。",
];

// 子 Agent 的最终回复（dispatchEnd.result——主 Agent 的验收输入）
export const SUB_RESULT =
  "已完成：runTools 里每个工具调用有独立的 120 秒超时，超时回填自解释错误文本，整轮不中断。新增 TestToolTimeout 回归用例（假工具 sleep 验证超时回填）。internal/agent 全量测试通过，无回归。";

// 主 Agent 的验收汇总（最终答复）
export const MAIN_ANSWER = [
  "代码 Agent 已完成，我验收过了：",
  "",
  "1. `internal/agent/session.go`——runTools 的每个工具调用包了独立 120s 超时，超时回填错误给模型而不是挂住整轮；",
  "2. `internal/agent/session_test.go`——新增 `TestToolTimeout` 用假工具验证超时回填路径。",
  "",
  "全量测试通过（152 + 新增 2 个），验收达标。旧的 bash 超时行为不受影响——那层管命令执行，这层管模型循环的兜底。",
];

export const TODO_INITIAL: TodoItem[] = [
  { content: "选派 Agent 并下发任务", status: "done" },
  { content: "子任务：加 per-tool 超时兜底", status: "active" },
  { content: "验收子结果并汇总", status: "pending" },
];

export const TODO_LATER: TodoItem[] = [
  { content: "选派 Agent 并下发任务", status: "done" },
  { content: "子任务：加 per-tool 超时兜底", status: "done" },
  { content: "验收子结果并汇总", status: "active" },
];

/** 一轮任务的产物（filesChanged 事件数据——Codex 的验收视图；子 Agent 的改动并入）。 */
export const FILES_CHANGED: FileChange[] = [
  {
    path: "internal/agent/session.go", added: 4, deleted: 0,
    diff: "@@ internal/agent/session.go:311 (runTools)\n \t\tif ctx.Err() != nil {\n \t\t\treturn false\n \t\t}\n+\t\t// per-tool 超时兜底：单工具卡死不让整轮挂住（tools 层超时不动，这层管循环）\n+\t\ttoolCtx, cancel := context.WithTimeout(ctx, 120*time.Second)\n+\t\tdefer cancel()\n+\t\t_ = toolCtx // 传入 Execute（演示数据省略）",
  },
  {
    path: "internal/agent/session_test.go", added: 26, deleted: 1,
    diff: "@@ internal/agent/session_test.go:402\n-func TestRunToolsCancel(t *testing.T) {\n+func TestRunToolsCancel(t *testing.T) {\n+\t// 原用例：取消传播（不变）\n+}\n+\n+// TestToolTimeout 超时兜底：假工具 sleep 超过阈值，断言回填超时错误而非死等。\n+func TestToolTimeout(t *testing.T) {\n+\ts := newTestSession(t)\n+\tif err := s.Send(\"跑个会卡死的工具\"); err != nil {\n+\t\tt.Fatal(err)\n+\t}\n+\twaitIdle(t, s)\n+\tsnapshot := s.History()\n+\tlast := snapshot.Messages[len(snapshot.Messages)-1]\n+\tif !strings.Contains(last.Content, \"超时\") {\n+\t\tt.Fatalf(\"应回填超时错误: %s\", last.Content)\n+\t}",
  },
];

export const SESSIONS: SessionMeta[] = [
  { id: "20260911-103024-a1b2", title: "给工具循环加超时保护", updatedAt: "刚刚", messages: 9, workspace: "proj-demo-lxcode" },
  { id: "20260910-225918-0a9e", title: "前后台分离的协议层评审", updatedAt: "昨天", messages: 14, workspace: "proj-demo-lxcode" },
  { id: "20260910-164246-c3d4", title: "edit 工具的唯一匹配校验设计", updatedAt: "3 天前", messages: 22, workspace: "proj-demo-lxcode" },
  { id: "20260909-090102-e5f6", title: "选型：Tauri 壳的边界", updatedAt: "上周", messages: 8, workspace: "proj-demo-lxcode", archived: true },
  { id: "20260908-151512-f7a8", title: "niubash 实测记录", updatedAt: "上周", messages: 6, workspace: "proj-demo-agent" },
  { id: "20260907-112209-b9c0", title: "容器化部署演练", updatedAt: "2 周前", messages: 18, workspace: "proj-demo-agent", archived: true },
];
