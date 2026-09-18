// 连接管理弹窗（从原 15.8KB 拆薄——只剩编排与远程访问块）：
// 后端列表（本机回落默认 + 远程后端 + 添加/编辑表单）+ 远程访问分区
// （局域网凭证 + 公网穿透）。壳的身份 = 连的谁。
// CopyBtn 在 ./CopyBtn，樱花frp 块在 ./SakuraBlock，表单在 ./RemoteForm。
import { useRef, useState } from "react";
import { maskToken, useConnections, type RemoteConn } from "../../shared/connections";
import { useEscape } from "../../shared/popover";
import { staggerIn } from "../../shared/motion";
import { useEnterRef } from "../../shared/anim";
import { useConfirmClick } from "../../shared/confirm-click";
import { Button, Toggle } from "../form";
import { IconPencil, IconTrash } from "../icons";
import { CopyBtn } from "./CopyBtn";
import { RemoteForm } from "./RemoteForm";
import { SakuraBlock } from "./SakuraBlock";

const LOCAL_ADDR = "127.0.0.1:7789";

/** 远程后端行的删除钮（两步确认——useConfirmClick 统一行为）。 */
function RemoteDelBtn({ onConfirm }: { onConfirm: () => void }) {
  const del = useConfirmClick(onConfirm);
  return (
    <button
      type="button"
      className={"ag-mini-btn danger" + (del.confirming ? " confirm" : "")}
      data-conn="del"
      onClick={del.onClick}
      onBlur={del.onBlur}
    >
      <IconTrash /> {del.confirming ? "确认" : "删除"}
    </button>
  );
}

/** 远程访问（被连）设置块：开关 + 地址/token 展示——面板展开有
 *  gsap 入场（上浮淡入 + 凭证行交错）+ 公网穿透（樱花frp）。 */
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

export function ConnectionManager({ onClose }: { onClose: () => void }) {
  useEscape(true, onClose);
  const { remotes, addRemote, updateRemote, removeRemote, active, setActive } = useConnections();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<RemoteConn | null>(null);

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
                      <RemoteDelBtn onConfirm={() => removeRemote(c.id)} />
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
