// 公网穿透（樱花frp）块：登录（访问密钥）→ 隧道列表（地址/状态/用量）
// + 账户流量。实现文档 docs/sakurafrp-integration.md——原型假数据，
// API 层后接 api.natfrp.com/v4。
// 从 ConnectionManager 拆出（独立领域：有自己的状态、冒烟链路与
// 实现文档；ConnectionManager 只在远程访问面板里挂它）。
import { useEffect, useMemo, useState } from "react";
import QRCode from "qrcode";
import { humanBytes, useConnections } from "../../shared/connections";
import { pairPayload, tunnelPairEndpoints } from "../../shared/pairing";
import { useEnterRef } from "../../shared/anim";
import { useConfirmClick } from "../../shared/confirm-click";
import { Button, TextInput } from "../form";
import { CopyBtn } from "./CopyBtn";
import { SakuraNodePicker } from "./SakuraNodePicker";
import { loadText } from "../../shared/sakura-nodes";

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
  const { sakura, sakuraBusy, sakuraError, loginSakura, logoutSakura, refreshSakura, tunnels, createTunnel, toggleTunnel, removeTunnel, remoteToken, listSakuraNodes, onSakuraNodeLatency } = useConnections();
  const [key, setKey] = useState("");
  // 创建流程：先开节点选择面板（节点是隧道级属性，建完改不了），确认后才创建
  const [picking, setPicking] = useState(false);
  const panelEnter = useEnterRef<HTMLDivElement>();
  const busy = sakuraBusy !== null;

  // 配对二维码：在线隧道的公网地址 + 后端同一把 token（frp 只是隧道，门是
  // 同一把——internal/server/remote.go）。只展示在线隧道（离线地址扫了也连
  // 不上）；token 拿不到（无桥/后端未就绪）就不渲染，不放占位假码。
  const pairEndpoints = useMemo(
    () => (remoteToken ? tunnelPairEndpoints(tunnels) : []),
    [remoteToken, tunnels],
  );
  const [qr, setQr] = useState<{ url: string; label: string; desc: string } | null>(null);
  useEffect(() => {
    let alive = true;
    setQr(null);
    const ep = pairEndpoints[0];
    if (!ep) return;
    QRCode.toDataURL(pairPayload(ep.addr, remoteToken, ep.name), { margin: 2, width: 320, errorCorrectionLevel: "M" })
      .then((url) => {
        if (alive) setQr({ url, label: `${ep.label}（${ep.addr}）`, desc: "手机端「扫码配对」扫此码即可通过公网入口连接——地址与 Token 自动填入。隧道已带自动 HTTPS（节点对外 TLS）：若手机端连不上，把入口地址改用 wss:// 再试（客户端连接协议待真机验证）。" });
      })
      .catch(() => {
        if (alive) setQr(null);
      });
    return () => { alive = false; };
  }, [pairEndpoints, remoteToken]);

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
            <div className="conn-tunnel" key={t.id} data-online={t.online ? "true" : undefined} data-starting={t.starting && !t.online ? "true" : undefined}>
              <div className="conn-tunnel-head">
                <span className="conn-tunnel-name mono">{t.name}</span>
                {/* 三态：online = 节点端确认在线；starting = 本机 frpc 在跑但节点未确认
                    （地址扫了还连不上）；都不满足 = 已断开 */}
                {t.online ? (
                  <span className="ag-pill risk-low">在线</span>
                ) : t.starting ? (
                  <span className="ag-pill starting">启动中…</span>
                ) : (
                  <span className="ag-pill src">已断开</span>
                )}
                {t.autoHttps ? (
                  <span className="ag-pill risk-low" title="已配置自动 HTTPS——内地节点对 TCP 隧道上的 WS 流量不再回 501">HTTPS 直通</span>
                ) : (
                  <span className="ag-pill warn" title="未配置自动 HTTPS——内地节点会把这条隧道上的 WS 流量回 501；启动隧道时会自动补上">需修复</span>
                )}
              </div>
              <div className="conn-cred">
                <span className="conn-cred-label">地址</span>
                <span className="conn-cred-value mono">{t.addr}</span>
                <CopyBtn value={t.addr} label="公网地址" />
              </div>
              <div className="conn-cred">
                <span className="conn-cred-label">节点</span>
                <span className="conn-cred-value">{t.nodeName} · 负载 {loadText(t.load)}</span>
              </div>
              <div className="conn-cred">
                <span className="conn-cred-label">用量</span>
                <span className="conn-cred-value">{t.used === null ? "—" : humanBytes(t.used)}</span>
                <Button variant="ghost" className="conn-tunnel-btn" data-conn="tunnel-toggle" disabled={busy || (!!t.starting && !t.online)} onClick={() => toggleTunnel(t.id)}>
                  {t.online ? "断开" : "启动"}
                </Button>
                <TunnelDelBtn onConfirm={() => removeTunnel(t.id)} disabled={busy} />
              </div>
            </div>
          ))
        )}
        {sakuraError && <div className="conn-sakura-err" role="alert">{sakuraError}</div>}
        {picking ? (
          <SakuraNodePicker
            busy={sakuraBusy}
            onCancel={() => setPicking(false)}
            onConfirm={(nodeId) => {
              createTunnel(nodeId);
              setPicking(false);
            }}
            listNodes={listSakuraNodes}
            onLatency={onSakuraNodeLatency}
          />
        ) : (
          <button type="button" className="conn-add" data-conn="tunnel-new" disabled={busy} onClick={() => setPicking(true)}>
            {busy ? (sakuraBusy ?? "处理中…") : "+ 创建公网隧道"}
          </button>
        )}
        {qr && (
          <div className="conn-qr" role="img" aria-label={`配对二维码：公网入口 ${qr.label}`}>
            <img className="conn-qr-img" src={qr.url} alt="" width={160} height={160} />
            <div className="conn-qr-desc">
              <div className="conn-qr-title">公网扫码配对 · {qr.label}</div>
              <div>{qr.desc}</div>
            </div>
          </div>
        )}
        <div className="conn-ra-hint">创建时可选节点（自动 = 按负载最低）并自动分配端口，本地指向 127.0.0.1:7789；删除不连带本机地址。隧道在线后可用上方二维码配对（公网入口 + 同一把 Token）。</div>
      </div>
    </div>
  );
}
