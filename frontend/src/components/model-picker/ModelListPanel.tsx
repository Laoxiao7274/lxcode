// 模型列表面板（从 ModelPicker 抽出，ModelPicker 与 AgentPicker 的第三级
// 换模型面板共用——搜索/分组/滚动高亮/键盘导航只有一份实现）。
// 只负责列表呈现与选择：搜索过滤、按提供商分组、同名来源标注、
// ↑/↓ 移动高亮、Enter 选中、高亮项滚进视野。
// 面板骨架（mp-panel 样式、标题、强度入口、开合与点外/Esc 收起）由宿主拼装。
//
// 键盘：宿主在**根元素**上监听 onKeyDown 并经 apiRef 转发到 handleKey——
// 与抽取前 ModelPicker 的单点监听同构（焦点在搜索框或列表项上都生效）；
// Esc 不在这里处理（usePopover 的 document 级兜底负责收面板）。
import { useEffect, useImperativeHandle, useMemo, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import type { ProviderMeta } from "../../shared/settings";
import { IconCheck } from "../icons";

const searchIcon = (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="11" cy="11" r="7" />
    <path d="m21 21-4.3-4.3" />
  </svg>
);

/** 宿主键盘转发的句柄：根 onKeyDown 里调 handleKey(e) 即完成 ↑/↓/Enter 导航。 */
export interface ModelListApi {
  handleKey: (e: KeyboardEvent) => void;
}

export function ModelListPanel({
  providers,
  selected,
  onPick,
  apiRef,
}: {
  /** 模型注册表（useSettings().providers——分组与可见性过滤在此处做）。 */
  providers: ProviderMeta[];
  /** 当前选中的模型 id（注册表主键）——首次 ↓ 从它起跳。 */
  selected?: string;
  /** 选模型（注册表主键）。 */
  onPick: (id: string) => void;
  /** 键盘转发句柄（宿主 root onKeyDown → apiRef.current.handleKey）。 */
  apiRef?: RefObject<ModelListApi | null>;
}) {
  // 搜索关键词与键盘高亮下标（-1 = 无高亮，首次 ↓ 落到当前选中项）
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(-1);
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

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

  // 高亮项滚进视野（键盘移动时列表不能把光标顶到看不见的地方）
  useEffect(() => {
    if (active < 0) return;
    const el = listRef.current?.querySelector<HTMLElement>(".mp-item.kbd");
    el?.scrollIntoView({ block: "nearest" });
  }, [active]);

  // 键盘：↑/↓ 移动高亮、Enter 选中。监听挂宿主根（apiRef 转发进来），
  // 搜索框里打字时方向键照样生效；Esc 交给 usePopover 兜底。
  const handleKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") return;
    if (flat.length === 0) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const start = active >= 0 ? active : flat.indexOf(selected ?? "");
      const from = start >= 0 ? start : (e.key === "ArrowDown" ? -1 : flat.length);
      const next = e.key === "ArrowDown"
        ? (from + 1) % flat.length
        : (from - 1 + flat.length) % flat.length;
      setActive(next);
    } else if (e.key === "Enter" && active >= 0 && flat[active]) {
      e.preventDefault();
      onPick(flat[active]);
    }
  };
  useImperativeHandle(apiRef, () => ({ handleKey }));

  return (
    <>
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
                onClick={() => onPick(m.id)}
              >
                <span className="mp-label">
                  <span className="mp-model-name">{m.name}</span>
                  <span className="mp-model-desc">{m.desc}</span>
                  {dupNames.has(m.modelId) && (
                    <span className="mp-model-desc">来源 {m.provider || g.name}</span>
                  )}
                </span>
                {selected === m.id && <span className="mp-check"><IconCheck /></span>}
              </button>
            ))}
          </div>
        ))}
      </div>
    </>
  );
}

/** 挂载即聚焦搜索框（键盘流：打开 → 直接打字过滤 → ↓/Enter）的副作用
 *  放在宿主：面板是条件渲染的，ModelPicker 在 open 变化时聚焦 root 内的
 *  搜索框即可覆盖两种宿主（AgentPicker 的三级面板同款姿势）。 */
export function focusModelSearch(root: HTMLElement | null) {
  root?.querySelector<HTMLInputElement>("input[type=search]")?.focus();
}
