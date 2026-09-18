// 连接管理弹窗：本机（回落默认 + 远程访问开关）+ 远程连接列表 + 添加。
// 壳的身份 = 连的谁：本机 127.0.0.1:7789 内置；远程连接（地址 + token）
// 可增删改、一键切换；本机被连 = 开启远程访问后展示地址与 token
// （遮罩显示，复制/重新生成）。公网穿透（樱花frp）：登录 → 自动建
// 隧道 → 公网连接地址 + 流量/隧道信息（实现文档见
// docs/sakurafrp-integration.md——原型假数据，API 层后接 v4）。
import { useRef, useState } from "react";
import { humanBytes, maskToken, useConnections, type RemoteConn } from "../../shared/connections";
import { useEscape } from "../../shared/popover";
import { staggerIn } from "../../shared/motion";
import { useEnterRef } from "../../shared/anim";
import { Button, TextInput, Toggle } from "../form";
import { IconPencil, IconTrash } from "../icons";

const LOCAL_ADDR = "127.0.0.1:7789";

/** 复制到剪贴板（反馈到按钮文案——不弹 toast）。 */
function CopyBtn({ value, label }: { value: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className="conn-copy"
      data-copied={done ? "true" : undefined}
      onClick={() => {
        void navigator.clipboard?.writeText(value).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        });
      }}
      title={`复制${label}`}
    >
      {done ? "已复制" : "复制"}
    </button>
  );
}

/** 公网穿透（樱花frp）块：登录（访问密钥）→ 隧道列表（地址/状态/用量）
 *  + 账户流量。实现文档 docs/sakurafrp-integration.md。 */
function SakuraBlock() {
  const { sakura, loginSakura, logoutSakura, tunnels, createTunnel, toggleTunnel, removeTunnel } = useConnections();
  const [key, setKey] = useState("");
  const [confirmId, setConfirmId] = useState<number | null>(null);
  const panelEnter = useEnterRef<HTMLDivElement>();

  // 未登录：密钥输入（用户中心获取访问密钥）
  if (!sakura) {
    return (
      <div className="conn-sakura">
        <div className="conn-ra-row">
          <div className="conn-ra-text">
            <div className="conn-name">公网穿透 · 樱花frp</div>
            <div className="conn-sub">把后端暴露到公网——任何设备可连（隧道流量双向计费）</div>
          </div>
        </div>
        <div className="conn-sakura-login">
          <TextInput
            className="conn-key-input"
            value={key}
            onChange={setKey}
            placeholder="访问密钥（在 natfrp.com 用户中心生成）"
            aria-label="樱花frp 访问密钥"
          />
          <Button variant="primary" data-conn="sakura-login" disabled={key.trim() === ""} onClick={() => loginSakura(key)}>
            登录
          </Button>
        </div>
      </div>
    );
  }

  const [today, remaining] = sakura.traffic;
  return (
    <div className="conn-sakura" data-on="true">
      <div className="conn-ra-row">
        <div className="conn-ra-text">
          <div className="conn-name">
            公网穿透 · 樱花frp
            <span className="conn-sakura-user">{sakura.name} · {sakura.group} · {sakura.speed}</span>
          </div>
          <div className="conn-sub">
            今日 {humanBytes(today)} · 剩余 <span className="conn-traffic-left">{humanBytes(remaining)}</span>（隧道流量双向计费）
          </div>
        </div>
        <button type="button" className="conn-copy" data-conn="sakura-logout" onClick={logoutSakura} title="退出登录（清除本机密钥）">
          退出
        </button>
      </div>
      <div className="conn-ra-panel" ref={panelEnter}>
        {tunnels.length === 0 ? (
          <div className="conn-sakura-empty">
            还没有公网隧道——创建一条，远程设备即可通过公网地址连这个后端。
          </div>
        ) : (
          tunnels.map((t) => (
            <div className="conn-tunnel" key={t.id} data-online={t.online ? "true" : undefined}>
              <div className="conn-tunnel-head">
                <span className="conn-tunnel-name mono">{t.name}</span>
                <span className={"ag-pill " + (t.online ? "risk-low" : "src")}>{t.online ? "在线" : "已断开"}</span>
              </div>
              <div className="conn-cred">
                <span className="conn-cred-label">地址</span>
                <span className="conn-cred-value mono">{t.addr}</span>
                <CopyBtn value={t.addr} label="公网地址" />
              </div>
              <div className="conn-cred">
                <span className="conn-cred-label">节点</span>
                <span className="conn-cred-value">{t.nodeName} · 负载 {t.load}%</span>
              </div>
              <div className="conn-cred">
                <span className="conn-cred-label">用量</span>
                <span className="conn-cred-value">{humanBytes(t.used)}</span>
                <Button variant="ghost" className="conn-tunnel-btn" data-conn="tunnel-toggle" onClick={() => toggleTunnel(t.id)}>
                  {t.online ? "断开" : "启动"}
                </Button>
                <button
                  type="button"
                  className={"conn-copy danger" + (confirmId === t.id ? " confirm" : "")}
                  data-conn="tunnel-del"
                  onClick={() => {
                    if (confirmId === t.id) removeTunnel(t.id);
                    else setConfirmId(t.id);
                  }}
                  onBlur={() => setConfirmId(null)}
                >
                  {confirmId === t.id ? "确认删除" : "删除"}
                </button>
              </div>
            </div>
          ))
        )}
        <button type="button" className="conn-add" data-conn="tunnel-new" onClick={createTunnel}>
          + 创建公网隧道
        </button>
        <div className="conn-ra-hint">创建 = 自动选节点（负载最低）并分配端口，本地指向 127.0.0.1:7789；删除不连带本机地址。</div>
      </div>
    </div>
  );
}

