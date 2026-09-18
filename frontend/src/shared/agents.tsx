// 可组装 Agent 名单：Agent = 身份 + 模型 + 工具白名单 + 上下文
// （流程模块单选 / 技能模块多选 / 自定义段）+ 委派 + 权限默认的组合单元。
// 两类（2026-09-17 用户拍板）：主 Agent = 唯一调度者（不可被委派，默认
// 委派名单可配置 + 会话内可收窄）；子 Agent = 纯执行者（不可委派）——
// 委派深度恒为 1，环与借手提权从结构上不存在。
// M1 后端化：live 模式（source.agentAdmin 存在）下后端是事实源——
// 初始种子拉取 + agent.changed/catalog.changed 事件重同步；写操作
// 乐观本地生效后发 WS 调用（错误回滚走 operationError 提示）。
// demo 模式保持内存种子（行为与原型完全一致）。
// 本文件只保留 Provider/hooks；类型在 agent-types.ts，种子数据在
// agent-seeds.ts（消费方兼容：类型与工厂从此处 re-export）。
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useSettings } from "./settings";
import type { AgentDef, ContextModuleSpec, McServerSpec, ToolSpec } from "./agent-types";
import { BUILTIN_TOOLS, CONTEXT_MODULES, MC_SERVERS, THIRD_PARTY_TOOLS, seedAgents } from "./agent-seeds";
import type { AgentAdminEntry, AgentAdminMcServer, AgentAdminModule, AgentAdminTool, AgentSource } from "./types";

// 消费方兼容 re-export（原 26KB 单文件的类型/工厂出口不变）
export type { AgentDef, ContextModuleSpec, McServerSpec, ToolParam, ToolSpec } from "./agent-types";
export { AGENT_COLORS, MAIN_TOOL, BUILTIN_TOOLS, THIRD_PARTY_TOOLS, blankAgent, blankModule } from "./agent-seeds";

// ---- wire（snake_case）→ 前端类型（camelCase）映射——is_main/custom 归一 ----

function fromWireAgent(a: AgentAdminEntry): AgentDef {
  return {
    id: a.id, name: a.name, desc: a.desc, color: a.color, model: a.model,
    tools: a.tools ?? [], workflow: a.workflow ?? "", skills: a.skills ?? [],
    delegates: a.delegates ?? [], approval: (a.approval || "confirm") as AgentDef["approval"],
    enabled: a.enabled, isMain: Boolean(a.is_main), prompt: a.prompt ?? "",
    protocol: a.protocol ?? "", custom: a.custom !== false,
  };
}

function toWireAgent(a: AgentDef): AgentAdminEntry {
  return {
    id: a.id, name: a.name, desc: a.desc, color: a.color, model: a.model,
    tools: a.tools, workflow: a.workflow, skills: a.skills, delegates: a.delegates,
    approval: a.approval, enabled: a.enabled, is_main: a.isMain || undefined,
    prompt: a.prompt, protocol: a.protocol ?? "", custom: a.custom !== false,
  };
}

function fromWireModule(m: AgentAdminModule): ContextModuleSpec {
  return { id: m.id, desc: m.desc, kind: m.kind, body: m.body, custom: m.custom };
}

function fromWireTool(t: AgentAdminTool): ToolSpec {
  return {
    id: t.id, desc: t.desc, risk: t.risk, source: t.source,
    params: t.params, doc: t.doc, server: t.server, command: t.command,
    example: t.example, packageFile: t.package_file, custom: t.custom,
  };
}

function toWireTool(t: ToolSpec): AgentAdminTool {
  return {
    id: t.id, desc: t.desc, risk: t.risk, source: t.source,
    params: t.params, doc: t.doc, server: t.server, command: t.command,
    example: t.example, package_file: t.packageFile, custom: t.custom !== false,
  };
}

function fromWireMcServer(m: AgentAdminMcServer): McServerSpec {
  return {
    id: m.id, desc: m.desc, transport: m.transport, command: m.command ?? "",
    args: m.args ?? [], env: m.env ?? {}, url: m.url ?? "",
    enabled: m.enabled, custom: m.custom,
  };
}

function toWireMcServer(m: McServerSpec): AgentAdminMcServer {
  return {
    id: m.id, desc: m.desc, transport: m.transport, command: m.command,
    args: m.args, env: m.env, url: m.url, enabled: m.enabled, custom: m.custom !== false,
  };
}

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

