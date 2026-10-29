// 远程访问页：把这台机器的后端暴露给其它设备的全部配置。
// 多模式架构——模式注册表（REMOTE_MODES）是唯一的扩展点：每个模式一张卡
// （标题 + 一句话说明 + 块组件），新加模式 = 注册表加一项 + 一个块组件。
// 目前：局域网直连（默认形态）/ 樱花frp 公网穿透 / Tailscale 组网。
import type { ReactNode } from "react";
import type { SakuraBridge } from "../../agent/sakura";
import type { TailscaleBridge } from "../../agent/tailscale";
import type { RemoteControlBridge } from "../../agent/host";
import { SakuraBlock } from "../connections/SakuraBlock";
import { LanAccessBlock } from "./LanAccessBlock";
import { TailscaleBlock } from "./TailscaleBlock";

interface RemoteMode {
  id: string;
  /** 一句话说明（卡片头部）。 */
  desc: string;
  /** 模式配置块（状态与操作都在块里）。 */
  render: () => ReactNode;
}

function buildModes(sakura: SakuraBridge | null, tailscale: TailscaleBridge | null): RemoteMode[] {
  return [
    { id: "lan", desc: "同一网络内的设备直连（默认，零配置）", render: () => <LanAccessBlock /> },
    { id: "sakura", desc: "公网穿透——任何设备可连（流量双向计费）", render: () => <SakuraBlock /> },
    { id: "tailscale", desc: "虚拟组网——tailnet 内设备可连（需要 Tailscale 客户端）", render: () => <TailscaleBlock bridge={tailscale} /> },
  ];
}

export function RemoteAccessPage({ sakuraBridge, tailscaleBridge, remoteControlBridge }: {
  sakuraBridge: SakuraBridge | null;
  tailscaleBridge: TailscaleBridge | null;
  remoteControlBridge: RemoteControlBridge | null;
}) {
  const modes = buildModes(sakuraBridge, tailscaleBridge);
  return (
    <div className="ra-page">
      <div className="ra-head">
        <div className="ra-title">远程访问</div>
        <div className="ra-sub">把这台机器的后端（127.0.0.1:7789）暴露给其它设备——按网络环境选一种或多种模式</div>
      </div>
      {modes.map((m) => (
        <section key={m.id} className="ra-mode" data-mode={m.id}>
          <div className="ra-mode-head">
            <span className="ra-mode-desc">{m.desc}</span>
          </div>
          {m.render()}
        </section>
      ))}
    </div>
  );
}
