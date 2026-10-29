// 候选模型选择器——**新建自定义提供商**与**已有提供商「获取模型列表」**共用这一份。
//
// 为什么抽出来：两条路径此前各写一套勾选 UI，新建那条还默认全选（探测完直接写入
// 注册表），用户没有选择机会。端点可能报 200 个模型（OpenRouter），自动写入会把
// 模型选择器淹掉；「可预期胜过聪明」——一律用户勾选，默认全不选。
//
// 契约（两处同款，改动只改这里）：
//  1. 候选**绝不自动添加**，默认全不选；确认按钮在调用方（写入动作不同：
//     新建 = addCustomProvider，已有 = addDiscovered）；
//  2. **已添加过的模型仍然显示**——整行置灰 + 开关禁用 + 「已添加」标签，
//     可见、可核对、不可再勾；全选/计数只作用于未添加的；
//  3. 搜索按 id/名称过滤，空结果给人话提示（不是白屏）；
//  4. 候选可能上百条，清单内部滚动（.mset-candidate-scroll），按钮不被顶出弹窗。
import { useState } from "react";
import { Toggle } from "../form";

const searchIcon = (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="11" cy="11" r="7" />
    <path d="m21 21-4.3-4.3" />
  </svg>
);

export interface CandidateItem {
  id: string;
  name?: string;
  note?: string;
  tags?: string[];
  /** 已在注册表：整行置灰、开关不可翻（仍显示，让用户核对端点确实报了它）。 */
  disabled?: boolean;
}

/** 候选行（目录勾选 / 端点探测两处共用——同一份版式与标签措辞）。 */
export function CandidateRows({
  items,
  picked,
  onToggle,
}: {
  items: CandidateItem[];
  picked: Set<string>;
  onToggle: (id: string) => void;
}) {
  return (
    <>
      {items.map((m, i) => (
        <div key={`${m.id}-${i}`} className={"mset-model-row" + (m.disabled ? " added" : "")}>
          <span className="mset-model-text">
            <span className="mset-model-name">{m.name || m.id}</span>
            <span className="mset-model-meta">
              <span className="mset-model-id">{m.id}</span>
              {m.note && <span className="mset-model-ctx">{m.note}</span>}
              {(m.tags ?? []).map((t) => (
                <span key={t} className="mset-model-tag">{t}</span>
              ))}
            </span>
          </span>
          <Toggle on={picked.has(m.id)} onChange={() => onToggle(m.id)} disabled={m.disabled} ariaLabel={`选择 ${m.id}`} />
        </div>
      ))}
    </>
  );
}

/** 候选清单：搜索 + 可滚动的勾选行 + 已选计数/全选。写入动作由调用方负责。 */
export function CandidatePicker({
  items,
  picked,
  onToggle,
  onPickAll,
  searchPlaceholder = "搜索模型",
  emptyText = "端点没有报告任何模型",
  autoFocus = false,
}: {
  items: CandidateItem[];
  picked: Set<string>;
  onToggle: (id: string) => void;
  /** 全选/全不选（对整份候选操作，不受搜索过滤影响）。 */
  onPickAll: (next: Set<string>) => void;
  searchPlaceholder?: string;
  /** 清单为空时的人话说明（获取失败/端点没报模型由调用方区分）。 */
  emptyText?: string;
  autoFocus?: boolean;
}) {
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const shown = q ? items.filter((m) => `${m.id} ${m.name ?? ""}`.toLowerCase().includes(q)) : items;

  const selectable = items.filter((m) => !m.disabled);
  const addedCount = items.length - selectable.length;
  const allPicked = selectable.length > 0 && selectable.every((m) => picked.has(m.id));
  // 全选/全不选作用于**整份候选**：不因搜索框里有关键词就悄悄丢掉被过滤掉的选择。
  const toggleAll = () => onPickAll(allPicked ? new Set() : new Set(selectable.map((m) => m.id)));

  return (
    <>
      <label className="mset-search mset-candidate-search">
        {searchIcon}
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={searchPlaceholder}
          autoFocus={autoFocus}
          spellCheck={false}
          aria-label="搜索候选模型"
        />
      </label>
      <div className="mset-connect-scroll mset-candidate-scroll">
        {shown.length === 0
          ? <div className="mset-connect-empty">{items.length === 0 ? emptyText : `没有匹配「${query}」的模型`}</div>
          : <CandidateRows items={shown} picked={picked} onToggle={onToggle} />}
      </div>
      <div className="mset-probe-head">
        <span className="mset-probe-text">
          已选 <b>{picked.size}</b> / {selectable.length}
          {addedCount > 0 ? ` · ${addedCount} 个已添加` : ""}
        </span>
        {selectable.length > 0 && (
          <button type="button" className="mset-probe-all" onClick={toggleAll}>
            {allPicked ? "全不选" : "全选"}
          </button>
        )}
      </div>
    </>
  );
}
