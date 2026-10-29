// MCP 服务器卡的显示判定（纯函数——node:test 钉住，组件不重复实现）。
//
// 这一小块逻辑有个真实的坑：**enabled 不等于已连接**。enabled 是「用户想开」
// （磁盘上的意图），连接状态是后端运行时报的。把前者当后者显示，用户会在命令
// 根本不存在（npx 没装）时看到绿色的「已连接」——本轮之前就是这样。
import type { McpRuntimeStatus } from "./types";

/** McpRuntimeView 是卡片需要的运行期状态（与 agents.tsx 的 McpRuntime 同形，
 *  这里独立声明以免 shared 内部成环）。 */
export interface McpRuntimeView {
  status: McpRuntimeStatus;
  toolCount: number;
  lastError: string;
  stderr: string;
}

export interface McpStatusPill {
  /** 标签文字。 */
  text: string;
  /** 标签样式类（复用既有的 ag-pill 变体）。 */
  cls: string;
  /** 第二行的补充说明（工具数那一行的尾巴）。 */
  note: string;
}

/** mcpStatusPill 算出卡片的状态标签。
 *
 * runtime 为 undefined（演示态/老后端没报状态）时**回落到按 enabled 显示**——
 * 与加运行期状态之前的行为一致（AGENTS.md §5 坑 11 的兼容姿势：新前端不能
 * 因为后端没热重载就显示错）。
 *
 * 三态各有各的话要说：
 *   - connected：真连上了，能力可用；
 *   - error：用户想开但连不上——必须和「已停止」区分开，否则用户以为是自己
 *     关掉了（实际是连不上）；
 *   - stopped：停用 = 能力挂起（这是**有意**的，不是故障）。 */
export function mcpStatusPill(enabled: boolean, runtime?: McpRuntimeView): McpStatusPill {
  if (!runtime) {
    return enabled
      ? { text: "已连接", cls: "risk-low", note: "能力可用" }
      : { text: "未连接", cls: "src", note: "能力挂起" };
  }
  switch (runtime.status) {
    case "connected":
      return { text: "已连接", cls: "risk-low", note: "能力可用" };
    case "error":
      // enabled 仍为 true（用户没关它）——说「连接失败」而不是「已停止」。
      return { text: "连接失败", cls: "risk-high", note: "能力不可用" };
    case "stopped":
      return enabled
        ? { text: "未连接", cls: "src", note: "能力挂起" }
        : { text: "已停止", cls: "src", note: "能力挂起" };
  }
}
