// 连接管理弹窗：本机（回落默认 + 远程访问开关）+ 远程连接列表 + 添加。
// 壳的身份 = 连的谁：本机 127.0.0.1:7789 内置；远程连接（地址 + token）
// 可增删改、一键切换；本机被连 = 开启远程访问后展示地址与 token
// （遮罩显示，复制/重新生成）。
import { useState } from "react";
import { maskToken, useConnections, type RemoteConn } from "../../shared/connections";
import { useEscape } from "../../shared/popover";
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

/** 远程访问（被连）设置块：开关 + 地址/token 展示。 */
function RemoteAccessBlock() {
  const { remoteAccess, setRemoteAccess, remoteAddr, remoteToken, regenerateRemoteToken } = useConnections();
  const [reveal, setReveal] = useState(false);
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
        <div className="conn-ra-panel">
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
        </div>
      )}
    </div>
  );
}

/** 添加/编辑远程连接表单。 */
function RemoteForm({ initial, onSave, onCancel }: { initial: RemoteConn; onSave: (c: RemoteConn) => void; onCancel: () => void }) {
  const [draft, setDraft] = useState(initial);
  const set = <K extends keyof RemoteConn>(k: K, v: RemoteConn[K]) => setDraft((d) => ({ ...d, [k]: v }));
  const savable = draft.name.trim() !== "" && draft.addr.trim() !== "" && draft.token.trim() !== "";
  return (
    <div className="conn-form">
      <div className="cg-field">
        <span className="cg-field-label">名称</span>
        <TextInput value={draft.name} onChange={(v) => set("name", v)} placeholder="如：公司开发机" aria-label="名称" />
      </div>
      <div className="cg-field">
        <span className="cg-field-label">地址</span>
        <TextInput className="cg-id-input" value={draft.addr} onChange={(v) => set("addr", v)} placeholder="host:port（如 10.0.0.8:7789）" aria-label="地址" />
      </div>
      <div className="cg-field">
        <span className="cg-field-label">Token</span>
        <TextInput className="cg-id-input" value={draft.token} onChange={(v) => set("token", v)} placeholder="连接凭证（服务端生成）" aria-label="Token" />
        <div className="cg-field-hint">问后端管理员要，或看「远程访问」面板里展示的 Token。</div>
      </div>
      <div className="ag-edit-actions ti-foot">
        <Button variant="ghost" onClick={onCancel}>取消</Button>
        <Button
          variant="primary"
          disabled={!savable}
          onClick={() => onSave({ ...draft, name: draft.name.trim(), addr: draft.addr.trim(), token: draft.token.trim() })}
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
              {/* 本机：内置回落默认 + 远程访问（被连） */}
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
                <RemoteAccessBlock />
              </div>

              {/* 远程连接列表 */}
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
                + 添加连接
              </button>
              <div className="conn-foot-hint">连接信息保存在本机；Token 由服务端生成与校验，切换连接即重连后端。</div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
