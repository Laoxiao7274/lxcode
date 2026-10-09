// 局域网直连模式（被连的默认形态）：开关 + 地址/Token 展示与复制。
// 从 ConnectionManager 迁入远程访问页——与樱花frp/Tailscale 并列为模式卡片。
// 开启态附带配对二维码（lxcode://pair?addr=…&token=…）——手机端扫码自动填入。
import { useEffect, useMemo, useRef, useState } from "react";
import QRCode from "qrcode";
import { maskToken, useConnections } from "../../shared/connections";
import { useEnterRef } from "../../shared/anim";
import { staggerIn } from "../../shared/motion";
import { nameFromAddr } from "../connections/RemoteForm";
import { CopyBtn } from "../connections/CopyBtn";
import { Toggle } from "../form";

/** 配对载荷：lxcode://pair?addr=<host:port>&token=<token>&name=<hostname>。
 *  组件值 URL encode（URLSearchParams）；name 取地址主机名，取不到不带该参数。 */
function pairPayload(addr: string, token: string): string {
  const params = new URLSearchParams();
  params.set("addr", addr);
  params.set("token", token);
  const name = nameFromAddr(addr);
  if (name && name !== addr) params.set("name", name);
  return `lxcode://pair?${params.toString()}`;
}

export function LanAccessBlock() {
  const { remoteAccess, remoteBusy, setRemoteAccess, remoteAddr, remoteToken, regenerateRemoteToken } = useConnections();
  const [reveal, setReveal] = useState(false);
  const panelEnter = useEnterRef<HTMLDivElement>();
  // 配对二维码：仅在开启且地址/Token 都可得时生成（缺一就不渲染，不放占位假码）
  const payload = useMemo(
    () => (remoteAccess && remoteAddr && remoteToken ? pairPayload(remoteAddr, remoteToken) : ""),
    [remoteAccess, remoteAddr, remoteToken],
  );
  const [qrDataUrl, setQrDataUrl] = useState("");
  useEffect(() => {
    let alive = true;
    if (!payload) {
      setQrDataUrl("");
      return;
    }
    QRCode.toDataURL(payload, { margin: 2, width: 320, errorCorrectionLevel: "M" })
      .then((url) => {
        if (alive) setQrDataUrl(url);
      })
      .catch(() => {
        if (alive) setQrDataUrl("");
      });
    return () => {
      alive = false;
    };
  }, [payload]);
  return (
    <div className="conn-ra">
      <div className="conn-ra-row">
        <div className="conn-ra-text">
          <div className="conn-name">局域网直连</div>
          <div className="conn-sub">同一网络内的设备直接连这个地址（开启后所有连接须携带 Token）</div>
        </div>
        <Toggle on={remoteAccess} onChange={setRemoteAccess} disabled={remoteBusy} ariaLabel="局域网远程访问" />
      </div>
      {remoteAccess && (
        <div
          className="conn-ra-panel"
          ref={(el) => {
            panelEnter(el);
            if (el) staggerIn(el.querySelectorAll(".conn-cred, .conn-qr, .conn-ra-hint"), { each: 0.05 });
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
            <button type="button" className="conn-copy" disabled={remoteBusy} onClick={regenerateRemoteToken} title="重新生成（旧 Token 立即失效）">
              重置
            </button>
          </div>
          {qrDataUrl && (
            <div className="conn-qr" role="img" aria-label="配对二维码：扫此码在手机端填入连接信息">
              <img className="conn-qr-img" src={qrDataUrl} alt="" width={160} height={160} />
              <div className="conn-qr-desc">
                <div className="conn-qr-title">扫码配对</div>
                <div>手机端「扫码配对」扫此码即可自动填入地址与 Token</div>
              </div>
            </div>
          )}
          <div className="conn-ra-hint">把地址和 Token 给要连你的设备；重置后旧 Token 立即失效。本机的 lxcode 窗口不受影响。</div>
        </div>
      )}
    </div>
  );
}
