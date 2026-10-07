// 连接提供商（OpenCode Desktop 式二级流程）：
// picker（**真实模型目录**：搜索 + 厂商清单 + 自定义入口）→ provider（端点信息 + API Key +
// 勾选该厂商的模型 → 写入注册表）/ custom（自定义 OpenAI 兼容端点，可探测模型）。
//
// 三条纪律：
//  1. 目录是**免 key** 的（models.dev 快照由后端拉取并缓存），所以先列厂商再要 key；
//  2. 目录里 6000+ 个模型**绝不自动写入**——一律用户勾选，否则模型选择器会被淹掉；
//  3. 目录拉不到时**退化成手工填 ID**（= 没有这个功能之前的行为），不假装有数据。
import { useEffect, useMemo, useRef, useState } from "react";
import { gsap } from "gsap";
import { useSettings } from "../../shared/settings";
import { catalogTags } from "../../shared/settings-models";
import { kfmtLimit } from "../../shared/format";
import { motionAllowed } from "../../shared/motion";
import { useEscape } from "../../shared/popover";
import type { CatalogModel, CatalogProvider, DiscoveredModel } from "../../shared/types";
import { TextInput, Toggle } from "../form";

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

type Page = { kind: "picker" } | { kind: "provider"; id: string } | { kind: "custom" };

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** 目录厂商没有品牌色，但徽标得有个稳定的底色：按 id 哈希取一个固定色相。
 *  只是显示用（同一厂商每次进来颜色一致），不代表任何品牌信息。 */
const BADGE_COLORS = ["#615ced", "#10a37f", "#d97757", "#8e8ea0", "#b48c5f", "#4a7bd9", "#c0554d", "#5a8f6b"];
function badgeColor(id: string): string {
  let h = 0;
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0;
  return BADGE_COLORS[h % BADGE_COLORS.length];
}

export function ConnectProviderDialog({ onClose }: { onClose: () => void }) {
  const { providers, catalogProviders, addCustomProvider } = useSettings();
  const [page, setPage] = useState<Page>({ kind: "picker" });
  const [query, setQuery] = useState("");
  const [catalog, setCatalog] = useState<CatalogProvider[] | null>(null);
  const [catalogErr, setCatalogErr] = useState<string | null>(null);
  const [stale, setStale] = useState(false);
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

  // 目录：进 picker 就拉一次（TTL 内后端走缓存，很快）。失败如实显示并留自定义入口。
  useEffect(() => {
    if (page.kind !== "picker" || catalog) return;
    let alive = true;
    catalogProviders()
      .then((r) => {
        if (!alive) return;
        setCatalog(r.providers);
        setStale(Boolean(r.stale));
      })
      .catch((e) => { if (alive) setCatalogErr(errText(e)); });
    return () => { alive = false; };
  }, [page.kind, catalog, catalogProviders]);

  // 已连接的端点从目录里排除（未连接的仍可连）——注册表按完整端点分组，所以比 base_url。
  const connectedUrls = useMemo(() => new Set(providers.filter((p) => p.connected).map((p) => p.id)), [providers]);
  const shown = useMemo(() => {
    const list = (catalog ?? []).filter((p) => !connectedUrls.has(p.api ?? ""));
    const q = query.trim().toLowerCase();
    if (!q) return list;
    return list.filter((p) => `${p.id} ${p.name} ${p.api ?? ""}`.toLowerCase().includes(q));
  }, [catalog, connectedUrls, query]);

  const current = page.kind === "provider" ? (catalog ?? []).find((p) => p.id === page.id) : undefined;
  const title = page.kind === "picker" ? "连接提供商" : page.kind === "custom" ? "自定义提供商" : current?.name ?? page.id;

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
          <span className="mset-connect-title">{title}</span>
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
              {!catalog && !catalogErr && <div className="mset-connect-empty">正在加载模型目录…</div>}
              {catalogErr && (
                <div className="mset-connect-empty" role="alert">
                  模型目录拉取失败：{catalogErr}
                  <br />
                  仍可用下面的「自定义提供商」手工填写端点与模型 ID。
                </div>
              )}
              {catalog && (
                <ConnectGroup
                  title={stale ? `提供商（目录已过期，显示上次缓存 · ${shown.length}）` : `提供商（${shown.length}）`}
                  items={shown.map((p) => ({
                    id: p.id,
                    name: p.name,
                    tagline: p.api ?? "",
                    color: badgeColor(p.id),
                    tag: p.model_count > 0 ? `${p.model_count} 个模型` : undefined,
                  }))}
                  onSelect={(id) => setPage({ kind: "provider", id })}
                />
              )}
              <ConnectGroup
                title="其他"
                items={[{ id: "_custom", name: "自定义提供商", tagline: "OpenAI 兼容接口 · 填 BaseURL 与模型", color: "#8e6fbe", tag: "自定义" }]}
                onSelect={() => setPage({ kind: "custom" })}
              />
              {catalog && shown.length === 0 && (
                <div className="mset-connect-empty">没有匹配「{query}」的提供商</div>
              )}
            </div>
          </div>
        )}

        {page.kind === "provider" && current && <ProviderForm provider={current} onClose={onClose} />}

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

