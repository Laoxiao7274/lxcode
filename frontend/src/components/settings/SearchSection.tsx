// 网页搜索分区（设置）：渠道清单 + apikey/实例地址配置 + 主渠道 + 单渠道测试。
//
// 渠道清单来自后端（内置适配器元数据 + 用户配置合并后的视图），前端不持有
// 列表——后端加渠道这里自动出现。30+ 个渠道平铺不可用，所以按后端给的
// category 分组，并在渠道多时提供筛选框（找具体某个渠道是常见诉求）。
//
// 语义：搜索工具默认走主渠道，失败自动降级其它就绪渠道；零配置渠道
// （opt_in）必须手动启用——「技术上能跑」不等于「想用它」。
import { useEffect, useMemo, useRef, useState } from "react";
import { groupByCategory, readyCount, useSearchAdmin } from "../../shared/search-admin";
import { acceptsKey, hasStoredConfig } from "../../shared/search-channels";
import type { SearchChannel, SearchTestResult } from "../../shared/types";
import { collapseAway, useEnterRef } from "../../shared/anim";
import { staggerIn } from "../../shared/motion";
import { useConfirmClick } from "../../shared/confirm-click";
import { maskToken } from "../../shared/connections";
import { Button, Select, TextInput } from "../form";
import { IconChevronRight } from "../icons";
import { Section } from "./rows";

/** 缺必填设置项的键名（空 = 必填项齐备）。 */
function missingRequiredOptions(c: SearchChannel): string[] {
  return (c.option_specs ?? [])
    .filter((spec) => spec.required && (c.options?.[spec.key] ?? "").trim() === "")
    .map((spec) => spec.label);
}

/** 渠道状态标签（就绪/待补全/未配置/需手动启用/已停用）。 */
function statusPill(c: SearchChannel): { text: string; cls: string } {
  if (!c.enabled) return { text: "已停用", cls: "src" };
  if (c.configured) return { text: "已就绪", cls: "risk-low" };
  // 缺必填设置项要单独说清楚：这类渠道看着「key 都填了」却不生效，
  // 笼统的「未配置」会让人以为是自己 key 填错了。
  const missing = missingRequiredOptions(c);
  if (missing.length > 0) return { text: `待补全 ${missing.join("、")}`, cls: "risk-high" };
  if (c.opt_in) return { text: "需手动启用", cls: "risk-high" };
  return { text: "未配置", cls: "src" };
}

