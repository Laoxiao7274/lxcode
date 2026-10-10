// 更新域：版本检查/下载/安装的共享状态机——设置面板的 UpdateBlock
// 与页面级 UpdateToast 共用（同一状态，两处展示）。
// 契约对齐自建 zip 更新（AGENTS.md §2.1）：manifest.json + update-<version>.zip、
// 两级更新（后端热替换 / asar 冷替换需重启）、哲学 = 定时检查 + 人工确认。
// 真链路 = getUpdateBridge()（壳主进程编排：manifest 拉取、sha256 校验、
// 后端热替换、asar 退出冷替换全不经过渲染层）；浏览器模式无桥 → 演示回落。
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { getUpdateBridge, type UpdateBridge, type UpdateManifestView } from "../agent/host";

export type UpdatePhase = "idle" | "checking" | "latest" | "available" | "downloading" | "ready";

interface UpdateValue {
  current: string;
  phase: UpdatePhase;
  manifest: UpdateManifestView | null;
  /** 下载进度（0-100）。 */
  progress: number;
  /** 最近一次操作的失败原因（检查失败/下载校验失败——UI 显示，不静默）。 */
  error: string | null;
  /** 用户已关闭提示（Toast 不再弹；设置里仍可继续）。 */
  toastDismissed: boolean;
  dismissToast: () => void;
  check: () => void;
  download: () => void;
  /** 立即重启（壳退出 → 冷替换脚本换 asar → 自动拉起）。 */
  restart: () => void;
}

const Ctx = createContext<UpdateValue | null>(null);

/** 演示 manifest（浏览器模式无桥时展示完整流——真链路元数据来自主进程）。 */
const DEMO_MANIFEST: UpdateManifestView = {
  version: "0.2.0",
  notes: [
    "Agent 名单与组装（模型/工具/上下文/委派）",
    "拓展：工具、技能、模板与 MCP 服务器管理",
    "连接管理：本机 / 远程后端 / 樱花frp 公网穿透",
    "设置面板重排与表单套件统一",
  ],
  size: 12_400_000,
};

export function UpdateProvider({ children }: { children: ReactNode }) {
  const [current, setCurrent] = useState("0.1.0");
  const [phase, setPhase] = useState<UpdatePhase>("idle");
  const [manifest, setManifest] = useState<UpdateManifestView | null>(null);
  const [progress, setProgress] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [toastDismissed, setToastDismissed] = useState(false);
  // 桥钉在挂载时的那一个；null = 浏览器演示模式
  const bridgeRef = useRef<UpdateBridge | null>(getUpdateBridge());
  const bridge = bridgeRef.current;
  const fail = useCallback((e: unknown) => {
    setError(e instanceof Error ? e.message : String(e));
    setPhase("idle");
  }, []);

  // 当前版本（真桥 = 壳 package.json）
  useEffect(() => {
    if (!bridge) return;
    bridge.version().then(setCurrent).catch(() => { /* 保持演示值 */ });
  }, [bridge]);

  const check = useCallback(() => {
    const b = bridgeRef.current;
    setError(null);
    if (!b) {
      // 演示：1s 后发现有新版本
      setPhase("checking");
      setTimeout(() => { setManifest(DEMO_MANIFEST); setPhase("available"); }, 900);
      return;
    }
    setPhase("checking");
    b.check()
      .then((r) => { setManifest(r.manifest); setPhase(r.available ? "available" : "latest"); })
      .catch(fail);
  }, [fail]);

  const download = useCallback(async () => {
    const b = bridgeRef.current;
    if (!b) {
      // 演示：模拟下载进度
      setPhase("downloading");
      setProgress(0);
      const t = setInterval(() => {
        setProgress((p) => {
          if (p >= 100) { clearInterval(t); setPhase("ready"); return 100; }
          return p + 4;
        });
      }, 60);
      return;
    }
    setError(null);
    setPhase("downloading");
    setProgress(0);
    const un = b.onProgress((p) => setProgress(p.percent));
    try {
      // download = 下载 + 整包/逐文件校验（暂存就绪）；apply = 后端热替换 +
      // asar 冷替换排程（主进程编排，壳不退）。两步分开——校验失败绝不碰运行中的文件。
      const d = await b.download();
      if (!d.ok) throw new Error("下载/校验失败");
      await b.apply();
      setPhase("ready");
    } catch (e) {
      fail(e);
    } finally {
      un();
    }
  }, [fail]);

  const restart = useCallback(() => {
    const b = bridgeRef.current;
    if (!b) { setPhase("latest"); return; } // 演示
    b.restart(); // 壳退出 → 冷替换脚本换 asar → 自动拉起
  }, []);

  // 启动自动检查（定时检查的人工确认形态：发现新版本才弹提示，不打断）
  useEffect(() => {
    if (!bridge) return;
    const t = setTimeout(() => check(), 1200);
    return () => clearTimeout(t);
  }, [bridge, check]);

  const value = useMemo(
    () => ({
      current, phase, manifest, progress, error,
      toastDismissed, dismissToast: () => setToastDismissed(true), check, download, restart,
    }),
    [current, phase, manifest, progress, error, toastDismissed, check, download, restart],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useUpdate(): UpdateValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useUpdate 必须在 UpdateProvider 内使用");
  return v;
}
