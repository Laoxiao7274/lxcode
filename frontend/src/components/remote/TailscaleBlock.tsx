// Tailscale 模式块：CLI 检测（安装/登录态）→ serve 发布开关 → tailnet 地址。
// 真实状态在壳主进程（shell/src/tailscale.ts），浏览器模式无桥 → 提示去壳内用。
import { useEffect, useState } from "react";
import type { TailscaleBridge, TailscaleStateView } from "../../agent/tailscale";
import { CopyBtn } from "../connections/CopyBtn";

const DOWNLOAD_URL = "https://tailscale.com/download";

export function TailscaleBlock({ bridge }: { bridge: TailscaleBridge | null }) {
  const [state, setState] = useState<TailscaleStateView | null>(null);
  const [localErr, setLocalErr] = useState<string | null>(null);

  useEffect(() => {
    if (!bridge) return;
    let alive = true;
    const apply = (s: TailscaleStateView) => { if (alive) setState(s); };
    const un = bridge.onState(apply);
    bridge.getState().then(apply).catch(() => { /* 推送会补 */ });
    return () => { alive = false; un(); };
  }, [bridge]);

  // 浏览器模式：Tailscale 检测要跑本机 CLI，只有壳里有
  if (!bridge) {
    return (
      <div className="conn-ra" data-ts="demo">
        <ModeHead sub="需要桌面壳支持——请在 lxcode 桌面应用内使用此模式" />
      </div>
    );
  }

  const act = (p: Promise<unknown>) => { setLocalErr(null); p.catch((e) => setLocalErr(e instanceof Error ? e.message : String(e))); };
  const busy = state?.busy !== null && state?.busy !== undefined;

  return (
    <div className="conn-ra" data-ts={state?.installed ? "ready" : "absent"}>
      <ModeHead
        sub={
          state === null ? "正在检测 Tailscale…"
          : !state.installed ? "未检测到 Tailscale——安装并登录后自动识别"
          : state.backendState !== "Running" ? "Tailscale 已安装但未连接"
          : state.ipv4 ? `本机 tailnet IP ${state.ipv4}` : "已连接"
        }
      />
      {state?.error && <div className="conn-sakura-err" role="alert">{state.error}</div>}
      {localErr && <div className="conn-sakura-err" role="alert">{localErr}</div>}
      {state?.installed && state.backendState !== "Running" && (
        <div className="conn-ra-hint">
          {state.hint}
          {!state.installed && <> —— <a href={DOWNLOAD_URL} target="_blank" rel="noreferrer">下载 Tailscale</a></>}
        </div>
      )}
      {state?.installed && state.backendState === "Running" && (
        <div className="conn-ra-panel" data-on={state.serveOn ? "true" : undefined}>
          {state.serveOn && state.serveUrl ? (
            <>
              <div className="conn-cred">
                <span className="conn-cred-label">地址</span>
                <span className="conn-cred-value mono">{state.serveUrl}</span>
                <CopyBtn value={state.serveUrl} label="tailnet 地址" />
              </div>
              <div className="conn-cred">
                <span className="conn-cred-label">状态</span>
                <span className="conn-cred-value">已发布到 tailnet（https 反代，支持 WebSocket）</span>
                <button type="button" className="conn-copy danger" disabled={busy} onClick={() => act(bridge.serveOff())}>
                  关闭发布
                </button>
              </div>
              <div className="conn-ra-hint">同一 tailnet 内的设备用这个地址直连后端；关闭发布后地址立即失效。</div>
            </>
          ) : (
            <>
              <div className="conn-ra-hint">
                把本机后端（127.0.0.1:7789）以 https 反代发布到 tailnet——tailnet 内设备即可直连，后端零配置。
              </div>
              <button type="button" className="conn-add" disabled={busy} onClick={() => act(bridge.serveOn())}>
                {busy ? (state.busy ?? "处理中…") : "开启 tailnet 访问"}
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}

function ModeHead({ sub }: { sub: string }) {
  return (
    <div className="conn-ra-row">
      <div className="conn-ra-text">
        <div className="conn-name">Tailscale</div>
        <div className="conn-sub">{sub}</div>
      </div>
    </div>
  );
}
