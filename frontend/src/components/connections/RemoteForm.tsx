// 添加/编辑远程连接表单（从 ConnectionManager 拆出）：
// 地址格式校验 + 名称自动带出 + 凭证引导。
import { useState } from "react";
import type { RemoteConn } from "../../shared/connections";
import { Button, TextInput } from "../form";

/** 从地址带出显示名（host 部分——名称留空时的兜底）。 */
export function nameFromAddr(addr: string): string {
  try {
    const u = addr.includes("://") ? new URL(addr) : new URL(`http://${addr}`);
    return u.hostname;
  } catch {
    return addr;
  }
}

export function RemoteForm({ initial, onSave, onCancel }: { initial: RemoteConn; onSave: (c: RemoteConn) => void; onCancel: () => void }) {
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
