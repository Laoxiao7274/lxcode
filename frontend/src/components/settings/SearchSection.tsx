// 网页搜索分区（设置）：多渠道 + apikey 配置 + 主渠道。
// 渠道卡 = 状态（已配置/未配置）+ key 遮罩（内联展开配置/替换——展开
// 有 gsap 入场）+ 主渠道切换；预设渠道不可删；自定义渠道（自建端点）
// 可加可删。语义：搜索工具默认走主渠道，失败自动降级其它已配置渠道。
import { useState } from "react";
import { isConfigured, maskToken, useSearchProviders, type SearchProvider } from "../../shared/search-providers";
import { useEnterRef } from "../../shared/anim";
import { Button, TextInput } from "../form";
import { IconTrash } from "../icons";
import { Section } from "./rows";

/** 单个渠道卡：状态 + key 展示/内联配置 + 主渠道操作。 */
function ProviderCard({ p, primary, onSetPrimary }: { p: SearchProvider; primary: boolean; onSetPrimary: () => void }) {
  const { updateProvider, removeCustom } = useSearchProviders();
  const [editing, setEditing] = useState(false);
  const [keyDraft, setKeyDraft] = useState("");
  const [urlDraft, setUrlDraft] = useState("");
  const [confirming, setConfirming] = useState(false);
  const editEnter = useEnterRef<HTMLDivElement>();
  const configured = isConfigured(p);

  const startEdit = () => {
    setKeyDraft(p.apiKey);
    setUrlDraft(p.baseUrl ?? "");
    setEditing(true);
  };
  const save = () => {
    updateProvider(p.id, { apiKey: keyDraft.trim(), baseUrl: urlDraft.trim() });
    setEditing(false);
  };

  return (
    <div className="sp-card" data-configured={configured ? "true" : undefined} data-primary={primary ? "true" : undefined}>
      <div className="sp-head">
        <span className="sp-name">{p.name}</span>
        <span className="sp-pills">
          {primary && <span className="ag-pill risk-low">主渠道</span>}
          <span className={"ag-pill " + (configured ? "risk-low" : "src")}>{configured ? "已配置" : "未配置"}</span>
        </span>
        <div className="sp-actions">
          {configured && !primary && (
            <Button variant="ghost" className="sp-btn" data-sp="set-primary" onClick={onSetPrimary}>设为主渠道</Button>
          )}
          {!configured && (
            <Button variant="ghost" className="sp-btn" data-sp="configure" onClick={startEdit}>配置</Button>
          )}
          {configured && (
            <button type="button" className="ag-mini-btn" data-sp="edit" onClick={startEdit}>
              替换 key
            </button>
          )}
          {!p.preset && (
            <button
              type="button"
              className={"ag-mini-btn danger" + (confirming ? " confirm" : "")}
              data-sp="del"
              onClick={() => {
                if (confirming) removeCustom(p.id);
                else setConfirming(true);
              }}
              onBlur={() => setConfirming(false)}
            >
              <IconTrash /> {confirming ? "确认" : "删除"}
            </button>
          )}
        </div>
      </div>
      <div className="sp-desc">{p.desc}</div>
      {configured && (
        <div className="sp-cred mono">
          {p.needsKey ? `key ${maskToken(p.apiKey)}` : p.baseUrl}
        </div>
      )}
      {editing && (
        <div className="sp-edit" ref={editEnter}>
          {p.needsKey ? (
            <div className="cg-field">
              <span className="cg-field-label">API Key</span>
              <TextInput
                className="cg-id-input"
                value={keyDraft}
                onChange={setKeyDraft}
                placeholder="粘贴渠道的 apikey"
                aria-label={`${p.name} API Key`}
              />
              {p.docsUrl && (
                <div className="cg-field-hint">还没有 key？到 <a href={p.docsUrl} target="_blank" rel="noreferrer">{p.docsUrl.replace(/^https?:\/\//, "")}</a> 注册获取。</div>
              )}
            </div>
          ) : (
            <div className="cg-field">
              <span className="cg-field-label">实例地址</span>
              <TextInput
                className="cg-id-input"
                value={urlDraft}
                onChange={setUrlDraft}
                placeholder="https://search.example.com"
                aria-label={`${p.name} 实例地址`}
              />
            </div>
          )}
          <div className="sp-edit-actions">
            <Button variant="ghost" onClick={() => setEditing(false)}>取消</Button>
            <Button variant="primary" data-sp="save" onClick={save}>保存</Button>
          </div>
        </div>
      )}
    </div>
  );
}

/** 添加自定义渠道表单（自建端点）——挂载入场（useEnterRef）。 */
function AddCustomForm({ onDone }: { onDone: () => void }) {
  const { addCustom } = useSearchProviders();
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const formEnter = useEnterRef<HTMLDivElement>();
  const savable = name.trim() !== "" && baseUrl.trim() !== "";
  return (
    <div className="sp-card sp-add-form" ref={formEnter}>
      <div className="cg-field">
        <span className="cg-field-label">名称</span>
        <TextInput value={name} onChange={setName} placeholder="如：公司自建搜索" aria-label="渠道名称" />
      </div>
      <div className="cg-field">
        <span className="cg-field-label">端点地址</span>
        <TextInput className="cg-id-input" value={baseUrl} onChange={setBaseUrl} placeholder="https://search.example.com/api" aria-label="端点地址" />
      </div>
      <div className="cg-field">
        <span className="cg-field-label">API Key（可选）</span>
        <TextInput className="cg-id-input" value={apiKey} onChange={setApiKey} placeholder="端点需要鉴权时填写" aria-label="API Key" />
      </div>
      <div className="sp-edit-actions">
        <Button variant="ghost" onClick={onDone}>取消</Button>
        <Button variant="primary" data-sp="save-custom" disabled={!savable} onClick={() => { addCustom({ name: name.trim(), baseUrl: baseUrl.trim(), apiKey: apiKey.trim() || undefined }); onDone(); }}>
          保存
        </Button>
      </div>
    </div>
  );
}

export function SearchSection() {
  const { providers, primaryId, setPrimary } = useSearchProviders();
  const [adding, setAdding] = useState(false);
  const primary = providers.find((p) => p.id === primaryId && isConfigured(p));
  return (
    <Section title="网页搜索" desc="Agent 网页检索的渠道——多渠道各配 apikey，主渠道失败自动降级其它已配置渠道。">
      <div className="sp-summary">
        <span>{providers.filter(isConfigured).length} 个已配置</span>
        <span className="sp-summary-primary">
          主渠道 {primary ? primary.name : "无（配置任意渠道后自动选择）"}
        </span>
      </div>
      {providers.map((p) => (
        <ProviderCard key={p.id} p={p} primary={p.id === primaryId && isConfigured(p)} onSetPrimary={() => setPrimary(p.id)} />
      ))}
      {adding ? (
        <AddCustomForm onDone={() => setAdding(false)} />
      ) : (
        <button type="button" className="conn-add" data-sp="add" onClick={() => setAdding(true)}>
          + 添加自定义渠道（自建端点）
        </button>
      )}
    </Section>
  );
}
