// 模型选择器（Codex 式双面板）：打开先显示模型列表（左，按提供商分组，
// 只显示启用的模型），点底部「推理强度 >」展开右面板调强度。两面板独立错位浮层。
// 档位是模型元数据（ModelMeta.efforts）：右面板只渲染当前模型支持的档位；
// 无档位模型（如 deepseek-chat）整个强度入口隐藏；切模型时当前档位不支持则回落。
// 开合/点外/Esc 关闭（含退场动画）由 usePopover 承担。
//
// **选中态 = 当前会话的模型**（`model` prop，来自 store 的 UIState.model：会话级
// 选择经 chat.model 落库 + chat.modelChanged 广播对齐）。不是全局 default 角色——
// 后者是「新会话的默认模型」（设置页管），拿它当选中态正是「一个会话切模型所有
// 会话跟着变」那个 bug 的成因。没传 prop（演示/子会话只读视图）才回落设置里的值。
//
// 列表纪律（模型一多就撑满屏幕——用户报的问题）：
//  - 面板**固定最大高度**（`max-height: min(420px, 60vh)`）+ 列表内部滚动；
//  - 顶部搜索框（按名称/描述/id 过滤），模型多时是唯一可用的入口；
//  - 分组标题**吸顶**（滚动时仍知道自己在哪家提供商下）；
//  - 键盘：↑/↓ 移动高亮、Enter 选中、Esc 关闭（Esc 由 usePopover 兜底），
//    高亮项滚动进视野（scrollIntoView block:nearest）。
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useSettings, EFFORTS, type EffortId } from "../../shared/settings";
import { usePopover } from "../../shared/popover";
import { IconCheck, IconChevronDown, IconChevronRight } from "../icons";

const searchIcon = (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="11" cy="11" r="7" />
    <path d="m21 21-4.3-4.3" />
  </svg>
);

