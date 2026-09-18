// 拓展管理页：工具 / 技能 / 模板 / MCP 服务器 四类可插拔拓展的浏览、
// 查看与自建。
// 三类统一：新建/编辑走表单弹窗（不让人写 JSON）+ 导入（固定格式 v1
// 的文件/粘贴——分发通道）+ 导出下载（自建条目分享）。工具的可执行
// 承载随后端化接插件机制。页签切目录（Segmented），条目卡网格；
// 点卡片开文档弹窗。卡片渲染器在 ./cards（EntryCard 及载荷装配）。
import { useEffect, useRef, useState } from "react";
import { blankModule, useAgents, type ContextModuleSpec, type McServerSpec, type ToolSpec } from "../../shared/agents";
import { downloadJson } from "../../shared/download";
import { serializeModuleExport } from "../../shared/module-import";
import { serializeToolExport } from "../../shared/tool-import";
import { staggerIn } from "../../shared/motion";
import { Button, Segmented } from "../form";
import { McServerCard, ModuleCard, ToolCard, useMcpToolCounts } from "./cards";
import { DocDialog, type Focus } from "./DocDialog";
import { McConfigImportDialog } from "./McConfigImportDialog";
import { McServerEditor } from "./McServerEditor";
import { ModuleEditor } from "./ModuleEditor";
import { ModuleImportDialog } from "./ModuleImportDialog";
import { ToolEditor } from "./ToolEditor";
import { ToolImportDialog } from "./ToolImportDialog";
import { IconDownload } from "../icons";

type Tab = "tools" | "skills" | "templates" | "mcp";

