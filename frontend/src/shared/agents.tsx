// 可组装 Agent 名单（原型）：Agent = 身份 + 模型 + 工具白名单 + 上下文
// （流程模块单选 / 技能模块多选 / 自定义段）+ 委派 + 权限默认的组合单元。
// 两类（2026-09-17 用户拍板）：主 Agent = 唯一调度者（不可被委派，默认
// 委派名单可配置 + 会话内可收窄）；子 Agent = 纯执行者（不可委派）——
// 委派深度恒为 1，环与借手提权从结构上不存在。内存态（刷新重置）——
// 后端 Agent 注册表落地前的 UI 原型。
// 本文件只保留 Provider/hooks；类型在 agent-types.ts，种子数据在
// agent-seeds.ts（消费方兼容：类型与工厂从此处 re-export）。
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { useSettings } from "./settings";
import type { AgentDef, ContextModuleSpec, McServerSpec, ToolSpec } from "./agent-types";
import { BUILTIN_TOOLS, CONTEXT_MODULES, MC_SERVERS, THIRD_PARTY_TOOLS, seedAgents } from "./agent-seeds";

// 消费方兼容 re-export（原 26KB 单文件的类型/工厂出口不变）
export type { AgentDef, ContextModuleSpec, McServerSpec, ToolParam, ToolSpec } from "./agent-types";
export { AGENT_COLORS, MAIN_TOOL, BUILTIN_TOOLS, THIRD_PARTY_TOOLS, blankAgent, blankModule } from "./agent-seeds";

interface AgentsValue {
  agents: AgentDef[];
  addAgent: (def: AgentDef) => void;
  updateAgent: (def: AgentDef) => void;
  removeAgent: (id: string) => void;
  /** 输入区当前选用的 Agent（原型只存 UI 态——协议接入是后续内核的事）。 */
  activeAgentId: string;
  setActiveAgentId: (id: string) => void;
  /** 本次会话的委派覆盖（null = 跟随主 Agent 的名单默认；
   *  新会话/切换会话时清掉——由 App 订阅 sessionChanged 调 reset）。 */
  sessionDelegates: string[] | null;
  setSessionDelegates: (list: string[] | null) => void;
  resetSessionDelegates: () => void;
  /** 上下文模块拓展（运行时状态：内置种子 + 用户自建条目）。
   *  Agent 组装的 chips 与拓展页都读这里——单一事实源。 */
  modules: ContextModuleSpec[];
  addModule: (mod: ContextModuleSpec) => void;
  updateModule: (mod: ContextModuleSpec) => void;
  removeModule: (id: string) => void;
  /** 工具拓展（运行时状态：内置+第三方种子 + 导入/表单创建条目）。
   *  导入走固定格式 v1（shared/tool-import.ts 的 parseToolImport 校验）。 */
  tools: ToolSpec[];
  addTools: (tools: ToolSpec[]) => void;
  updateTool: (tool: ToolSpec) => void;
  removeTool: (id: string) => void;
  /** MCP 服务器拓展（第四版块——接入单元；能力以 source=mcp 工具进工具拓展）。 */
  mcpServers: McServerSpec[];
  addMcServer: (server: McServerSpec) => void;
  updateMcServer: (server: McServerSpec) => void;
  removeMcServer: (id: string) => void;
}

const Ctx = createContext<AgentsValue | null>(null);

export function AgentsProvider({ children }: { children: ReactNode }) {
  const [agents, setAgents] = useState<AgentDef[]>(seedAgents);
  const [activeAgentId, setActiveAgentId] = useState("main");
  const [sessionDelegates, setSessionDelegates] = useState<string[] | null>(null);
  const [modules, setModules] = useState<ContextModuleSpec[]>(CONTEXT_MODULES);
  const [tools, setTools] = useState<ToolSpec[]>(() => [...BUILTIN_TOOLS, ...THIRD_PARTY_TOOLS]);

  const addAgent = useCallback((def: AgentDef) => setAgents((list) => [...list, def]), []);
  const updateAgent = useCallback(
    (def: AgentDef) => setAgents((list) => list.map((a) => (a.id === def.id ? def : a))),
    [],
  );
  const removeAgent = useCallback((id: string) => {
    setAgents((list) => list.filter((a) => a.id !== id));
    // 删的是当前选用 → 回落主 Agent（入口永远存在）
    setActiveAgentId((cur) => (cur === id ? "main" : cur));
  }, []);
  const resetSessionDelegates = useCallback(() => setSessionDelegates(null), []);
  const addModule = useCallback((mod: ContextModuleSpec) => setModules((list) => [...list, mod]), []);
  const updateModule = useCallback(
    (mod: ContextModuleSpec) => setModules((list) => list.map((x) => (x.id === mod.id ? mod : x))),
    [],
  );
  const removeModule = useCallback(
    (id: string) => setModules((list) => list.filter((x) => x.id !== id)),
    [],
  );
  const addTools = useCallback((list: ToolSpec[]) => setTools((cur) => [...cur, ...list]), []);
  const updateTool = useCallback(
    (tool: ToolSpec) => setTools((cur) => cur.map((t) => (t.id === tool.id ? tool : t))),
    [],
  );
  const removeTool = useCallback(
    (id: string) => setTools((cur) => cur.filter((t) => t.id !== id)),
    [],
  );
  const [mcpServers, setMcServers] = useState<McServerSpec[]>(MC_SERVERS);
  const addMcServer = useCallback((s: McServerSpec) => setMcServers((list) => [...list, s]), []);
  const updateMcServer = useCallback(
    (s: McServerSpec) => setMcServers((list) => list.map((x) => (x.id === s.id ? s : x))),
    [],
  );
  const removeMcServer = useCallback(
    (id: string) => setMcServers((list) => list.filter((x) => x.id !== id)),
    [],
  );

  const value = useMemo(
    () => ({
      agents, addAgent, updateAgent, removeAgent,
      activeAgentId, setActiveAgentId,
      sessionDelegates, setSessionDelegates, resetSessionDelegates,
      modules, addModule, updateModule, removeModule,
      tools, addTools, updateTool, removeTool,
      mcpServers, addMcServer, updateMcServer, removeMcServer,
    }),
    [
      agents, addAgent, updateAgent, removeAgent,
      activeAgentId, sessionDelegates, resetSessionDelegates,
      modules, addModule, updateModule, removeModule,
      tools, addTools, updateTool, removeTool,
      mcpServers, addMcServer, updateMcServer, removeMcServer,
    ],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAgents(): AgentsValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useAgents 必须在 AgentsProvider 内使用");
  return v;
}

/** 模型显示名（注册表条目 → 展示名；找不到回落原始 id）。 */
export function useModelLabel(modelId: string): string {
  const { providers } = useSettings();
  const m = providers.flatMap((p) => p.models).find((x) => x.id === modelId);
  return m ? m.name : modelId || "未绑定模型";
}
