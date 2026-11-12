// 连接域共享小组件：Token 凭证行（遮罩 + 显示/隐藏切换 + 复制 + 重置）。
// 从 LanAccessBlock 抽出——樱花frp 公网入口与局域网入口共用同一把 Token
// （frp 只是隧道，门是同一把——internal/server/remote.go），两处同款展示。
import { useState } from "react";
import { maskToken } from "../../shared/connections";
import { CopyBtn } from "../connections/CopyBtn";

export function TokenCred({
  token,
  busy,
  onRegenerate,
}: {
  token: string;
  busy?: boolean;
  onRegenerate?: () => void;
}) {
  const [reveal, setReveal] = useState(false);
  return (
    <div className="conn-cred">
      <span className="conn-cred-label">Token</span>
      <span className="conn-cred-value mono">{reveal ? token : maskToken(token)}</span>
      <button type="button" className="conn-copy" onClick={() => setReveal((v) => !v)} title={reveal ? "隐藏" : "显示明文"}>
        {reveal ? "隐藏" : "显示"}
      </button>
      <CopyBtn value={token} label="Token" />
      {onRegenerate && (
        <button type="button" className="conn-copy" disabled={busy} onClick={onRegenerate} title="重新生成（旧 Token 立即失效）">
          重置
        </button>
      )}
    </div>
  );
}
