// 软件更新块（设置 · 通用分区底部）：版本号 + 检查/下载/安装流。
// 契约对齐后端已定的自建 zip 更新（AGENTS.md §2.1）：manifest.json +
// update-<version>.zip；两级更新——后端热替换 / asar 冷替换（退出时换，
// 重启生效）；哲学 = 定时检查 + 人工确认。
// 原型：内存态模拟全流程（检查→新版本→下载进度→就绪）；后端化接
// manifest 检查（版本对比）/ sha256 校验 / 下载 / 替换编排。
import { useEffect, useRef, useState } from "react";
import { Button } from "../form";
import { humanBytes } from "../../shared/connections";

/** 当前版本（shell/package.json 的 version——唯一版本源；后端化时经
 *  协议 hello 或 manifest 获取）。 */
const CURRENT = "0.1.0";

/** 演示 manifest（后端化时 GET manifest.json 的形状——version/notes/
 *  size/sha256）。演示恒有新版本——展示完整 UI 流。 */
const DEMO_MANIFEST = {
  version: "0.2.0",
  notes: [
    "Agent 名单与组装（模型/工具/上下文/委派）",
    "拓展：工具、技能、模板与 MCP 服务器管理",
    "连接管理：本机 / 远程后端 / 樱花frp 公网穿透",
    "设置面板重排与表单套件统一",
  ],
  size: 12_400_000,
};

type Phase = "idle" | "checking" | "latest" | "available" | "downloading" | "ready";

export function UpdateBlock() {
  const [phase, setPhase] = useState<Phase>("idle");
  const [progress, setProgress] = useState(0);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => () => { if (timerRef.current) clearInterval(timerRef.current); }, []);

  const check = () => {
    setPhase("checking");
    // 演示：1s 后发现有新版本（真实：GET manifest.json 比较版本）
    setTimeout(() => setPhase("available"), 900);
  };

  const download = () => {
    setPhase("downloading");
    setProgress(0);
    // 模拟下载进度（真实：流式下载 + sha256 校验）
    if (timerRef.current) clearInterval(timerRef.current);
    timerRef.current = setInterval(() => {
      setProgress((p) => {
        if (p >= 100) {
          if (timerRef.current) clearInterval(timerRef.current);
          setPhase("ready");
          return 100;
        }
        return p + 4;
      });
    }, 60);
  };

  return (
    <div className="set-update" data-phase={phase}>
      <div className="set-row set-row-block">
        <div className="set-update-head">
          <div className="set-row-label">软件更新</div>
          <span className="set-update-ver mono">lxcode {CURRENT}</span>
        </div>

        {phase === "idle" && (
          <div className="set-update-actions">
            <Button variant="ghost" data-su="check" onClick={check}>检查更新</Button>
          </div>
        )}
        {phase === "checking" && (
          <div className="set-update-status">
            <span className="mset-spinner" />
            正在检查更新…
          </div>
        )}
        {phase === "latest" && (
          <div className="set-update-status ok">✓ 已是最新版本</div>
        )}
        {phase === "available" && (
          <div className="set-update-panel">
            <div className="set-update-new">
              <span className="set-update-dot" />
              新版本 <span className="mono">{DEMO_MANIFEST.version}</span>
              <span className="set-update-size">{humanBytes(DEMO_MANIFEST.size)}</span>
            </div>
            <ul className="set-update-notes">
              {DEMO_MANIFEST.notes.map((n) => <li key={n}>{n}</li>)}
            </ul>
            <div className="set-update-actions">
              <Button variant="primary" data-su="download" onClick={download}>
                下载并安装（{humanBytes(DEMO_MANIFEST.size)}）
              </Button>
            </div>
          </div>
        )}
        {phase === "downloading" && (
          <div className="set-update-panel">
            <div className="set-update-progress-row">
              <div className="set-update-progress">
                <div className="set-update-progress-bar" style={{ width: `${progress}%` }} />
              </div>
              <span className="mono">{progress}%</span>
            </div>
            <div className="set-update-hint">下载中……校验后自动准备安装</div>
          </div>
        )}
        {phase === "ready" && (
          <div className="set-update-panel">
            <div className="set-update-status ok">✓ 更新已就绪（{DEMO_MANIFEST.version}）——重启 lxcode 后生效</div>
            <div className="set-update-actions">
              <Button variant="primary" data-su="restart" onClick={() => setPhase("latest")}>
                立即重启
              </Button>
              <span className="set-update-hint">稍后重启也行——退出时自动完成替换</span>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
