// 提供商连接配置弹窗：改 Base URL / API Key / 格式——**一次写到该提供商下所有模型**。
//
// 为什么要有它：磁盘上 base_url/api_key/format 是逐模型存的（没有提供商实体），
// 此前全 UI 没有这三个字段的任何入口，用户改一个 key 只能「移除提供商再重加」——
// 那会把每个模型已配的窗口、输出上限、能力标签一起丢掉。
//
// key 回填现值（不是「留空 = 不变」的只写框）：用户来这儿就是要看现在填的是什么、
// 好把错的改对（自己机器上的自己的 key）。卡片上仍只显地址，不显 key。
import { useEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { useSettings, type ProviderMeta } from "../../shared/settings";
import { motionAllowed } from "../../shared/motion";
import { useEscape } from "../../shared/popover";
import { Segmented, TextInput } from "../form";

const FORMATS = [
  { value: "openai", label: "OpenAI 兼容", hint: "chat completions 协议（多数网关与自建端点）" },
  { value: "anthropic", label: "Anthropic", hint: "Messages 协议（Claude 与兼容实现）" },
];

export function ProviderEditDialog({ provider, onClose }: { provider: ProviderMeta; onClose: () => void }) {
  const { updateProvider, error } = useSettings();
  const [baseUrl, setBaseUrl] = useState(provider.baseUrl);
  const [apiKey, setApiKey] = useState(provider.apiKey);
  const [format, setFormat] = useState(provider.format);
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const formRef = useRef<HTMLFormElement>(null);
  const count = provider.models.length;

  useEscape(true, onClose);

  useEffect(() => {
    const el = formRef.current;
    if (!el || !motionAllowed()) return;
    gsap.fromTo(
      el.querySelectorAll(".mset-edit-field"),
      { opacity: 0, y: 8 },
      { opacity: 1, y: 0, duration: 0.26, stagger: 0.035, ease: "power2.out", clearProps: "transform,opacity" },
    );
  }, []);

  const save = async () => {
    setErr(null);
    setSaving(true);
    const ok = await updateProvider(provider.id, { baseUrl, apiKey, format });
    setSaving(false);
    if (ok) onClose();
    // 失败时错误由 error（面板顶部）与下面的 err 一并呈现：校验类错误在前端就地
    // 拦下，后端类错误（非法地址/重复）从 run() 回填到 error。
    else setErr("保存失败，请看下方错误");
  };

  return (
    <div className="mset-connect-mask" role="dialog" aria-label="提供商连接配置" aria-modal="true">
      <div className="mset-connect">
        <div className="mset-connect-head">
          <span className="mset-connect-back-spacer" />
          <span className="mset-connect-title">连接配置 · {provider.name}</span>
          <button type="button" className="mset-connect-close" onClick={onClose} aria-label="关闭">×</button>
        </div>
        <form
          className="mset-connect-body mset-form"
          ref={formRef}
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <div className="mset-form-desc">
            这三项是该提供商下 <b>{count}</b> 个模型共用的连接配置，改一次全部生效；
            各模型的上下文窗口与能力不受影响。
          </div>
          <div className="mset-edit-fields">
            <label className="mset-edit-field wide">
              <span>Base URL</span>
              <TextInput
                className="mono"
                autoFocus
                value={baseUrl}
                onChange={(v) => { setBaseUrl(v); setErr(null); }}
                placeholder="https://api.example.com/v1"
                spellCheck={false}
              />
            </label>
            <label className="mset-edit-field wide">
              <span>API Key</span>
              <TextInput
                className="mono"
                type="password"
                value={apiKey}
                onChange={(v) => { setApiKey(v); setErr(null); }}
                placeholder="sk-…（留空 = 该端点不需要 key）"
                spellCheck={false}
              />
            </label>
            <div className="mset-edit-field wide">
              <span>协议格式</span>
              <Segmented options={FORMATS} value={format} onChange={(v) => { setFormat(v); setErr(null); }} ariaLabel="协议格式" />
            </div>
          </div>
          <div className="mset-form-desc">
            改 Base URL 会把这家提供商移到新地址下（地址就是分组的依据）；格式填错会让请求直接失败。
          </div>
          {(error || err) && <div className="mset-edit-err" role="alert">{error || err}</div>}
          <div className="mset-medit-foot">
            <span />
            <span className="mset-medit-actions">
              <button type="button" className="mset-add-cancel" onClick={onClose}>取消</button>
              <button type="submit" className="mset-add-confirm" disabled={saving}>{saving ? "保存中…" : "保存"}</button>
            </span>
          </div>
        </form>
      </div>
    </div>
  );
}
