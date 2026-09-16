// 连接提供商（OpenCode Desktop 式二级流程）：
// picker（搜索 + 推荐/其他分组 + 自定义入口）→ key（API Key 表单）/ custom（自定义提供商表单）。
// 全 mock：Key 只做非空校验，连接即落进设置里的提供商列表。
import { useEffect, useMemo, useRef, useState } from "react";
import { gsap } from "gsap";
import { CONNECTABLE_PROVIDERS, useSettings } from "../../shared/settings";
import { motionAllowed } from "../../shared/motion";
import { useEscape } from "../../shared/popover";

const backArrow = (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M19 12H5" />
    <path d="m12 19-7-7 7-7" />
  </svg>
);
const searchIcon = (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <circle cx="11" cy="11" r="7" />
    <path d="m21 21-4.3-4.3" />
  </svg>
);

type Page = { kind: "picker" } | { kind: "key"; provider: string } | { kind: "custom" };

export function ConnectProviderDialog({ onClose }: { onClose: () => void }) {
  const { providers, connectProvider, addCustomProvider } = useSettings();
  const [page, setPage] = useState<Page>({ kind: "picker" });
  const [query, setQuery] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);
  const prevKind = useRef<Page["kind"] | null>(null);

  // 页面切换：picker→表单向右滑入，返回向左；列表行交错浮现
  useEffect(() => {
    const root = rootRef.current;
    const first = prevKind.current === null;
    const dir = page.kind === "picker" ? -8 : 8;
    prevKind.current = page.kind;
    if (!root || !motionAllowed()) return;
    const body = root.querySelector<HTMLElement>(".mset-connect-body");
    if (body) {
      gsap.fromTo(body, first ? { opacity: 0, y: 6 } : { opacity: 0, x: dir }, first
        ? { opacity: 1, y: 0, duration: 0.22, ease: "power2.out", clearProps: "transform,opacity" }
        : { opacity: 1, x: 0, duration: 0.22, ease: "power2.out", clearProps: "transform,opacity" });
    }
    const rows = root.querySelectorAll(".mset-connect-row");
    if (rows.length) {
      gsap.fromTo(rows, { opacity: 0, y: 6 }, { opacity: 1, y: 0, duration: 0.22, stagger: 0.03, delay: 0.06, ease: "power2.out", clearProps: "transform,opacity" });
    }
  }, [page.kind]);

  useEscape(true, onClose);

  // 只把「已连接」的提供商从可连接目录里排除（未连接的仍可从目录连）
  const connectedIds = new Set(providers.filter((p) => p.connected).map((p) => p.id));
  const catalog = useMemo(
    () => CONNECTABLE_PROVIDERS.filter((p) => !connectedIds.has(p.id)),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [providers],
  );
  const featured = catalog.filter((p) => ["openai", "qwen"].includes(p.id));
  const other = catalog.filter((p) => !["openai", "qwen"].includes(p.id));

  const filter = (list: typeof catalog) => {
    const q = query.trim().toLowerCase();
    if (!q) return list;
    return list.filter((p) => `${p.id} ${p.name} ${p.tagline}`.toLowerCase().includes(q));
  };
  const featuredShown = filter(featured);
  const otherShown = filter(other);

  return (
    <div className="mset-connect-mask" role="dialog" aria-label="连接提供商" aria-modal="true">
      <div className="mset-connect" ref={rootRef}>
        <div className="mset-connect-head">
          {page.kind !== "picker" ? (
            <button type="button" className="mset-connect-back" onClick={() => setPage({ kind: "picker" })} aria-label="返回">
              {backArrow}
            </button>
          ) : (
            <span className="mset-connect-back-spacer" />
          )}
          <span className="mset-connect-title">
            {page.kind === "picker" ? "连接提供商" : page.kind === "custom" ? "自定义提供商" : "连接 " + (catalog.find((p) => p.id === page.provider)?.name ?? page.provider)}
          </span>
          <button type="button" className="mset-connect-close" onClick={onClose} aria-label="关闭">×</button>
        </div>

        {page.kind === "picker" && (
          <div className="mset-connect-body">
            <label className="mset-search">
              {searchIcon}
              <input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="搜索提供商"
                autoFocus
                spellCheck={false}
              />
            </label>
            <div className="mset-connect-scroll">
              <ConnectGroup
                title="推荐"
                items={featuredShown.map((p) => ({ ...p, tag: "推荐" }))}
                onSelect={(id) => setPage({ kind: "key", provider: id })}
              />
              <ConnectGroup
                title="其他"
                items={[
                  ...otherShown.map((p) => ({ ...p, tag: undefined as string | undefined })),
                  ...("自定义 openai 兼容 custom".includes(query.trim().toLowerCase())
                    ? [{ id: "_custom", name: "自定义提供商", tagline: "OpenAI 兼容接口 · 填 BaseURL 与模型", color: "#8e6fbe", tag: "自定义" }]
                    : []),
                ]}
                onSelect={(id) => (id === "_custom" ? setPage({ kind: "custom" }) : setPage({ kind: "key", provider: id }))}
              />
              {featuredShown.length === 0 && otherShown.length === 0 && (
                <div className="mset-connect-empty">没有匹配「{query}」的提供商</div>
              )}
            </div>
          </div>
        )}

        {page.kind === "key" && <KeyForm name={page.provider === "_custom" ? "自定义" : (catalog.find((p) => p.id === page.provider)?.name ?? page.provider)} onSubmit={() => { connectProvider(page.provider); onClose(); }} />}

        {page.kind === "custom" && <CustomForm onSubmit={(input) => { addCustomProvider(input); onClose(); }} />}
      </div>
    </div>
  );
}

