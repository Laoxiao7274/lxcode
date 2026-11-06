// 助手配置页签（拓展页第五页签 / 助手模式独立配置页）：助手性格与拓展的
// 可视化编辑——数据源 assistant.config.get/set（后端 config/assistant.json，
// 下一轮生效）。
//
// 与 AgentEditor 的同款组件复用：TextInput（名字）/ Textarea（persona）/
// Chips（技能多选、模板单选 exclusive、MCP 服务器多选）。保存是**全量替换**
//（set 带什么存什么——后端把 skills/workflow/mcpServers 的 id 现读校验，
// 无效 id 报人话错误原样展示）。
//
// MCP 服务器区（2026-12 用户拍板：助手拓展能力完整化）数据源 = useAgents()
// 的 mcpServers 列表，**绑定的是服务器 id**（该服务器全部已物化工具合入
// 白名单）。区头带管理入口（onOpenCatalog——调用方决定去拓展页的哪条路：
// 助手模式独立页切 secondaryView("catalog") + setCatalogTabPref，拓展页内
// 切页签），并明示安全语义：MCP 工具在助手 auto 审批档下不经确认直接执行。
import { useEffect, useState } from "react";
import type { AssistantConfig } from "../../shared/types";
import { getAgentSource } from "../../agent";
import { useAgents } from "../../shared/agents";
import { Button, Chips, TextInput, Textarea } from "../form";
import { ToolCardPicker } from "../agents/ToolCardPicker";

/** 触发警示标注的写类工具（勾任一时明示「自动执行档下不经确认直接运行」）。 */
const WRITE_TOOLS = new Set(["bash", "edit", "write_file"]);

/** 基础能力三件套的去勾后果说明（warn 标注用——不强制保留，让用户知情）。
 *  只在**去勾**时显示：toolWhitelist 非空（已保存过白名单或本轮点过卡片）且
 *  该 id 不在选中集合——默认态/未保存态不显示任何后果说明（2026-12 用户反馈：
 *  常驻三行看不懂是干嘛的）。 */
const CORE_TOOLS = ["run_task", "update_assistant_config", "mcp_manage"] as const;
const CORE_HINTS: Record<string, string> = {
  run_task: "run_task——失去向项目派发任务的能力",
  update_assistant_config: "update_assistant_config——失去自改配置的能力",
  mcp_manage: "mcp_manage——失去管理 MCP 服务器的能力",
};

/** onOpenCatalog 的目标页签（与 CatalogPage 的 Tab 的可跳子集——助手配置里
 *  只提供「去管技能」「去管 MCP 服务器」「去建工具」三个入口）。 */
export type AssistantCatalogTarget = "skills" | "mcp" | "tools";

/** 分组空态（2026-12 用户反馈：自定义工具区只有 hint 没有 chips，看起来像
 *  功能缺失）：空时给一个可点的空态入口，与 manageEntry 同一条
 *  onOpenCatalog 通路（data-cg 钩子同款命名，便于测试与自动化点按）。 */
function EmptyEntry({ target, label, onOpenCatalog }: { target: AssistantCatalogTarget; label: string; onOpenCatalog?: (tab: AssistantCatalogTarget) => void }) {
  if (!onOpenCatalog) return null;
  return (
    <button
      type="button"
      className="ag-chip-empty"
      data-cg={`empty-${target}`}
      onClick={() => onOpenCatalog(target)}
      title={`去拓展页管理${target === "mcp" ? " MCP 服务器" : target === "tools" ? "工具" : "技能"}`}
    >
      {label} →
    </button>
  );
}

