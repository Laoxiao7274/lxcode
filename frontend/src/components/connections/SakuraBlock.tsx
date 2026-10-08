// 公网穿透（樱花frp）块：登录（访问密钥）→ 隧道列表（地址/状态/用量）
// + 账户流量。实现文档 docs/sakurafrp-integration.md——原型假数据，
// API 层后接 api.natfrp.com/v4。
// 从 ConnectionManager 拆出（独立领域：有自己的状态、冒烟链路与
// 实现文档；ConnectionManager 只在远程访问面板里挂它）。
import { useState } from "react";
import { humanBytes, useConnections } from "../../shared/connections";
import { useEnterRef } from "../../shared/anim";
import { useConfirmClick } from "../../shared/confirm-click";
import { Button, TextInput } from "../form";
import { CopyBtn } from "./CopyBtn";

/** 隧道删除钮（两步确认——useConfirmClick 统一行为）。 */
function TunnelDelBtn({ onConfirm, disabled }: { onConfirm: () => void; disabled?: boolean }) {
  const del = useConfirmClick(onConfirm);
  return (
    <button
      type="button"
      className={"conn-copy danger" + (del.confirming ? " confirm" : "")}
      data-conn="tunnel-del"
      disabled={disabled}
      onClick={del.onClick}
      onBlur={del.onBlur}
    >
      {del.confirming ? "确认删除" : "删除"}
    </button>
  );
}

export function SakuraBlock() {
  const { sakura, sakuraBusy, sakuraError, loginSakura, logoutSakura, refreshSakura, tunnels, createTunnel, toggleTunnel, removeTunnel } = useConnections();
  const [key, setKey] = useState("");
  const panelEnter = useEnterRef<HTMLDivElement>();
  const busy = sakuraBusy !== null;

  // 未登录：密钥输入（用户中心获取访问密钥）；登录失败/账户冻结就地报错
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
          <Button variant="primary" data-conn="sakura-login" disabled={key.trim() === "" || busy} onClick={() => loginSakura(key)}>
            {busy ? "登录中…" : "登录"}
          </Button>
        </div>
        {sakuraError && <div className="conn-sakura-err" role="alert">{sakuraError}</div>}
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
        <div className="conn-sakura-actions">
          <button type="button" className="conn-copy" data-conn="sakura-refresh" disabled={busy} onClick={refreshSakura} title="刷新账户流量与隧道列表（面板上新建的隧道也会出现）">
            刷新
          </button>
          <button type="button" className="conn-copy" data-conn="sakura-logout" disabled={busy} onClick={logoutSakura} title="退出登录（清除本机密钥）">
            退出
          </button>
        </div>
      </div>
      {busy && (
        <div className="conn-sakura-busy" role="status">
          <span className="conn-busy-spinner" aria-hidden="true" />
          {sakuraBusy}…
        </div>
      )}
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
                <span className="conn-cred-value">{t.used === null ? "—" : humanBytes(t.used)}</span>
                <Button variant="ghost" className="conn-tunnel-btn" data-conn="tunnel-toggle" disabled={busy} onClick={() => toggleTunnel(t.id)}>
                  {t.online ? "断开" : "启动"}
                </Button>
                <TunnelDelBtn onConfirm={() => removeTunnel(t.id)} disabled={busy} />
              </div>
            </div>
          ))
        )}
        {sakuraError && <div className="conn-sakura-err" role="alert">{sakuraError}</div>}
        <button type="button" className="conn-add" data-conn="tunnel-new" disabled={busy} onClick={createTunnel}>
          {busy ? (sakuraBusy ?? "处理中…") : "+ 创建公网隧道"}
        </button>
        <div className="conn-ra-hint">创建 = 自动选节点（负载最低）并分配端口，本地指向 127.0.0.1:7789；删除不连带本机地址。</div>
      </div>
    </div>
  );
}
