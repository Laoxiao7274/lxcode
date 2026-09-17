// 主 Agent 委派名单的合成逻辑（纯函数——与 UI 无关，可独立测试）。
// 独立成叶模块的原因：agents.tsx 带运行时依赖（settings/context），
// 测试加载器只认带扩展名的导入；本文件只 import type，转译后零依赖。
import type { AgentDef } from "./agents";

/** 主 Agent 的有效委派名单：会话覆盖 ?? 名单默认，再 ∩ 启用的子 Agent。
 *  Thread 空态计数与输入区选择器共用——语义漂移会直接影响主 Agent 的
 *  调度面，别在调用处各自手拼。名单卡片展示「默认值」时不带会话覆盖，
 *  语义不同，不走这里。 */
export function effectiveDelegates(agents: AgentDef[], sessionDelegates: string[] | null): string[] {
  const main = agents.find((a) => a.isMain);
  if (!main) return [];
  const subIds = new Set(agents.filter((a) => !a.isMain && a.enabled).map((a) => a.id));
  return (sessionDelegates ?? main.delegates).filter((id) => subIds.has(id));
}
