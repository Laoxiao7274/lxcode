// 输入区 Agent 选择器：选「谁」来干活——默认主 Agent（编排模式）。
// 选中主 Agent 时菜单底部有「可委派」入口（ModelPicker 双面板同款语言）：
// 二级面板调整本次会话的委派名单（会话级覆盖；新会话/切换会话回到
// 名单默认）。子 Agent 选中时无此入口——它们不可委派。
import { useState } from "react";
import { useAgents } from "../../shared/agents";
import { effectiveDelegates } from "../../shared/agent-delegation";
import { usePopover } from "../../shared/popover";
import { IconCheck, IconChevronDown, IconChevronRight } from "../icons";

export function AgentPicker() {
  const { agents, activeAgentId, setActiveAgentId, sessionDelegates, setSessionDelegates } = useAgents();
  const { open, toggle, requestClose, rootRef } = usePopover();
  const [panel, setPanel] = useState(false);

  const current = agents.find((a) => a.id === activeAgentId) ?? agents.find((a) => a.isMain);
  const pickable = agents.filter((a) => a.enabled);
  // 参与分派的子 Agent（启用的才算）——委派面板的展示范围
  const subs = agents.filter((a) => !a.isMain && a.enabled);
  // 有效委派名单：会话覆盖 ?? 主 Agent 的名单默认（∩ 启用的子 Agent）
  const effective = current?.isMain ? effectiveDelegates(agents, sessionDelegates) : [];

  // 重新打开选择器时收起二级面板
  const onToggle = () => {
    if (!open) setPanel(false);
    toggle();
  };

  // 切换委派：以当前有效名单为底做增删（写会话覆盖）
  const toggleDelegate = (id: string) => {
    const next = effective.includes(id) ? effective.filter((x) => x !== id) : [...effective, id];
    setSessionDelegates(next);
  };

  if (!current) return null;

  return (
    <div className="pop-wrap" ref={rootRef}>
      <span className="pop-trigger" onClick={onToggle}>
        <button type="button" className="agent-chip" title="选择干活的 Agent">
          <span className="ag-dot" style={{ background: current.color }} />
          {current.name}
          {current.isMain && effective.length > 0 && <span className="agent-chip-sub">· 可委派 {effective.length}</span>}
          <IconChevronDown size={9} strokeWidth={2.2} />
        </button>
      </span>
      {open && (
        <>
          <div className="perm-menu" role="menu" data-pop>
            {pickable.map((a) => (
              <button
                key={a.id}
                type="button"
                className={"perm-item" + (a.id === current.id ? " on" : "")}
                role="menuitem"
                onClick={() => {
                  setActiveAgentId(a.id);
                  requestClose();
                }}
              >
                <span className="ag-dot" style={{ background: a.color }} />
                <span className="perm-text">
                  <span className="perm-label">
                    {a.name}
                    {a.isMain && <span className="ag-main-badge">主</span>}
                  </span>
                  <span className="perm-desc">{a.desc || a.model}</span>
                </span>
                {a.id === current.id && <IconCheck />}
              </button>
            ))}
            {current.isMain && subs.length > 0 && (
              <>
                <div className="mp-sep" />
                <button
                  type="button"
                  className={"mp-item mp-jump" + (panel ? " hl" : "")}
                  data-ap="delegates"
                  onClick={() => setPanel((v) => !v)}
                >
                  <span className="mp-label">可委派 · {effective.length}</span>
                  <span className="mp-jump-arrow">
                    <IconChevronRight />
                  </span>
                </button>
              </>
            )}
          </div>
          {current.isMain && panel && (
            <div className="ap-panel" role="dialog" data-pop aria-label="本次会话可委派">
              <div className="mp-title">本次会话可委派</div>
              {subs.map((s) => (
                <button
                  key={s.id}
                  type="button"
                  className={"mp-item" + (effective.includes(s.id) ? " on" : "")}
                  role="menuitemcheckbox"
                  aria-checked={effective.includes(s.id)}
                  onClick={() => toggleDelegate(s.id)}
                >
                  <span className="ag-dot" style={{ background: s.color }} />
                  <span className="mp-label">
                    <span className="mp-model-name">{s.name}</span>
                    <span className="mp-model-desc">{s.desc}</span>
                  </span>
                  {effective.includes(s.id) && <span className="mp-check"><IconCheck /></span>}
                </button>
              ))}
              {sessionDelegates !== null && (
                <>
                  <div className="mp-sep" />
                  <button
                    type="button"
                    className="mp-item"
                    onClick={() => setSessionDelegates(null)}
                    title="清掉本次会话的覆盖，回到主 Agent 的名单默认"
                  >
                    <span className="mp-label">恢复名单默认</span>
                  </button>
                </>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}