export function AgentsProvider({ source, children }: { source: AgentSource; children: ReactNode }) {
  const [agents, setAgents] = useState<AgentDef[]>(seedAgents);
  const [activeAgentId, setActiveAgentId] = useState("main");
  const [sessionDelegates, setSessionDelegates] = useState<string[] | null>(null);
  const [modules, setModules] = useState<ContextModuleSpec[]>(CONTEXT_MODULES);
  const [tools, setTools] = useState<ToolSpec[]>(() => [...BUILTIN_TOOLS, ...THIRD_PARTY_TOOLS]);
  const [mcpServers, setMcServers] = useState<McServerSpec[]>(MC_SERVERS);
  const admin = source.agentAdmin;

  // live 模式：后端是事实源——初始拉取 + changed 事件重同步（网络错误
  // 只 opError 提示，不打断 UI——种子兜底显示）。
  useEffect(() => {
    if (!admin) return;
    const pull = () => {
      setAgents(admin.agents().map(fromWireAgent));
      setModules(admin.modules().map(fromWireModule));
      setTools(admin.tools().map(fromWireTool));
      setMcServers(admin.mcpServers().map(fromWireMcServer));
    };
    pull();
    return admin.onChanged(pull);
  }, [admin]);

  const addAgent = useCallback((def: AgentDef) => {
    setAgents((list) => [...list, def]);
    void admin?.addAgent(toWireAgent(def)).catch((e) => sourceError(source, `注册 Agent 失败: ${e.message}`));
  }, [admin, source]);
  const updateAgent = useCallback(
    (def: AgentDef) => {
      setAgents((list) => list.map((a) => (a.id === def.id ? def : a)));
      void admin?.updateAgent(toWireAgent(def)).catch((e) => sourceError(source, `更新 Agent 失败: ${e.message}`));
    },
    [admin, source],
  );
  const removeAgent = useCallback((id: string) => {
    setAgents((list) => list.filter((a) => a.id !== id));
    // 删的是当前选用 → 回落主 Agent（入口永远存在）
    setActiveAgentId((cur) => (cur === id ? "main" : cur));
    void admin?.removeAgent(id).catch((e) => sourceError(source, `删除 Agent 失败: ${e.message}`));
  }, [admin, source]);
  const resetSessionDelegates = useCallback(() => setSessionDelegates(null), []);
  const addModule = useCallback((mod: ContextModuleSpec) => {
    setModules((list) => [...list, mod]);
    void admin?.addModule({ id: mod.id, desc: mod.desc, kind: mod.kind, body: mod.body, custom: mod.custom !== false })
      .catch((e) => sourceError(source, `保存模块失败: ${e.message}`));
  }, [admin, source]);
  const updateModule = useCallback(
    (mod: ContextModuleSpec) => {
      setModules((list) => list.map((x) => (x.id === mod.id ? mod : x)));
      void admin?.updateModule({ id: mod.id, desc: mod.desc, kind: mod.kind, body: mod.body, custom: mod.custom !== false })
        .catch((e) => sourceError(source, `更新模块失败: ${e.message}`));
    },
    [admin, source],
  );
  const removeModule = useCallback(
    (id: string) => {
      setModules((list) => list.filter((x) => x.id !== id));
      void admin?.removeModule(id).catch((e) => sourceError(source, `删除模块失败: ${e.message}`));
    },
    [admin, source],
  );
  const addTools = useCallback((list: ToolSpec[]) => {
    setTools((cur) => [...cur, ...list]);
    for (const t of list) {
      void admin?.addTool(toWireTool(t)).catch((e) => sourceError(source, `导入工具 ${t.id} 失败: ${e.message}`));
    }
  }, [admin, source]);
  const updateTool = useCallback(
    (tool: ToolSpec) => {
      setTools((cur) => cur.map((t) => (t.id === tool.id ? tool : t)));
      void admin?.updateTool(toWireTool(tool)).catch((e) => sourceError(source, `更新工具失败: ${e.message}`));
    },
    [admin, source],
  );
  const removeTool = useCallback(
    (id: string) => {
      setTools((cur) => cur.filter((t) => t.id !== id));
      void admin?.removeTool(id).catch((e) => sourceError(source, `删除工具失败: ${e.message}`));
    },
    [admin, source],
  );
  const addMcServer = useCallback((s: McServerSpec) => {
    setMcServers((list) => [...list, s]);
    void admin?.addMcServer(toWireMcServer(s)).catch((e) => sourceError(source, `添加服务器失败: ${e.message}`));
  }, [admin, source]);
  const updateMcServer = useCallback(
    (s: McServerSpec) => {
      setMcServers((list) => list.map((x) => (x.id === s.id ? s : x)));
      void admin?.updateMcServer(toWireMcServer(s)).catch((e) => sourceError(source, `更新服务器失败: ${e.message}`));
    },
    [admin, source],
  );
  const removeMcServer = useCallback(
    (id: string) => {
      setMcServers((list) => list.filter((x) => x.id !== id));
      void admin?.removeMcServer(id).catch((e) => sourceError(source, `删除服务器失败: ${e.message}`));
    },
    [admin, source],
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

/** live 写失败 → 一次性提示。经 DOM 事件桥到 App 的 store 归约（App 监听
 * lx-operation-error → reportError）——Provider 不持 AgentSource 的错误
 * 通道，自定义事件是零接口改动的最短路径（只有 live 模式会走到这里）。 */
export function sourceError(_source: AgentSource, message: string) {
  window.dispatchEvent(new CustomEvent("lx-operation-error", { detail: message }));
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