/** 候选模型勾选行（目录与探测两处共用——同一份版式与标签措辞）。 */
function CandidateRows({
  items,
  picked,
  onToggle,
}: {
  items: { id: string; name?: string; note?: string; tags?: string[] }[];
  picked: Set<string>;
  onToggle: (id: string) => void;
}) {
  return (
    <>
      {items.map((m) => (
        <div key={m.id} className="mset-model-row">
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
          <Toggle on={picked.has(m.id)} onChange={() => onToggle(m.id)} ariaLabel={`选择 ${m.id}`} />
        </div>
      ))}
    </>
  );
}

function ProviderForm({ provider, onClose }: { provider: CatalogProvider; onClose: () => void }) {
  const { catalogModels, addCatalogModels } = useSettings();
  const [key, setKey] = useState("");
  const [models, setModels] = useState<CatalogModel[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [stale, setStale] = useState(false);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => inputRef.current?.focus(), []);

  useEffect(() => {
    let alive = true;
    catalogModels(provider.id)
      .then((r) => {
        if (!alive) return;
        setModels(r.models);
        setStale(Boolean(r.stale));
      })
      .catch((e) => { if (alive) setErr(errText(e)); });
    return () => { alive = false; };
  }, [provider.id, catalogModels]);

  const toggle = (id: string) =>
    setPicked((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const submit = async () => {
    if (!models) return;
    const chosen = models.filter((m) => picked.has(m.id));
    if (chosen.length === 0) {
      setErr("请至少勾选一个模型");
      return;
    }
    setBusy(true);
    const ok = await addCatalogModels(provider, chosen, key.trim());
    setBusy(false);
    if (ok) onClose();
    else setErr("添加失败，请查看设置面板顶部的错误提示");
  };

  const envHint = provider.env && provider.env.length > 0 ? `后端也会读环境变量 ${provider.env[0]}` : null;

  return (
    <div className="mset-connect-body mset-form">
      <p className="mset-form-desc">
        端点 <span className="mset-probe-endpoint-inline">{provider.api || "（目录未提供地址，请改用自定义提供商）"}</span>
        {envHint ? ` · ${envHint}` : ""}
      </p>
      <label className="mset-field">
        <span>API Key（可选）</span>
        <TextInput
          inputRef={inputRef}
          type="password"
          value={key}
          onChange={(v) => { setKey(v); setErr(null); }}
          placeholder="sk-…（自建端点可留空）"
          spellCheck={false}
        />
      </label>
      <div className="mset-field">
        <span>模型（{models ? `${models.length} 个可选` : "加载中…"}）</span>
        {!models && !err && <div className="mset-connect-empty">正在读取模型目录…</div>}
        {models && models.length === 0 && <div className="mset-connect-empty">目录里这个厂商没有可用模型</div>}
        {models && models.length > 0 && (
          <div className="mset-connect-scroll mset-candidate-scroll">
            <CandidateRows
              items={models.map((m) => ({
                id: m.id,
                name: m.name,
                note: `上下文 ${kfmtLimit(m.context_window ?? 0)} · 输出 ${kfmtLimit(m.max_output_tokens ?? 0)}`,
                tags: catalogTags(m),
              }))}
              picked={picked}
              onToggle={toggle}
            />
          </div>
        )}
        {models && models.length > 0 && (
          <div className="mset-probe-head">
            <span className="mset-probe-text">已选 {picked.size} / {models.length}</span>
            <button
              type="button"
              className="mset-probe-all"
              onClick={() => setPicked((prev) => (prev.size === models.length ? new Set() : new Set(models.map((m) => m.id))))}
            >
              {picked.size === models.length ? "全不选" : "全选"}
            </button>
          </div>
        )}
        {stale && <div className="mset-connect-empty">目录已过期（网络不可用），显示的是上次缓存。</div>}
      </div>
      {err && <div className="mset-field-err" role="alert">{err}</div>}
      <button type="button" className="mset-form-submit" disabled={busy || !models || picked.size === 0} onClick={submit}>
        {busy ? "添加中…" : `添加选中（${picked.size}）`}
      </button>
    </div>
  );
}

function CustomForm({ onSubmit }: { onSubmit: (input: { name: string; baseUrl: string; models: string[]; apiKey?: string }) => void }) {
  const { discover } = useSettings();
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [key, setKey] = useState("");
  const [models, setModels] = useState<string[]>([""]);
  const [err, setErr] = useState<{ name?: string; baseUrl?: string }>({});
  // 探测：把端点**实际**提供的模型列出来让用户勾（比手抄 ID 可靠）
  const [found, setFound] = useState<DiscoveredModel[] | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [probing, setProbing] = useState(false);
  const [probeErr, setProbeErr] = useState<string | null>(null);
  const [endpoint, setEndpoint] = useState<string | null>(null);

  const runProbe = async () => {
    if (!/^https?:\/\//.test(baseUrl.trim())) {
      setProbeErr("需先填以 http(s):// 开头的 Base URL");
      return;
    }
    setProbing(true);
    setProbeErr(null);
    try {
      const res = await discover({ baseUrl: baseUrl.trim(), apiKey: key, format: "openai" });
      setFound(res.models);
      setEndpoint(res.endpoint);
      setPicked(new Set(res.models.map((m) => m.id)));
    } catch (e) {
      setFound(null);
      setProbeErr(errText(e));
    } finally {
      setProbing(false);
    }
  };

  const submit = () => {
    const next: { name?: string; baseUrl?: string } = {};
    if (!name.trim()) next.name = "名称不能为空";
    if (!/^https?:\/\//.test(baseUrl.trim())) next.baseUrl = "需以 http(s):// 开头";
    const ids = found ? [...picked] : models.map((m) => m.trim()).filter(Boolean);
    if (ids.length === 0) next.baseUrl = next.baseUrl ?? "至少填一个模型 ID（或用「探测模型」自动列出）";
    setErr(next);
    if (next.name || next.baseUrl) return;
    onSubmit({ name: name.trim(), baseUrl: baseUrl.trim(), models: ids, apiKey: key });
  };

  return (
    <form className="mset-connect-body mset-form" onSubmit={(e) => { e.preventDefault(); submit(); }}>
      <p className="mset-form-desc">接入任意 OpenAI 兼容接口（vLLM / 网关 / 中转）。</p>
      <label className="mset-field">
        <span>名称</span>
        <TextInput value={name} onChange={setName} placeholder="我的网关" spellCheck={false} />
      </label>
      {err.name && <div className="mset-field-err" role="alert">{err.name}</div>}
      <label className="mset-field">
        <span>Base URL</span>
        <TextInput value={baseUrl} onChange={setBaseUrl} placeholder="https://api.example.com/v1" spellCheck={false} />
      </label>
      <label className="mset-field">
        <span>API Key（可选）</span>
        <TextInput type="password" value={key} onChange={setKey} placeholder="sk-…" spellCheck={false} />
      </label>
      <div className="mset-field">
        <span>模型</span>
        <div className="mset-probe-head">
          <span className="mset-probe-text">
            {found ? `端点报告 ${found.length} 个模型` : "手工填 ID，或让端点自己报（推荐）"}
          </span>
          <button type="button" className="mset-probe-all" onClick={runProbe} disabled={probing}>
            {probing ? "探测中…" : "探测模型"}
          </button>
        </div>
        {endpoint && <div className="mset-probe-endpoint">{endpoint}</div>}
        {probeErr && <div className="mset-field-err" role="alert">{probeErr}</div>}
        {found && found.length > 0 && (
          <div className="mset-connect-scroll mset-candidate-scroll">
            <CandidateRows
              items={found.map((m) => ({ id: m.id, name: m.name }))}
              picked={picked}
              onToggle={(id) => setPicked((prev) => {
                const next = new Set(prev);
                if (next.has(id)) next.delete(id);
                else next.add(id);
                return next;
              })}
            />
          </div>
        )}
        {found && found.length === 0 && <div className="mset-connect-empty">端点没有报告任何模型</div>}
        {!found && models.map((m, i) => (
          <TextInput
            key={i}
            className="mset-model-input"
            value={m}
            onChange={(v) => setModels((ms) => ms.map((x, j) => (j === i ? v : x)))}
            placeholder={i === 0 ? "例如 deepseek-v3.2" : ""}
            spellCheck={false}
          />
        ))}
        {!found && (
          <button type="button" className="mset-add-model" onClick={() => setModels((ms) => [...ms, ""])}>
            + 添加模型
          </button>
        )}
      </div>
      {err.baseUrl && <div className="mset-field-err" role="alert">{err.baseUrl}</div>}
      <button type="submit" className="mset-form-submit">连接</button>
    </form>
  );
}