function ConnectGroup({ title, items, onSelect }: { title: string; items: { id: string; name: string; tagline: string; color: string; tag?: string }[]; onSelect: (id: string) => void }) {
  if (items.length === 0) return null;
  return (
    <section className="mset-connect-group">
      <div className="mset-connect-group-title">{title}</div>
      {items.map((p) => (
        <button type="button" key={p.id} className="mset-connect-row" onClick={() => onSelect(p.id)}>
          <span className="mset-provider-badge" style={{ background: p.color }}>
            {p.name.slice(0, 1)}
          </span>
          <span className="mset-connect-row-name">{p.name}</span>
          <span className="mset-connect-row-tagline">{p.tagline}</span>
          {p.tag && <span className="mset-connect-row-tag">{p.tag}</span>}
        </button>
      ))}
    </section>
  );
}

function KeyForm({ name, onSubmit }: { name: string; onSubmit: () => void }) {
  const [key, setKey] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => inputRef.current?.focus(), []);
  return (
    <form
      className="mset-connect-body mset-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (!key.trim()) {
          setErr("API Key 不能为空");
          return;
        }
        onSubmit();
      }}
    >
      <p className="mset-form-desc">粘贴 {name} 的 API Key。密钥只保存在本机，不会上传。</p>
      <label className="mset-field">
        <span>API Key</span>
        <input
          ref={inputRef}
          type="password"
          value={key}
          onChange={(e) => {
            setKey(e.target.value);
            setErr(null);
          }}
          placeholder="sk-…"
          spellCheck={false}
        />
      </label>
      {err && <div className="mset-field-err" role="alert">{err}</div>}
      <button type="submit" className="mset-form-submit">继续</button>
    </form>
  );
}

function CustomForm({ onSubmit }: { onSubmit: (input: { name: string; baseUrl: string; models: string[] }) => void }) {
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [key, setKey] = useState("");
  const [models, setModels] = useState<string[]>([""]);
  const [err, setErr] = useState<{ name?: string; baseUrl?: string }>({});
  return (
    <form
      className="mset-connect-body mset-form"
      onSubmit={(e) => {
        e.preventDefault();
        const next: { name?: string; baseUrl?: string } = {};
        if (!name.trim()) next.name = "名称不能为空";
        if (!/^https?:\/\//.test(baseUrl.trim())) next.baseUrl = "需以 http(s):// 开头";
        setErr(next);
        if (next.name || next.baseUrl) return;
        onSubmit({ name: name.trim(), baseUrl: baseUrl.trim(), models: models.filter((m) => m.trim()) });
      }}
    >
      <p className="mset-form-desc">接入任意 OpenAI 兼容接口（vLLM / 网关 / 中转）。</p>
      <label className="mset-field">
        <span>名称</span>
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="我的网关" spellCheck={false} />
      </label>
      {err.name && <div className="mset-field-err" role="alert">{err.name}</div>}
      <label className="mset-field">
        <span>Base URL</span>
        <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="https://api.example.com/v1" spellCheck={false} />
      </label>
      {err.baseUrl && <div className="mset-field-err" role="alert">{err.baseUrl}</div>}
      <label className="mset-field">
        <span>API Key（可选）</span>
        <input type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder="sk-…" spellCheck={false} />
      </label>
      <div className="mset-field">
        <span>模型 ID</span>
        {models.map((m, i) => (
          <input
            key={i}
            className="mset-model-input"
            value={m}
            onChange={(e) => setModels((ms) => ms.map((x, j) => (j === i ? e.target.value : x)))}
            placeholder={i === 0 ? "例如 deepseek-v3.2" : ""}
            spellCheck={false}
          />
        ))}
        <button type="button" className="mset-add-model" onClick={() => setModels((ms) => [...ms, ""])}>
          + 添加模型
        </button>
      </div>
      <button type="submit" className="mset-form-submit">连接</button>
    </form>
  );
}