export function AssistantConfigTab({ onOpenCatalog }: { onOpenCatalog?: (tab: AssistantCatalogTarget) => void }) {
  const source = getAgentSource();
  const assistant = source.assistant;
  const { modules, mcpServers, tools } = useAgents();
  const [cfg, setCfg] = useState<AssistantConfig>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    let alive = true;
    if (!assistant) {
      setLoading(false);
      setError("当前数据源不支持助手配置（演示模式有最小实现）");
      return;
    }
    assistant
      .getConfig()
      .then((c) => {
        if (alive) {
          setCfg(c);
          setError("");
        }
      })
      .catch((e) => {
        if (alive) setError(`读取助手配置失败: ${e instanceof Error ? e.message : String(e)}`);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    // configChanged 广播对齐（别的客户端 / 助手自己经 update_assistant_config
    // 改了性格——打开着的页签立即反映最新值，不等到重开）。
    const off = source.subscribe((ev) => {
      if (ev.type === "assistantConfigChanged" && alive) setCfg(ev.config);
    });
    return () => {
      alive = false;
      off();
    };
  }, [assistant, source]);

  if (!assistant || loading) {
    return <div className="cg-new-note">{loading ? "读取助手配置…" : "当前数据源不支持助手配置。"}</div>;
  }

  const toModuleChip = (m: { id: string; desc: string; kind: "process" | "skill" }) => ({
    value: m.id,
    label: m.id,
    desc: `${m.desc}（${m.kind === "process" ? "模板" : "技能"}）`,
  });
  const processChips = modules.filter((m) => m.kind === "process").map(toModuleChip);
  const skillChips = modules.filter((m) => m.kind === "skill").map(toModuleChip);
  const mcpChips = mcpServers.map((s) => ({ value: s.id, label: s.id, desc: s.desc || "MCP 服务器" }));
  // 工具白名单（2026-12 用户拍板：默认工具完全可配）：
  //   - 预填 = whitelist 非空 ? whitelist :（builtinDefaults ∪ 旧 cfg.tools）
  //     ——builtinDefaults 来自服务端 get 应答（事实源，前端不写死 14 项）；
  //     旧 tools 并入预填是迁移路径（保存时写 whitelist、清空旧 tools，防双源）；
  //   - 「恢复默认」= 清空 whitelist（回到内置默认）。
  const builtinDefaults = cfg.builtinDefaults ?? [];
  const whitelist = cfg.toolWhitelist?.length
    ? cfg.toolWhitelist
    : Array.from(new Set([...builtinDefaults, ...(cfg.tools ?? [])]));
  // 分组载荷（AgentEditor 同款四组：内置 / 第三方 / MCP / 自定义）。
  const toolGroups = [
    { title: "内置", items: tools.filter((t) => !t.custom && t.source === "builtin") },
    { title: "第三方 / 插件", items: tools.filter((t) => !t.custom && t.source === "binary") },
    { title: "MCP", items: tools.filter((t) => !t.custom && t.source === "mcp") },
    { title: "自定义", items: tools.filter((t) => t.custom) },
  ].filter((g) => g.items.length > 0);
  // 写类警示（勾任一写类工具时明示 auto 档不经确认）。
  const writePicked = whitelist.some((id) => WRITE_TOOLS.has(id));
  // 基础能力警示只在**去勾**时出现：cfg.toolWhitelist 非空（保存过白名单或
  // 本轮点过卡片——onChange 落 cfg.toolWhitelist）且该 id 不在选中集合。
  // 默认态（whitelist 来自预填并集）不显示——2026-12 用户反馈：三行常驻
  // 看不懂是干嘛的。
  const coreMissing =
    (cfg.toolWhitelist?.length ?? 0) > 0
      ? CORE_TOOLS.filter((id) => !whitelist.includes(id))
      : [];
  const dirty =
    (cfg.name ?? "") !== "" || (cfg.persona ?? "") !== "" || (cfg.skills?.length ?? 0) > 0 ||
    (cfg.workflow ?? "") !== "" || (cfg.mcpServers?.length ?? 0) > 0 ||
    (cfg.toolWhitelist?.length ?? 0) > 0 || (cfg.tools?.length ?? 0) > 0;

  const manageEntry = (target: AssistantCatalogTarget, label: string) =>
    onOpenCatalog ? (
      <Button
        variant="ghost"
        className="ag-chip-manage"
        data-cg={`manage-${target}`}
        onClick={() => onOpenCatalog(target)}
        title={`去拓展页管理${target === "mcp" ? " MCP 服务器" : target === "tools" ? "工具" : "技能"}`}
      >
        {label} →
      </Button>
    ) : null;

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      await assistant.setConfig(cfg);
      setSaved(true);
      setTimeout(() => setSaved(false), 2500);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="asstcfg">
      <div className="cg-new-note">助手的性格与拓展绑定（保存后下一轮对话生效——正在生成的回复不受影响）。空 = 用内置值。</div>
      {error && <div className="asstcfg-error">{error}</div>}
      <section className="ag-sec">
        <div className="ag-sec-title">身份</div>
        <TextInput
          className="ag-name-input"
          value={cfg.name ?? ""}
          onChange={(v) => setCfg((c) => ({ ...c, name: v }))}
          placeholder="显示名（空 = 助手）"
          aria-label="助手显示名"
        />
        <div className="ag-chip-label">性格</div>
        <Textarea
          value={cfg.persona ?? ""}
          onChange={(v) => setCfg((c) => ({ ...c, persona: v }))}
          placeholder="性格/语气设定，一句话（如：轻松幽默，回答简洁）——空 = 无设定（助手会主动问你）"
          ariaLabel="助手性格"
        />
        <div className="ag-hint">性格作为补充设定追加在助手职责协议之后——不替换协议本身。</div>
      </section>
      <section className="ag-sec">
        <div className="ag-sec-title">
          上下文<span className="ag-count">{(cfg.workflow ? 1 : 0) + (cfg.skills?.length ?? 0) + (cfg.mcpServers?.length ?? 0) + (cfg.toolWhitelist?.length ?? 0) + (cfg.tools?.length ?? 0)}</span>
        </div>
        <div className="ag-chip-label">模板</div>
        <Chips
          exclusive
          options={processChips}
          value={cfg.workflow ? [cfg.workflow] : []}
          onChange={(v) => setCfg((c) => ({ ...c, workflow: v[0] ?? "" }))}
          ariaLabel="助手模板（单选）"
        />
        <div className="ag-chip-label">技能{manageEntry("skills", "管理技能")}</div>
        {skillChips.length > 0 ? (
          <Chips
            options={skillChips}
            value={cfg.skills ?? []}
            onChange={(skills) => setCfg((c) => ({ ...c, skills }))}
            ariaLabel="助手技能"
          />
        ) : (
          <EmptyEntry target="skills" label="还没有技能——去建一个" onOpenCatalog={onOpenCatalog} />
        )}
        <div className="ag-chip-label">MCP 服务器{manageEntry("mcp", "管理 MCP 服务器")}</div>
        {mcpChips.length > 0 ? (
          <Chips
            options={mcpChips}
            value={cfg.mcpServers ?? []}
            onChange={(mcpServers) => setCfg((c) => ({ ...c, mcpServers }))}
            ariaLabel="助手 MCP 服务器（多选——绑定的是服务器 id，非工具名）"
          />
        ) : (
          <EmptyEntry target="mcp" label="还没有 MCP 服务器——去建一个" onOpenCatalog={onOpenCatalog} />
        )}
        <div className="ag-hint">
          安装/启用的 MCP 服务器，其工具将直接对助手可用（助手当前权限档为自动执行）。
        </div>
        <div className="ag-chip-label">工具<span className="ag-count">{whitelist.length}</span></div>
        {/* 工具白名单卡片选择器（2026-12 用户拍板：助手默认工具完全可配——
            AgentEditor 同款 ToolCardPicker，四组内置/第三方/MCP/自定义全目录可选，
            含内置写类 bash/edit——用户担责）。
            预填 = whitelist 非空 ? whitelist :（builtinDefaults ∪ 旧 tools）——
            builtinDefaults 是服务端带回的内置默认 14 项（前端不写死，防漂移）；
            旧 cfg.tools 并入预填（迁移路径：保存时写 whitelist 并清空旧 tools）。 */}
        <ToolCardPicker
          groups={toolGroups}
          value={whitelist}
          onChange={(toolWhitelist) => setCfg((c) => ({ ...c, toolWhitelist, tools: undefined }))}
          focusedValue={null}
          onFocus={() => {}}
          total={tools.length}
        />
        {writePicked && (
          <div className="asstcfg-warn" role="note">
            已勾选写类工具（bash/edit/write_file）：助手在自动执行档下**不经确认直接运行**——确保命令只做你授权的事。
          </div>
        )}
        {coreMissing.length > 0 && (
          <div className="asstcfg-warn" role="note">
            {coreMissing.map((id) => (
              <div key={id}>已去掉 {CORE_HINTS[id] ?? id}。不强制保留，按需取舍。</div>
            ))}
          </div>
        )}
        <div className="asstcfg-actions asstcfg-tools-actions">
          <Button
            variant="ghost"
            data-cg="restore-tool-defaults"
            onClick={() => setCfg((c) => ({ ...c, toolWhitelist: undefined, tools: undefined }))}
            disabled={whitelist.length === 0}
          >
            恢复默认
          </Button>
          <span className="ag-hint">
            恢复默认 = 清空白名单（回到内置 {builtinDefaults.length || 14} 项）。
          </span>
        </div>
        <div className="ag-hint">
          绑定的工具在助手权限档（自动执行）下不经确认直接运行；MCP 服务器的工具始终按下方 MCP 绑定单独合入（与本清单正交）。
        </div>
      </section>
      <div className="asstcfg-actions">
        <Button variant="primary" disabled={saving} onClick={save}>
          {saving ? "保存中…" : "保存"}
        </Button>
        {saved && <span className="asstcfg-saved">已保存——下一轮对话生效</span>}
        {!dirty && <span className="ag-hint">全部为空 = 用内置值（内置名「助手」、无性格、不绑技能）。</span>}
      </div>
    </div>
  );
}