export function ModelPicker({ model, onPickModel, chipTitle }: {
  /** 当前**会话**的模型 id（会话级状态；空/未传 = 回落全局 default 角色）。 */
  model?: string;
  /** 选模型：改当前会话（App → source.setSessionModel → chat.model）。未传 =
   *  退回设置页的全局语义（role.set，改「新会话的默认模型」）。 */
  onPickModel?: (id: string) => void;
  /** 芯片 title 覆盖（子会话页 busy 态用：「正在运行，切换将在下一轮生效」）。 */
  chipTitle?: string;
} = {}) {
  const { settings, set, providers } = useSettings();
  const { open, toggle, requestClose, rootRef } = usePopover();
  // 打开先显示模型（先选模型，再调强度）
  const [panel, setPanel] = useState<"model" | "effort">("model");
  // 搜索关键词与键盘高亮下标（-1 = 无高亮，首次 ↓ 落到当前选中项）
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(-1);
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  // 选中态：会话级模型优先；没传（演示/只读视图）回落全局 default 角色
  const selected = model ?? settings.model;

  // 只显示已连接且启用的提供商下的可见模型（设置「模型」分区控制）
  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    return providers
      .filter((p) => p.connected && p.enabled)
      .map((p) => ({
        name: p.name,
        models: p.models
          .filter((m) => m.visible)
          .filter((m) => !q || `${m.name} ${m.desc} ${m.id}`.toLowerCase().includes(q)),
      }))
      .filter((g) => g.models.length > 0);
  }, [providers, query]);

  // 扁平的可选序列（键盘导航与高亮用；分组只影响呈现）。
  // id 是注册表**主键**（provider::id 或纯 id）——两家提供商的同名模型是两个不同项。
  const flat = useMemo(() => groups.flatMap((g) => g.models.map((m) => m.id)), [groups]);

  // 有重名的模型才标注来源（提供商名）：组标题已经写着 host，再重复一遍纯噪音；
  // 只有用户在多个组里看到同一个模型名时，来源才是必须说清的信息。
  const dupNames = useMemo(() => {
    const byId = new Map<string, string[]>();
    for (const p of providers) {
      if (!p.connected || !p.enabled) continue;
      for (const m of p.models) byId.set(m.modelId, [...(byId.get(m.modelId) ?? []), p.name]);
    }
    return new Set([...byId].filter(([, names]) => names.length > 1).map(([id]) => id));
  }, [providers]);
  const hasQuery = query.trim().length > 0;

  // 当前模型的档位元数据
  const current = useMemo(
    () => providers.flatMap((p) => p.models).find((m) => m.id === selected),
    [providers, selected],
  );
  // 芯片上的模型名也标来源：同名模型时只报名字的话用户分不清选了哪家
  const currentDup = current ? dupNames.has(current.modelId) : false;
  const effortOptions = useMemo(() => EFFORTS.filter((e) => current?.efforts.includes(e.id)), [current]);
  const effortSupported = effortOptions.length > 0;
  // 无 provider 标签时（存量条目）来源就退成分组 host
  const providerNameOf = current ? (providers.find((p) => p.models.includes(current))?.name ?? "") : "";
  const effortLabel = effortSupported ? effortOptions.find((e) => e.id === settings.effort)?.label ?? effortOptions[effortOptions.length - 1].label : undefined;

  // 重新打开回到模型面板；清掉上一轮的搜索/高亮
  const onToggle = () => {
    if (!open) {
      setPanel("model");
      setQuery("");
      setActive(-1);
    }
    toggle();
  };

  // 打开即聚焦搜索框（键盘流：打开 → 直接打字过滤 → ↓/Enter）
  useEffect(() => {
    if (open && panel === "model") searchRef.current?.focus();
  }, [open, panel]);

  // 高亮项滚进视野（键盘移动时列表不能把光标顶到看不见的地方）
  useEffect(() => {
    if (active < 0) return;
    const el = listRef.current?.querySelector<HTMLElement>(".mp-item.kbd");
    el?.scrollIntoView({ block: "nearest" });
  }, [active]);

  // 切模型：当前档位在新模型上不存在 → 回落（优先"中"，否则新模型的首档）；
  // 选完带退场动画收起（最常见的关闭路径，手感要完整）
  const pickModel = (id: string) => {
    const m = providers.flatMap((p) => p.models).find((x) => x.id === id);
    const supported = m?.efforts ?? [];
    // 推理强度是本地偏好（request 级参数），切模型时档位不支持就回落；
    // 模型本身走会话级入口（onPickModel），不回写全局设置。
    if (supported.length > 0 && !supported.includes(settings.effort)) {
      set({ effort: (supported.includes("medium") ? "medium" : supported[0]) as EffortId });
    }
    if (onPickModel) onPickModel(id);
    else set({ model: id });
    requestClose();
  };

  // 键盘：↑/↓ 移动高亮、Enter 选中、Esc 关闭。
  // 搜索框与列表在同一个 root 里，监听挂 root（搜索框里打字时方向键照样生效）。
  const onKeyDown = (e: KeyboardEvent) => {
    if (!open) return;
    if (e.key === "Escape") {
      // usePopover 的 document 级 Esc 也会收；这里只管停掉面板内的默认行为
      return;
    }
    if (panel !== "model" || flat.length === 0) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const start = active >= 0 ? active : flat.indexOf(selected);
      const from = start >= 0 ? start : (e.key === "ArrowDown" ? -1 : flat.length);
      const next = e.key === "ArrowDown"
        ? (from + 1) % flat.length
        : (from - 1 + flat.length) % flat.length;
      setActive(next);
    } else if (e.key === "Enter" && active >= 0 && flat[active]) {
      e.preventDefault();
      pickModel(flat[active]);
    }
  };

  return (
    <div className="pop-wrap" ref={rootRef} onKeyDown={onKeyDown}>
      <span className="pop-trigger" onClick={onToggle}>
        <button type="button" className="model-chip" title={chipTitle ?? "模型与推理强度"}>
          {/* 芯片显示模型名（display_name→model→id 回退链），不裸显注册 id */}
          {current?.name ?? (selected || "模型")}
          {currentDup && current && <span className="chip-dim">· {current.provider || providerNameOf}</span>}
          {effortSupported && <span className="chip-dim">· {effortLabel}</span>}
          <IconChevronDown />
        </button>
      </span>
      {open && (
        <>
          {/* 左面板：模型列表（按提供商分组，只显示启用的）+ 底部强度跳转行（无档位模型隐藏） */}
          <div className="mp-panel mp-left" role="menu" data-pop>
            <div className="mp-title">模型</div>
            <label className="mp-search">
              {searchIcon}
              <input
                ref={searchRef}
                type="search"
                value={query}
                onChange={(e) => { setQuery(e.target.value); setActive(-1); }}
                placeholder="搜索模型"
                spellCheck={false}
                aria-label="搜索模型"
              />
            </label>
            <div className="mp-list" ref={listRef}>
              {groups.length === 0 && (
                <div className="mp-empty">
                  {hasQuery ? `没有匹配「${query}」的模型` : "没有启用的模型——到 设置 → 模型 里开启"}
                </div>
              )}
              {groups.map((g) => (
                <div key={g.name} className="mp-group">
                  <div className="mp-group-title">{g.name}</div>
                  {g.models.map((m) => (
                    <button
                      key={m.id}
                      type="button"
                      className={"mp-item" + (selected === m.id ? " on" : "") + (flat[active] === m.id ? " kbd" : "")}
                      role="menuitem"
                      onClick={() => pickModel(m.id)}
                    >
                      <span className="mp-label">
                        <span className="mp-model-name">{m.name}</span>
                        <span className="mp-model-desc">{m.desc}</span>
                        {dupNames.has(m.modelId) && (
                          <span className="mp-model-desc">来源 {m.provider || g.name}</span>
                        )}
                      </span>
                      {selected === m.id && <span className="mp-check">{<IconCheck />}</span>}
                    </button>
                  ))}
                </div>
              ))}
            </div>
            {effortSupported && (
              <>
                <div className="mp-sep" />
                <button
                  type="button"
                  className={"mp-item mp-jump" + (panel === "effort" ? " hl" : "")}
                  onClick={() => setPanel(panel === "effort" ? "model" : "effort")}
                >
                  <span className="mp-label">推理强度 · {effortLabel}</span>
                  <span className="mp-jump-arrow">{<IconChevronRight />}</span>
                </button>
              </>
            )}
          </div>
          {/* 右面板：只渲染当前模型支持的档位（点左面板底部行展开） */}
          {effortSupported && panel === "effort" && (
            <div className="mp-panel mp-right" role="menu" data-pop>
              <div className="mp-title">推理强度</div>
              {effortOptions.map((e) => (
                <button
                  key={e.id}
                  type="button"
                  className={"mp-item" + (effortLabel === e.label ? " on" : "")}
                  role="menuitem"
                  title={e.hint}
                  onClick={() => { set({ effort: e.id }); requestClose(); }}
                >
                  <span className="mp-label">{e.label}</span>
                  {effortLabel === e.label && <span className="mp-check">{<IconCheck />}</span>}
                </button>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
