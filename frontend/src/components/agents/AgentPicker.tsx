// 输入区 Agent 选择器：选「谁」来干活——默认主 Agent（编排模式）。
// 选中主 Agent 时菜单底部有「可委派」入口（ModelPicker 双面板同款语言）：
// 二级面板展示**可委派名单**（backend truth = 主 Agent 的名单默认 ∩ 启用
// 子 Agent，effectiveDelegates(agents, null)——sessionDelegates 是前端内存
// 态、后端分派不认它，这里不再呈现成可勾选的开关，只保留只读的覆盖指示）。
// 每行右侧是模型 chip（当前绑定或「未绑定」），点开第三级模型面板换绑定
// （updateAgent 乐观生效，先例 = Agents 页 enabled 开关）。子 Agent 选中时
// 无此入口——它们不可委派。
import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useAgents } from "../../shared/agents";
import { effectiveDelegates } from "../../shared/agent-delegation";
import { useSettings } from "../../shared/settings";
import { usePopover } from "../../shared/popover";
import { IconCheck, IconChevronDown, IconChevronRight } from "../icons";
import { ModelListPanel, focusModelSearch, type ModelListApi } from "../model-picker/ModelListPanel";

export function AgentPicker() {
  const { agents, activeAgentId, setActiveAgentId, sessionDelegates, setSessionDelegates, updateAgent } = useAgents();
  const { providers } = useSettings();
  const { open, toggle, requestClose, rootRef } = usePopover();
  const [panel, setPanel] = useState(false);
  // 第三级模型面板：当前展开的是哪个子 Agent 的行（null = 收起）
  const [modelPanelFor, setModelPanelFor] = useState<string | null>(null);
  const listApi = useRef<ModelListApi | null>(null);

  const current = agents.find((a) => a.id === activeAgentId) ?? agents.find((a) => a.isMain);
  const pickable = agents.filter((a) => a.enabled);
  // 可委派名单 = backend truth：主 Agent 名单默认 ∩ 启用子 Agent（不做会话
  // 覆盖的合成——那个覆盖后端不认，呈现在这里只会误导）。
  const delegates = effectiveDelegates(agents, null);
  const rows = delegates
    .map((id) => agents.find((a) => a.id === id))
    .filter((a): a is NonNullable<typeof a> => Boolean(a) && !a!.isMain);
  // 会话覆盖仍存在（App 会话切换时清）——只作只读指示，不再当开关
  const overridden = current?.isMain && sessionDelegates !== null;

  // 重新打开选择器时收起二级/三级面板
  const onToggle = () => {
    if (!open) { setPanel(false); setModelPanelFor(null); }
    toggle();
  };

  // 换子 Agent 的模型绑定：整个 AgentDef 原样回写、只改 model 字段
  // （乐观生效——后端回执经 agent.changed 重拉对齐；先例 = Agents 页 enabled 开关）
  const pickModel = (agentId: string, modelId: string) => {
    const agent = agents.find((a) => a.id === agentId);
    if (!agent) return;
    updateAgent({ ...agent, model: modelId });
    setModelPanelFor(null);
  };

  // 键盘：方向键/Enter 只在第三级模型面板开着时生效（转给 ModelListPanel）；
  // Esc 交给 usePopover 兜底收整个选择器。
  const onKeyDown = (e: KeyboardEvent) => {
    if (!open || !modelPanelFor) return;
    if (e.key === "Escape") return;
    listApi.current?.handleKey(e);
  };

  // 三级面板展开即聚焦搜索框（与 ModelPicker 打开即聚焦同款键盘流）
  useEffect(() => {
    if (modelPanelFor) focusModelSearch(rootRef.current);
  }, [modelPanelFor, rootRef]);

  if (!current) return null;

  return (
    <div className="pop-wrap" ref={rootRef} onKeyDown={onKeyDown}>
      <span className="pop-trigger" onClick={onToggle}>
        <button type="button" className="agent-chip" title="选择干活的 Agent">
          <span className="ag-dot" style={{ background: current.color }} />
          {current.name}
          {current.isMain && delegates.length > 0 && <span className="agent-chip-sub">· 可委派 {delegates.length}</span>}
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
            {current.isMain && rows.length > 0 && (
              <>
                <div className="mp-sep" />
                <button
                  type="button"
                  className={"mp-item mp-jump" + (panel ? " hl" : "")}
                  data-ap="delegates"
                  onClick={() => { setPanel((v) => !v); setModelPanelFor(null); }}
                >
                  <span className="mp-label">可委派（名单） · {delegates.length}</span>
                  <span className="mp-jump-arrow">
                    <IconChevronRight />
                  </span>
                </button>
              </>
            )}
          </div>
          {current.isMain && panel && (
            <div className="ap-panel" role="dialog" data-pop aria-label="可委派名单">
              <div className="mp-title">可委派（名单）</div>
              {overridden && (
                <button
                  type="button"
                  className="ap-note"
                  onClick={() => setSessionDelegates(null)}
                  title="本次会话有一份名单覆盖（后端分派不认它）——点这里清掉，回到名单默认"
                >
                  本次会话有名单覆盖 · 恢复默认
                </button>
              )}
              <div className="ap-list">
                {rows.map((s) => (
                  <div key={s.id} className="ap-row">
                    {/* 左半：整行可点但这里无切换语义（名单是配置不是会话态）——
                        左侧只作身份呈现，按钮语义留在模型 chip 上 */}
                    <span className="ap-row-main">
                      <span className="ag-dot" style={{ background: s.color }} />
                      <span className="mp-model-name">{s.name}</span>
                    </span>
                    <button
                      type="button"
                      className={"ap-model-chip" + (modelPanelFor === s.id ? " hl" : "")}
                      data-ap="model"
                      title="换这个子 Agent 绑定的模型"
                      onClick={() => setModelPanelFor((v) => (v === s.id ? null : s.id))}
                    >
                      {/* 模型显示名（display_name→model→id 回退链）；未绑定给灰态占位 */}
                      <span className="ap-model-label">
                        {s.model ? modelLabelOf(s.model, providers) : "未绑定"}
                      </span>
                      <IconChevronRight size={9} strokeWidth={2.2} />
                    </button>
                  </div>
                ))}
              </div>
              {modelPanelFor && (
                <div className="ap-model" data-pop>
                  <ModelListPanel
                    providers={providers}
                    selected={agents.find((a) => a.id === modelPanelFor)?.model}
                    onPick={(id) => pickModel(modelPanelFor, id)}
                    apiRef={listApi}
                  />
                </div>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
}

/** 模型显示名（注册表条目 → 展示名，找不到回落原始 id）。
 *  纯函数（非 hook）：id 在 map 里才确定，hook 不能在循环里调——
 *  useModelLabel 的同款查找逻辑在这里以纯函数形态复用。 */
function modelLabelOf(modelId: string, providers: ReturnType<typeof useSettings>["providers"]): string {
  const m = providers.flatMap((p) => p.models).find((x) => x.id === modelId);
  return m ? m.name : modelId;
}
