// Agent 名单页：主 Agent 卡置顶（调度中枢的特殊态）+ 已注册网格 + 组装
// 编辑子视图。编辑态是页内子路由（editing 持有草稿，取消即丢弃——切换
// 视图回聊天也会丢，原型语义：名单才是事实源）。
import { useEffect, useRef, useState } from "react";
import { blankAgent, useAgents, type AgentDef } from "../../shared/agents";
import { staggerIn } from "../../shared/motion";
import { Button } from "../form";
import { AgentCard } from "./AgentCard";
import { AgentEditor } from "./AgentEditor";

export function AgentsPage() {
  const { agents, addAgent, updateAgent, removeAgent, activeAgentId, setActiveAgentId } = useAgents();
  const [editing, setEditing] = useState<{ def: AgentDef; isNew: boolean } | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  // 名单交错入场：挂载与「从编辑器返回」时播（editing 变化驱动重播）；
  // 编辑器视图下名单节点不存在，effect 直接跳过
  useEffect(() => {
    if (editing) return;
    const el = rootRef.current;
    if (!el) return;
    staggerIn(el.querySelectorAll(".ag-head, .ag-card, .ag-grid-label, .ag-empty-inline"), { each: 0.05 });
  }, [editing]);

  if (editing) {
    return (
      <AgentEditor
        key={editing.def.id}
        initial={editing.def}
        isNew={editing.isNew}
        onCancel={() => setEditing(null)}
        onSave={(saved) => {
          if (editing.isNew) addAgent(saved);
          else updateAgent(saved);
          setEditing(null);
        }}
      />
    );
  }

  const main = agents.find((a) => a.isMain);
  const others = agents.filter((a) => !a.isMain);
  const enabledCount = agents.filter((a) => a.enabled).length;

  /** 停用的是输入区当前选用的 Agent → 回落主 Agent。 */
  const toggle = (a: AgentDef) => {
    updateAgent({ ...a, enabled: !a.enabled });
    if (a.enabled && activeAgentId === a.id) setActiveAgentId("main");
  };

  return (
    <div className="ag-page" ref={rootRef}>
      <div className="ag-head">
        <div>
          <div className="ag-title">Agent 名单</div>
          <div className="ag-sub">
            {agents.length} 个 Agent · {enabledCount} 个启用 —— 主 Agent 唯一调度，子 Agent 按能力组装
          </div>
        </div>
        <Button variant="primary" data-ag="new" onClick={() => setEditing({ def: blankAgent(main?.model ?? "", agents.map((a) => a.color)), isNew: true })}>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
          组装新 Agent
        </Button>
      </div>

      {main && <AgentCard agent={main} onEdit={() => setEditing({ def: main, isNew: false })} />}

      <div className="ag-grid-label">子 Agent（{others.length}）</div>
      <div className="ag-grid">
        {others.map((a) => (
          <AgentCard
            key={a.id}
            agent={a}
            onEdit={() => setEditing({ def: a, isNew: false })}
            onToggle={() => toggle(a)}
            onDelete={() => removeAgent(a.id)}
          />
        ))}
      </div>
      {others.length === 0 && (
        <div className="ag-empty-inline">还没有子 Agent——点右上「组装新 Agent」开始</div>
      )}
    </div>
  );
}