/** 远程访问（被连）设置块：开关 + 地址/token 展示——面板展开有
 *  gsap 入场（上浮淡入 + 凭证行交错）。 */
function RemoteAccessBlock() {
  const { remoteAccess, setRemoteAccess, remoteAddr, remoteToken, regenerateRemoteToken } = useConnections();
  const [reveal, setReveal] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);
  const panelEnter = useEnterRef<HTMLDivElement>();
  return (
    <div className="conn-ra">
      <div className="conn-ra-row">
        <div className="conn-ra-text">
          <div className="conn-name">远程访问</div>
          <div className="conn-sub">允许其它设备连这台机器的后端（局域网/公网）</div>
        </div>
        <Toggle on={remoteAccess} onChange={setRemoteAccess} ariaLabel="远程访问" />
      </div>
      {remoteAccess && (
        <div
          className="conn-ra-panel"
          ref={(el) => {
            panelRef.current = el;
            panelEnter(el);
            // 凭证行交错浮现（地址/Token/hint）
            if (el) staggerIn(el.querySelectorAll(".conn-cred, .conn-ra-hint"), { each: 0.05 });
          }}
        >
          <div className="conn-cred">
            <span className="conn-cred-label">地址</span>
            <span className="conn-cred-value mono">{remoteAddr}</span>
            <CopyBtn value={remoteAddr} label="地址" />
          </div>
          <div className="conn-cred">
            <span className="conn-cred-label">Token</span>
            <span className="conn-cred-value mono">{reveal ? remoteToken : maskToken(remoteToken)}</span>
            <button type="button" className="conn-copy" onClick={() => setReveal((v) => !v)} title={reveal ? "隐藏" : "显示明文"}>
              {reveal ? "隐藏" : "显示"}
            </button>
            <CopyBtn value={remoteToken} label="Token" />
            <button type="button" className="conn-copy" onClick={regenerateRemoteToken} title="重新生成（旧 Token 立即失效）">
              重置
            </button>
          </div>
          <div className="conn-ra-hint">把地址和 Token 给要连你的设备；重置后旧 Token 立即失效。</div>
          <SakuraBlock />
        </div>
      )}
    </div>
  );
}

/** 从地址带出显示名（host 部分——名称留空时的兜底）。 */
function nameFromAddr(addr: string): string {
  try {
    const u = addr.includes("://") ? new URL(addr) : new URL(`http://${addr}`);
    return u.hostname;
  } catch {
    return addr;
  }
}

