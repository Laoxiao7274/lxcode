// 连接管理弹窗（连谁）：后端列表——本机回落默认 + 远程后端 + 添加/编辑表单。
// 「被连」的远程访问配置（局域网凭证 + 公网穿透模式）在侧栏「远程访问」页
// （components/remote/RemoteAccessPage）——两个入口各管一头。
// CopyBtn 在 ./CopyBtn，表单在 ./RemoteForm。
import { useState } from "react";
import { maskToken, useConnections, type RemoteConn } from "../../shared/connections";
import { useEscape } from "../../shared/popover";
import { useConfirmClick } from "../../shared/confirm-click";
import { Button } from "../form";
import { IconPencil, IconTrash } from "../icons";
import { RemoteForm } from "./RemoteForm";

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

              {/* 「被连」的远程访问配置在侧栏「远程访问」页——这里只管「连谁」 */}
              <div className="conn-ra-hint">要把这台机器的后端暴露给其它设备？去侧栏「远程访问」页配置（局域网 / 樱花frp / Tailscale）。</div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
