// 局域网直连模式（被连的默认形态）：开关 + 地址/Token 展示与复制。
// 从 ConnectionManager 迁入远程访问页——与樱花frp/Tailscale 并列为模式卡片。
// 开启态附带配对二维码（lxcode://pair?addr=…&token=…）——手机端扫码自动填入。
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import QRCode from "qrcode";
import { maskToken, useConnections } from "../../shared/connections";
import { pairPayload } from "../../shared/pairing";
import { playEnter } from "../../shared/anim";
import { staggerIn } from "../../shared/motion";
import { CopyBtn } from "../connections/CopyBtn";
import { Toggle } from "../form";

export function LanAccessBlock() {
  const { remoteAccess, remoteBusy, setRemoteAccess, remoteAddr, remoteToken, regenerateRemoteToken } = useConnections();
  const [reveal, setReveal] = useState(false);
  // 面板 ref：恒定回调（useCallback + playEnter 模块函数）——内联 ref 每次渲染
  // 都会被 React 重调（旧 ref 传 null、新 ref 传 el），里面若触发动画就会随
  // 父级每次重渲染重播，卡片反复淡入即「远程访问页一直闪」。
  const panelRef = useRef<HTMLDivElement | null>(null);
  const setPanelRef = useCallback((el: HTMLDivElement | null) => {
    panelRef.current = el;
    playEnter(el);
  }, []);
  // 面板入场交错：由 effect 驱动、只在面板挂载（remoteAccess 开启）时播一次；
  // 门控仍走 staggerIn 内部的 motionAllowed（reduced-motion/测试开关）。
  useEffect(() => {
    if (!remoteAccess) return;
    const el = panelRef.current;
    if (el) staggerIn(el.querySelectorAll(".conn-cred, .conn-qr, .conn-ra-hint"), { each: 0.05 });
  }, [remoteAccess]);
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
        <div className="conn-ra-panel" ref={setPanelRef}>
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