/** 添加/编辑远程连接表单：地址格式校验 + 名称自动带出 + 凭证引导。 */
function RemoteForm({ initial, onSave, onCancel }: { initial: RemoteConn; onSave: (c: RemoteConn) => void; onCancel: () => void }) {
  const [draft, setDraft] = useState(initial);
  const set = <K extends keyof RemoteConn>(k: K, v: RemoteConn[K]) => setDraft((d) => ({ ...d, [k]: v }));
  const addr = draft.addr.trim();
  // 地址格式：host:port 或 https://host（host 可为域名/IP）
  const addrOk = addr === "" || /^(https?:\/\/)?[a-zA-Z0-9.-]+(:\d{1,5})(\/.*)?$/.test(addr);
  const savable = addr !== "" && addrOk && draft.token.trim() !== "" && (draft.name.trim() !== "" || nameFromAddr(addr) !== addr);
  return (
    <div className="conn-form">
      <div className="cg-field">
        <span className="cg-field-label">地址</span>
        <TextInput
          className="cg-id-input"
          value={draft.addr}
          onChange={(v) => set("addr", v)}
          placeholder="host:port（如 10.0.0.8:7789）或 https://api.example.com"
          aria-label="地址"
        />
        {!addrOk && <div className="ag-warn">地址格式应为 host:port（如 10.0.0.8:7789）或 https://host。</div>}
      </div>
      <div className="cg-field">
        <span className="cg-field-label">名称</span>
        <TextInput
          value={draft.name}
          onChange={(v) => set("name", v)}
          placeholder={`显示名（留空自动取 ${addr ? nameFromAddr(addr) : "地址主机名"}）`}
          aria-label="名称"
        />
      </div>
      <div className="cg-field">
        <span className="cg-field-label">Token</span>
        <TextInput
          className="cg-id-input"
          value={draft.token}
          onChange={(v) => set("token", v)}
          placeholder="连接凭证（服务端生成）"
          aria-label="Token"
        />
        <div className="cg-field-hint">对方机器「连接 → 远程访问」面板里展示的 Token；或问管理员要。</div>
      </div>
      <div className="ag-edit-actions ti-foot">
        <Button variant="ghost" onClick={onCancel}>取消</Button>
        <Button
          variant="primary"
          disabled={!savable}
          onClick={() => onSave({ ...draft, name: draft.name.trim() || nameFromAddr(addr), addr, token: draft.token.trim() })}
        >
          保存
        </Button>
      </div>
    </div>
  );
}

export function ConnectionManager({ onClose }: { onClose: () => void }) {
  useEscape(true, onClose);
  const { remotes, addRemote, updateRemote, removeRemote, active, setActive } = useConnections();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<RemoteConn | null>(null);
  const [confirmId, setConfirmId] = useState<string | null>(null);

  return (
    <div
      className="ag-doc-mask"
      role="dialog"
      aria-modal="true"
      aria-label="连接管理"
      onPointerDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="ag-doc">
        <div className="ag-doc-head">
          <span className="ag-doc-title">连接</span>
          <button type="button" className="ag-doc-close" onClick={onClose} aria-label="关闭">
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="ag-doc-body">
          {adding || editing ? (
            <RemoteForm
              key={editing?.id ?? "new"}
              initial={editing ?? { id: crypto.randomUUID(), name: "", addr: "", token: "" }}
              onCancel={() => { setAdding(false); setEditing(null); }}
              onSave={(saved) => {
                if (adding) addRemote(saved);
                else if (editing) updateRemote(saved);
                setAdding(false);
                setEditing(null);
              }}
            />
          ) : (
            <>
              {/* 后端列表（连谁）：本机内置回落默认 + 远程后端 */}
              <div className="conn-item" data-active={active === "local" ? "true" : undefined}>
                <div className="conn-ra-row">
                  <div className="conn-ra-text">
                    <div className="conn-name">
                      <span className="conn-dot on" />本机{active === "local" && <span className="conn-tag">当前</span>}
                    </div>
                    <div className="conn-sub mono">{LOCAL_ADDR}</div>
                  </div>
                  {active !== "local" && (
                    <Button variant="ghost" data-conn="connect-local" onClick={() => setActive("local")}>连接</Button>
                  )}
                </div>
              </div>

              {remotes.map((c) => (
                <div className="conn-item" key={c.id} data-active={active === c.id ? "true" : undefined}>
                  <div className="conn-ra-row">
                    <div className="conn-ra-text">
                      <div className="conn-name">
                        <span className={"conn-dot" + (active === c.id ? " on" : "")} />
                        {c.name}
                        {active === c.id && <span className="conn-tag">当前</span>}
                      </div>
                      <div className="conn-sub mono">{c.addr} · {maskToken(c.token)}</div>
                    </div>
                    <div className="conn-actions">
                      {active === c.id ? (
                        <Button variant="ghost" data-conn="disconnect" onClick={() => setActive("local")}>断开</Button>
                      ) : (
                        <Button variant="ghost" data-conn="connect" onClick={() => setActive(c.id)}>连接</Button>
                      )}
                      <button type="button" className="ag-mini-btn" data-conn="edit" onClick={() => setEditing(c)}>
                        <IconPencil /> 编辑
                      </button>
                      <button
                        type="button"
                        className={"ag-mini-btn danger" + (confirmId === c.id ? " confirm" : "")}
                        data-conn="del"
                        onClick={() => {
                          if (confirmId === c.id) removeRemote(c.id);
                          else setConfirmId(c.id);
                        }}
                        onBlur={() => setConfirmId(null)}
                      >
                        <IconTrash /> {confirmId === c.id ? "确认" : "删除"}
                      </button>
                    </div>
                  </div>
                </div>
              ))}

              <button type="button" className="conn-add" data-conn="add" onClick={() => setAdding(true)}>
                + 添加远程后端
              </button>

              {/* 远程访问（被连）：局域网凭证 + 公网穿透——独立分区，
               *  与「连谁」的后端列表分开 */}
              <div className="conn-sec-title">远程访问<span className="conn-sec-sub">被连——把这台机器的后端暴露给其它设备</span></div>
              <div className="conn-item conn-ra-item">
                <RemoteAccessBlock />
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
