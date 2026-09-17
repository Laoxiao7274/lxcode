// 模型分区：提供商卡（可收拢的模型列表 + 可见性开关 + 获取/添加/删除）。
import { useEffect, useRef, useState, type MouseEvent } from "react";
import { gsap } from "gsap";
import { useSettings, type ProviderMeta } from "../../shared/settings";
import { motionAllowed, staggerIn, enterEase } from "../../shared/motion";
import { collapseAway, playEnter } from "../../shared/anim";
import { kfmtTokens } from "../../shared/format";
import { IconChevronRight, IconPencil, IconTrash } from "../icons";

function ProviderBlock({ provider, onEditModel }: { provider: ProviderMeta; onEditModel: (modelId: string) => void }) {
  const { settings, set, setProviderEnabled, setModelVisible, fetchModels, addModel, removeModel, disconnectProvider } = useSettings();
  const [expanded, setExpanded] = useState(true);
  // 添加模型：内联输入行（空 = 未在添加）
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState("");
  const [draftErr, setDraftErr] = useState<string | null>(null);

  const cardRef = useRef<HTMLDivElement>(null);
  const modelsRef = useRef<HTMLDivElement>(null);
  const addRef = useRef<HTMLDivElement>(null);

  const enabledCount = provider.models.filter((m) => m.visible).length;
  const providerVisible = provider.models.length > 0 && provider.models.every((m) => m.visible);

  // 展开/收起（用户切换才动，首挂载由分区交错负责）
  const firstExpand = useRef(true);
  useEffect(() => {
    if (firstExpand.current) {
      firstExpand.current = false;
      return;
    }
    if (!expanded || !modelsRef.current) return;
    staggerIn([...modelsRef.current.children], { each: 0.04 });
  }, [expanded]);

  // 收起要有退场：先播高度收拢+淡出，动画结束再卸载（条件渲染直接卸载是瞬灭）
  const collapsingRef = useRef(false);
  const toggleExpand = () => {
    if (!expanded) {
      setExpanded(true);
      return;
    }
    if (collapsingRef.current) return;
    collapsingRef.current = true;
    collapseAway(modelsRef.current, () => {
      collapsingRef.current = false;
      setExpanded(false);
    }, { borderTopWidth: 0, duration: 0.24 });
  };

  // 新增行（获取合并 / 手动添加）：自上滑入
  const prevLen = useRef(provider.models.length);
  useEffect(() => {
    const added = provider.models.length - prevLen.current;
    prevLen.current = provider.models.length;
    if (added <= 0 || !expanded || !modelsRef.current) return;
    const rows = modelsRef.current.querySelectorAll(".mset-model-row");
    const fresh = Array.from(rows).slice(Math.max(0, rows.length - added));
    if (!motionAllowed() || fresh.length === 0) return;
    gsap.fromTo(fresh, { opacity: 0, y: -8 }, { opacity: 1, y: 0, duration: 0.3, stagger: 0.05, ease: enterEase, clearProps: "transform,opacity" });
  }, [provider.models.length, expanded]);

  // 添加输入行：出现即上浮
  useEffect(() => {
    if (!adding) return;
    playEnter(addRef.current, { opacity: 0, y: -6 }, { opacity: 1, y: 0, duration: 0.22, ease: enterEase, clearProps: "transform,opacity" });
  }, [adding]);

  const commitAdd = async () => {
    if (!draft.trim()) {
      setDraftErr("模型 ID 不能为空");
      return;
    }
    if (!(await addModel(provider.id, draft))) {
      setDraftErr("添加失败，请检查模型 ID 或上方错误提示");
      return;
    }
    setDraft("");
    setDraftErr(null);
    setAdding(false);
  };

  // 删除模型：先收行再移除
  const handleDelete = (e: MouseEvent<HTMLButtonElement>, modelId: string) => {
    collapseAway((e.currentTarget as HTMLElement).closest(".mset-model-row"), () => removeModel(provider.id, modelId), { borderBottomWidth: 0, duration: 0.24 });
  };

  // 移除提供商：整卡收起后断开
  const handleRemove = () => {
    collapseAway(cardRef.current, () => disconnectProvider(provider.id), { marginBottom: 0, duration: 0.28 });
  };

  return (
    <div className="mset-provider" ref={cardRef}>
      <div className="mset-provider-head">
        <button type="button" className="mset-provider-summary" onClick={toggleExpand} aria-expanded={expanded}>
          <span className={"mset-chevron" + (expanded ? " open" : "")}>
            <IconChevronRight />
          </span>
          <span className="mset-provider-badge" style={{ background: provider.color }}>{provider.name.slice(0, 1)}</span>
          <span className="mset-provider-text">
            <span className="mset-provider-name">{provider.name}</span>
            <span className="mset-provider-tagline">
              {provider.tagline}
              {provider.connected
                ? ` · ${provider.models.length > 0 ? `${enabledCount}/${provider.models.length} 模型` : "未获取模型"}${provider.fetchedAt && provider.models.length > 0 ? ` · ${provider.fetchedAt}` : ""}`
                : " · 未连接"}
            </span>
          </span>
        </button>
        {provider.connected && (
          <>
            <span
              className={"toggle" + (providerVisible ? " on" : "")}
              role="switch"
              aria-checked={providerVisible}
              aria-label={`显示 ${provider.name} 的模型`}
              tabIndex={0}
              onClick={() => {
                const next = !providerVisible;
                setProviderEnabled(provider.id, next);
                provider.models.forEach((m) => setModelVisible(provider.id, m.id, next));
              }}
              onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); (e.target as HTMLElement).click(); } }}
            >
              <span className="toggle-knob" />
            </span>
            <button type="button" className="mset-provider-remove" title="断开并移除凭据" onClick={handleRemove}>
              移除
            </button>
          </>
        )}
      </div>
      {expanded && provider.connected && (
        <div className="mset-models" ref={modelsRef}>
          {provider.fetching && (
            <div className="mset-fetching" role="status">
              <span className="mset-spinner" aria-hidden="true" />
              正在获取模型列表…
            </div>
          )}
          {!provider.fetching && provider.models.length === 0 && (
            <div className="mset-empty">
              <span className="mset-empty-text">尚未获取模型列表</span>
              <button type="button" className="mset-fetch-btn" onClick={() => fetchModels(provider.id)}>
                获取模型
              </button>
            </div>
          )}
          {provider.models.map((m) => (
            <div key={m.id} className="mset-model-row">
              <span className="mset-model-text">
                <span className="mset-model-name">{m.name}</span>
                <span className="mset-model-meta">
                  <span className="mset-model-id">{m.id}</span>
                  <span className="mset-model-ctx">上下文 {kfmtTokens(m.contextWindow)} · 输出 {kfmtTokens(m.maxOutput)}</span>
                  {m.tags.map((t) => (
                    <span key={t} className="mset-model-tag">{t}</span>
                  ))}
                  {m.manual && <span className="mset-model-tag manual">手动</span>}
                </span>
              </span>
              {settings.model === m.id ? (
                <button type="button" className="mset-model-default" title="当前默认模型">默认</button>
              ) : (
                <button type="button" className="mset-model-use" onClick={() => set({ model: m.id })}>
                  设为默认
                </button>
              )}
              <span
                className={"toggle" + (m.visible ? " on" : "")}
                role="switch"
                aria-checked={m.visible}
                aria-label={`在模型选择器中显示 ${m.name}`}
                tabIndex={0}
                onClick={() => setModelVisible(provider.id, m.id, !m.visible)}
                onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); (e.target as HTMLElement).click(); } }}
              >
                <span className="toggle-knob" />
              </span>
              <button type="button" className="mset-model-edit" title="模型配置" aria-label={`配置 ${m.name}`} onClick={() => onEditModel(m.id)}>
                <IconPencil strokeWidth={2} />
              </button>
              <button type="button" className="mset-model-del" title="删除模型" aria-label={`删除 ${m.name}`} onClick={(e) => handleDelete(e, m.id)}>
                <IconTrash />
              </button>
            </div>
          ))}
          {provider.models.length > 0 && (
            adding ? (
              <div className="mset-add-row" ref={addRef}>
                <input
                  autoFocus
                  value={draft}
                  onChange={(e) => { setDraft(e.target.value); setDraftErr(null); }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") { e.preventDefault(); commitAdd(); }
                    if (e.key === "Escape") { setAdding(false); setDraft(""); setDraftErr(null); }
                  }}
                  placeholder="模型 ID，如 deepseek-v3.2"
                  spellCheck={false}
                  aria-label="新模型 ID"
                />
                <span className={"mset-add-hint" + (draftErr ? " err" : "")}>{draftErr ?? "回车确认 · Esc 取消"}</span>
                <button type="button" className="mset-add-confirm" onClick={commitAdd}>添加</button>
                <button type="button" className="mset-add-cancel" onClick={() => { setAdding(false); setDraft(""); setDraftErr(null); }}>取消</button>
              </div>
            ) : (
              <div className="mset-models-foot">
                <button type="button" className="mset-add-model-btn" onClick={() => setAdding(true)}>+ 添加模型</button>
                <button type="button" className="mset-refresh-btn" onClick={() => fetchModels(provider.id)} title="从提供商重新获取模型列表">刷新列表</button>
              </div>
            )
          )}
        </div>
      )}
    </div>
  );
}

export { ProviderBlock };