/** 单个渠道卡：状态 + 凭据展示/内联配置 + 主渠道/测试/停用/删除操作。 */
function ChannelCard({ c, busy, onTested }: {
  c: SearchChannel;
  busy: boolean;
  onTested: (r: SearchTestResult | null) => void;
}) {
  const { save, remove, setPrimary, test, live } = useSearchAdmin();
  const [editing, setEditing] = useState(false);
  const [keyDraft, setKeyDraft] = useState("");
  const [urlDraft, setUrlDraft] = useState("");
  // 设置项草稿（键 = OptionSpec.key）。后端 options 是整体覆盖语义，
  // 所以这里必须带上**全部**声明过的键，而不是只带改过的那个。
  const [optionDrafts, setOptionDrafts] = useState<Record<string, string>>({});
  const [testing, setTesting] = useState(false);
  const del = useConfirmClick(() => void remove(c.id));
  const editEnter = useEnterRef<HTMLDivElement>();
  const editElRef = useRef<HTMLDivElement | null>(null);
  const closingRef = useRef(false);
  const pill = statusPill(c);
  const specs = c.option_specs ?? [];

  // 收拢退场（高度归零 + 淡出）后再卸载——取消/保存都不是瞬灭。
  const closeEdit = () => {
    if (closingRef.current) return;
    closingRef.current = true;
    collapseAway(editElRef.current, () => {
      closingRef.current = false;
      setEditing(false);
    });
  };
  const startEdit = () => {
    setKeyDraft(c.api_key ?? "");
    setUrlDraft(c.base_url ?? "");
    setOptionDrafts({ ...(c.options ?? {}) });
    setEditing(true);
  };
  const saveEdit = async () => {
    // 只提交这个渠道自己声明需要的字段：不接受 key 的渠道提交空 key 会
    // 覆盖掉用户可能通过环境变量提供的值（后端 upsert 语义）。
    const ok = await save(c.id, {
      apiKey: acceptsKey(c) ? keyDraft.trim() : undefined,
      baseUrl: c.needs_url ? urlDraft.trim() : undefined,
      // 只提交声明过的键（避免把界面上不存在的键写进磁盘）；
      // 全空时给 {}——后端会当成「没有设置项」。
      options: specs.length > 0
        ? Object.fromEntries(specs.map((s) => [s.key, (optionDrafts[s.key] ?? "").trim()]))
        : undefined,
      enabled: true,
    });
    if (ok) closeEdit();
  };
  const runTest = async () => {
    setTesting(true);
    onTested(await test(c.id));
    setTesting(false);
  };

  return (
    <div className="sp-card" data-configured={c.configured ? "true" : undefined} data-primary={c.primary ? "true" : undefined}>
      <div className="sp-head">
        <span className="sp-name">{c.label}</span>
        <span className="sp-pills">
          {c.primary && c.configured && <span className="ag-pill risk-low">主渠道</span>}
          <span className={"ag-pill " + pill.cls}>{pill.text}</span>
        </span>
        <div className="sp-actions">
          {c.configured && !c.primary && (
            <Button variant="ghost" className="sp-btn" data-sp="set-primary" onClick={() => void setPrimary(c.id)}>设为主渠道</Button>
          )}
          {c.configured && (
            <Button variant="ghost" className="sp-btn" data-sp="test" disabled={testing || busy} onClick={() => void runTest()}>
              {testing ? "测试中…" : "测试"}
            </Button>
          )}
          <button type="button" className="ag-mini-btn" data-sp="edit" onClick={startEdit}>
            {c.configured ? "修改" : "配置"}
          </button>
          {c.configured && (
            <button
              type="button"
              className="ag-mini-btn"
              data-sp="toggle"
              onClick={() => void save(c.id, { enabled: !c.enabled })}
            >
              {c.enabled ? "停用" : "启用"}
            </button>
          )}
          {hasStoredConfig(c) && (
            <button
              type="button"
              className={"ag-mini-btn danger" + (del.confirming ? " confirm" : "")}
              data-sp="del"
              onClick={del.onClick}
              onBlur={del.onBlur}
            >
              {del.confirming ? "确认清除" : "清除"}
            </button>
          )}
        </div>
      </div>
      <div className="sp-desc">{c.desc}</div>
      {c.configured && (
        <div className="sp-cred mono">
          {acceptsKey(c) && c.api_key ? `key ${maskToken(c.api_key)}` : null}
          {c.needs_url && c.base_url ? c.base_url : null}
          {specs.map((s) => {
            const v = c.options?.[s.key];
            return v ? `${s.label} ${v}` : null;
          })}
          {!acceptsKey(c) && !c.needs_url && specs.length === 0 ? "无需凭据" : null}
        </div>
      )}
      {editing && (
        <div
          className="sp-edit"
          ref={(el) => {
            editElRef.current = el;
            editEnter(el);
          }}
        >
          {acceptsKey(c) && (
            <div className="cg-field">
              <span className="cg-field-label">API Key{c.needs_key ? "" : "（可选）"}</span>
              <TextInput
                className="cg-id-input"
                value={keyDraft}
                onChange={setKeyDraft}
                placeholder={c.needs_key ? "粘贴渠道的 apikey" : "留空即可用（走免配置通道）"}
                aria-label={`${c.label} API Key`}
              />
              {!c.needs_key && (
                <div className="cg-field-hint">
                  不填也能用（走免配置通道，有免费额度）；填了则走直连 API，更稳更快。
                </div>
              )}
              {c.doc_url && (
                <div className="cg-field-hint">
                  还没有 key？到 <a href={c.doc_url} target="_blank" rel="noreferrer">{c.doc_url.replace(/^https?:\/\//, "")}</a> 获取。
                </div>
              )}
              {c.env_var && (
                <div className="cg-field-hint">也可以不填，改用环境变量 <code>{c.env_var}</code>。</div>
              )}
            </div>
          )}
          {c.needs_url && (
            <div className="cg-field">
              <span className="cg-field-label">实例地址</span>
              <TextInput
                className="cg-id-input"
                value={urlDraft}
                onChange={setUrlDraft}
                placeholder="https://search.example.com"
                aria-label={`${c.label} 实例地址`}
              />
            </div>
          )}
          {specs.map((s) => {
            const value = optionDrafts[s.key] ?? "";
            return (
              <div className="cg-field" key={s.key}>
                <span className="cg-field-label">
                  {s.label}
                  {s.required && <span className="sp-req" title="必填">*</span>}
                </span>
                {s.choices && s.choices.length > 0 ? (
                  // 有限枚举渲染成下拉：手打错了要么被后端硬校验拦下（白填一次），
                  // 要么静默改变计费（如 Mistral 的 premium 档）。
                  <Select
                    value={value}
                    onChange={(v) => setOptionDrafts((d) => ({ ...d, [s.key]: v }))}
                    groups={[{ options: s.choices!.map((ch) => ({ value: ch, label: ch })) }]}
                    placeholder={s.placeholder ?? "请选择"}
                    ariaLabel={`${c.label} ${s.label}`}
                  />
                ) : (
                  <TextInput
                    className="cg-id-input"
                    value={value}
                    onChange={(v) => setOptionDrafts((d) => ({ ...d, [s.key]: v }))}
                    placeholder={s.placeholder}
                    aria-label={`${c.label} ${s.label}`}
                  />
                )}
                {s.hint && <div className="cg-field-hint">{s.hint}</div>}
                {s.env_var && (
                  <div className="cg-field-hint">也可以留空，改用环境变量 <code>{s.env_var}</code>。</div>
                )}
              </div>
            );
          })}
          {c.opt_in && (
            <div className="cg-field-hint">
              该渠道无需凭据，但需要手动启用后才会参与搜索（避免默认抓取外部页面）。
            </div>
          )}
          {c.default_ready && (
            <div className="cg-field-hint">
              无需配置，开箱即可用（作为默认渠道）；不想要可以「停用」。
            </div>
          )}
          <div className="sp-edit-actions">
            <Button variant="ghost" onClick={closeEdit}>取消</Button>
            <Button variant="primary" data-sp="save" disabled={!live} onClick={() => void saveEdit()}>保存</Button>
          </div>
          {!live && <div className="cg-field-hint">演示模式：启动后端后这里才会真正写入配置。</div>}
        </div>
      )}
    </div>
  );
}

/** 分组：可收拢的渠道清单。展开/收起与模型分区的 ProviderBlock 同款——
 *  收起先播高度收拢 + 淡出再卸载（直接卸载是瞬灭），展开时子卡自上交错浮现。
 *  首挂载不播入场（分区自己有入场，两套一起放会打架）。
 *  折叠状态由父级持有（按分组 id），这样筛选时切走再切回来不会丢折叠状态。 */
function ChannelGroup({
  group, collapsed, onToggle, busy, onTested,
}: {
  group: { id: string; label: string; items: SearchChannel[] };
  collapsed: boolean;
  onToggle: () => void;
  busy: boolean;
  onTested: (c: SearchChannel, r: SearchTestResult | null) => void;
}) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const collapsingRef = useRef(false);
  const firstRender = useRef(true);

  useEffect(() => {
    if (firstRender.current) {
      firstRender.current = false;
      return;
    }
    if (collapsed || !bodyRef.current) return;
    staggerIn([...bodyRef.current.children], { each: 0.04 });
  }, [collapsed]);

  const toggle = () => {
    if (collapsed) {
      onToggle(); // 展开：先切状态，入场交给上面的 effect
      return;
    }
    if (collapsingRef.current) return;
    collapsingRef.current = true;
    collapseAway(bodyRef.current, () => {
      collapsingRef.current = false;
      onToggle();
    });
  };

  return (
    <div className="sp-group">
      <button
        type="button"
        className="sp-group-head"
        data-sp="group"
        aria-expanded={!collapsed}
        onClick={toggle}
      >
        <span className={"sp-group-caret" + (collapsed ? "" : " open")}>
          <IconChevronRight />
        </span>
        <span className="sp-group-label">{group.label}</span>
        <span className="sp-group-count">{group.items.filter((c) => c.configured).length}/{group.items.length}</span>
      </button>
      {!collapsed && (
        <div className="sp-group-body" ref={bodyRef}>
          {group.items.map((c) => (
            <ChannelCard
              key={c.id}
              c={c}
              busy={busy}
              onTested={(r) => onTested(c, r)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

/** 测试结果块：把渠道真实返回的内容摊给用户看（凭据能不能用一眼可知）。 */
function TestResultBox({ name, result, onClose }: { name: string; result: SearchTestResult; onClose: () => void }) {
  const boxEnter = useEnterRef<HTMLDivElement>();
  return (
    <div className="sp-card sp-test" ref={boxEnter}>
      <div className="sp-head">
        <span className="sp-name">测试结果 · {name}</span>
        <span className="sp-pills">
          <span className="ag-pill risk-low">{result.elapsed_ms}ms</span>
          <span className="ag-pill src">{result.results.length} 条</span>
        </span>
        <div className="sp-actions">
          <button type="button" className="ag-mini-btn" onClick={onClose}>关闭</button>
        </div>
      </div>
      {result.answer && <div className="sp-test-answer">{result.answer}</div>}
      {result.results.slice(0, 3).map((r, i) => (
        <div className="sp-test-hit" key={i}>
          <a href={r.url} target="_blank" rel="noreferrer">{r.title}</a>
          {r.snippet && <div className="sp-test-snippet">{r.snippet}</div>}
        </div>
      ))}
      {result.results.length === 0 && !result.answer && (
        <div className="sp-desc">渠道连通但没返回结果——换个查询词再试。</div>
      )}
    </div>
  );
}

export function SearchSection() {
  const { snapshot, live, error, clearError } = useSearchAdmin();
  const [filter, setFilter] = useState("");
  const [result, setResult] = useState<{ id: string; name: string; r: SearchTestResult } | null>(null);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  const channels = snapshot.channels;
  const groups = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const matched = q
      ? channels.filter((c) => `${c.label} ${c.id} ${c.desc}`.toLowerCase().includes(q))
      : channels;
    return groupByCategory(matched);
  }, [channels, filter]);
  const ready = readyCount(channels);
  const primary = channels.find((c) => c.primary && c.configured);
  const testing = result !== null;

  return (
    <Section title="网页搜索" desc="Agent 网页检索的渠道——主渠道失败自动降级其它就绪渠道；零配置渠道需手动启用。">
      <div className="sp-summary">
        <span>{ready} / {channels.length} 个就绪</span>
        <span className="sp-summary-primary">
          主渠道 {primary ? primary.label : ready > 0 ? "自动（未指定）" : "无"}
        </span>
      </div>
      {!live && <div className="cg-field-hint">演示模式：显示的是示例渠道，启动后端后这里会列出全部内置渠道。</div>}
      {ready === 0 && live && channels.length > 0 && (
        <div className="cg-field-hint">
          还没有就绪的渠道——Agent 的 web_search 工具暂时不可用，配置任意一个即可（自建 SearXNG 或任一家 API key）。
        </div>
      )}
      {error && (
        <div className="sp-error" role="alert">
          {error}
          <button type="button" className="ag-mini-btn" onClick={clearError}>知道了</button>
        </div>
      )}
      {testing && result && (
        <TestResultBox name={result.name} result={result.r} onClose={() => setResult(null)} />
      )}
      {channels.length > 8 && (
        <div className="sp-filter">
          <TextInput value={filter} onChange={setFilter} placeholder={`筛选渠道（共 ${channels.length} 个）`} aria-label="筛选渠道" />
        </div>
      )}
      {groups.length === 0 && <div className="sp-desc">没有匹配的渠道。</div>}
      {groups.map((g) => (
        <ChannelGroup
          key={g.id}
          group={g}
          collapsed={Boolean(collapsed[g.id])}
          onToggle={() => setCollapsed((s) => ({ ...s, [g.id]: !s[g.id] }))}
          busy={testing}
          onTested={(c, r) => setResult(r ? { id: c.id, name: c.label, r } : null)}
        />
      ))}
    </Section>
  );
}