export function CatalogPage() {
  const { modules, addModule, updateModule, removeModule, tools, addTools, updateTool, removeTool, mcpServers, addMcServer, updateMcServer, removeMcServer } = useAgents();
  const toolCountOf = useMcpToolCounts();
  const [tab, setTab] = useState<Tab>("tools");
  const [focus, setFocus] = useState<Focus | null>(null);
  const [editing, setEditing] = useState<{ mod: ContextModuleSpec; isNew: boolean } | null>(null);
  const [editingTool, setEditingTool] = useState<{ tool: ToolSpec; isNew: boolean } | null>(null);
  const [editingServer, setEditingServer] = useState<{ server: McServerSpec } | null>(null);
  const [importingMc, setImportingMc] = useState(false);
  const [importing, setImporting] = useState<"tools" | "modules" | null>(null);
  const gridRef = useRef<HTMLDivElement>(null);

  // 页签切换重播条目交错入场
  useEffect(() => {
    const el = gridRef.current;
    if (!el) return;
    staggerIn(el.querySelectorAll(".cg-card"), { each: 0.028 });
  }, [tab]);

  const skills = modules.filter((m) => m.kind === "skill");
  const templates = modules.filter((m) => m.kind === "process");
  const moduleTabKind = tab === "templates" ? "process" : "skill";
  const customOfTab = (tab === "templates" ? templates : skills).filter((m) => m.custom);
  const moduleTabName = tab === "templates" ? "模板" : "技能";
  const customTools = tools.filter((t) => t.custom);

  const tabs = [
    { value: "tools" as const, label: `工具 · ${tools.length}`, hint: "内置、第三方与 MCP 工具" },
    { value: "skills" as const, label: `技能 · ${skills.length}`, hint: "领域知识与方法（多选注入）" },
    { value: "templates" as const, label: `模板 · ${templates.length}`, hint: "工作方式模板（单选注入）" },
    { value: "mcp" as const, label: `MCP · ${mcpServers.length}`, hint: "MCP 服务器接入（能力以工具进拓展）" },
  ];

  const exportModules = () => {
    if (customOfTab.length === 0) return;
    downloadJson(`lxcode-${tab}.json`, serializeModuleExport(customOfTab));
  };
  const exportTools = () => {
    if (customTools.length === 0) return;
    downloadJson("lxcode-tools.json", serializeToolExport(customTools));
  };

  return (
    <div className="cg-page">
      <div className="ag-head">
        <div>
          <div className="ag-title">拓展</div>
          <div className="ag-sub">可插拔的能力拓展——工具、技能、模板与 MCP 服务器；Agent 组装时从这里勾选注入</div>
        </div>
        <div className="cg-head-actions">
          {tab === "tools" ? (
            <>
              {customTools.length > 0 && (
                <Button variant="ghost" data-cg="export-tools" onClick={exportTools} title={`下载 ${customTools.length} 个自定义工具（v1 JSON——可分享导入）`}>
                  <IconDownload />
                  导出
                </Button>
              )}
              <Button variant="ghost" data-cg="import" onClick={() => setImporting("tools")}>
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M12 3v12" />
                  <path d="m7 10 5 5 5-5" />
                  <path d="M5 21h14" />
                </svg>
                导入
              </Button>
              <Button
                variant="primary"
                data-cg="new"
                onClick={() => setEditingTool({ tool: { id: "", desc: "", risk: "low", source: "binary" }, isNew: true })}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                  <path d="M12 5v14M5 12h14" />
                </svg>
                新建工具
              </Button>
            </>
          ) : tab === "mcp" ? (
            <Button variant="primary" data-cg="import-mc" onClick={() => setImportingMc(true)}>
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M12 3v12" />
                <path d="m7 10 5 5 5-5" />
                <path d="M5 21h14" />
              </svg>
              添加服务器
            </Button>
          ) : (
            <>
              {customOfTab.length > 0 && (
                <Button variant="ghost" data-cg="export" onClick={exportModules} title={`下载 ${customOfTab.length} 个自定义${moduleTabName}（v1 JSON——可分享导入）`}>
                  <IconDownload />
                  导出
                </Button>
              )}
              <Button variant="ghost" data-cg="import-modules" onClick={() => setImporting("modules")}>
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M12 3v12" />
                  <path d="m7 10 5 5 5-5" />
                  <path d="M5 21h14" />
                </svg>
                导入
              </Button>
              <Button
                variant="primary"
                data-cg="new"
                onClick={() => setEditing({ mod: blankModule(moduleTabKind), isNew: true })}
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
                  <path d="M12 5v14M5 12h14" />
                </svg>
                新建{moduleTabName}
              </Button>
            </>
          )}
        </div>
      </div>
      {tab === "tools" && (
        <div className="cg-new-note">单个工具用「新建工具」表单；导入/导出是分发通道（固定格式 v1 的 JSON 文件）；自定义工具的可执行承载随后端化接插件机制。</div>
      )}
      {tab === "mcp" && (
        <div className="cg-new-note">注册 = 贴配置片段（mcpServers 标准形态：stdio = 命令 + 参数 + 环境变量；SSE = 端点 URL）；注册后暴露的能力以工具形式进工具拓展；停用 = 能力挂起（工具保留）。</div>
      )}
      <div className="cg-tabs">
        <Segmented options={tabs} value={tab} onChange={setTab} ariaLabel="拓展页签" />
      </div>
      <div className="cg-grid" ref={gridRef}>
        {tab === "tools" && tools.map((t) => (
          <ToolCard
            key={t.id}
            tool={t}
            onOpen={() => setFocus({ kind: "tool", id: t.id })}
            onEdit={t.custom ? () => setEditingTool({ tool: t, isNew: false }) : undefined}
            onDelete={t.custom ? () => removeTool(t.id) : undefined}
          />
        ))}
        {tab === "skills" && skills.map((m) => (
          <ModuleCard
            key={m.id}
            mod={m}
            onOpen={() => setFocus({ kind: "module", id: m.id })}
            onEdit={m.custom ? () => setEditing({ mod: m, isNew: false }) : undefined}
            onDelete={m.custom ? () => removeModule(m.id) : undefined}
          />
        ))}
        {tab === "templates" && templates.map((m) => (
          <ModuleCard
            key={m.id}
            mod={m}
            onOpen={() => setFocus({ kind: "module", id: m.id })}
            onEdit={m.custom ? () => setEditing({ mod: m, isNew: false }) : undefined}
            onDelete={m.custom ? () => removeModule(m.id) : undefined}
          />
        ))}
        {tab === "mcp" && mcpServers.map((s) => (
          <McServerCard
            key={s.id}
            server={s}
            toolCount={toolCountOf(s.id)}
            onEdit={() => setEditingServer({ server: s })}
            onToggle={() => updateMcServer({ ...s, enabled: !s.enabled })}
            onDelete={s.custom ? () => removeMcServer(s.id) : undefined}
          />
        ))}
      </div>
      {focus && <DocDialog focus={focus} onClose={() => setFocus(null)} />}
      {editingServer && (
        <McServerEditor
          key={editingServer.server.id || "edit"}
          initial={editingServer.server}
          onCancel={() => setEditingServer(null)}
          onSave={(saved) => {
            updateMcServer(saved);
            setEditingServer(null);
          }}
        />
      )}
      {importingMc && (
        <McConfigImportDialog
          onClose={() => setImportingMc(false)}
          onImport={(imported) => imported.forEach(addMcServer)}
        />
      )}
      {editingTool && (
        <ToolEditor
          key={editingTool.tool.id || "new"}
          initial={editingTool.tool}
          isNew={editingTool.isNew}
          onCancel={() => setEditingTool(null)}
          onSave={(saved) => {
            if (editingTool.isNew) addTools([saved]);
            else updateTool(saved);
            setEditingTool(null);
          }}
        />
      )}
      {editing && (
        <ModuleEditor
          key={editing.mod.id || "new"}
          initial={editing.mod}
          isNew={editing.isNew}
          onCancel={() => setEditing(null)}
          onSave={(saved) => {
            if (editing.isNew) addModule(saved);
            else updateModule(saved);
            setEditing(null);
          }}
        />
      )}
      {importing === "tools" && (
        <ToolImportDialog
          onClose={() => setImporting(null)}
          onImport={(imported) => {
            addTools(imported);
            setImporting(null);
          }}
        />
      )}
      {importing === "modules" && (
        <ModuleImportDialog
          kind={moduleTabKind}
          onClose={() => setImporting(null)}
          onImport={(imported) => {
            imported.forEach(addModule);
            setImporting(null);
          }}
        />
      )}
    </div>
  );
}
