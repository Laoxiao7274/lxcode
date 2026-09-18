// 更新域：版本检查/下载/安装的共享状态机——设置面板的 UpdateBlock
// 与页面级 UpdateToast 共用（同一状态，两处展示）。
// 契约对齐自建 zip 更新（AGENTS.md §2.1）：manifest.json + update-<version>.zip、
// 两级更新（后端热替换 / asar 冷替换需重启）、哲学 = 定时检查 + 人工确认。
// 原型：内存态模拟（挂载后自动检查一次——演示「定时检查」的启动检查）；
// 后端化接 manifest 版本对比 / sha256 校验 / 流式下载 / 替换编排。
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

/** 当前版本（唯一源 = shell/package.json；后端化经协议 hello/manifest 获取）。 */
export const CURRENT_VERSION = "0.1.0";

/** 演示 manifest（真实 = GET manifest.json）。演示恒有新版本——展示完整流。 */
export const DEMO_MANIFEST = {
  version: "0.2.0",
  notes: [
    "Agent 名单与组装（模型/工具/上下文/委派）",
    "拓展：工具、技能、模板与 MCP 服务器管理",
    "连接管理：本机 / 远程后端 / 樱花frp 公网穿透",
    "设置面板重排与表单套件统一",
  ],
  size: 12_400_000,
};

export type UpdatePhase = "idle" | "checking" | "latest" | "available" | "downloading" | "ready";

interface UpdateValue {
  current: string;
  phase: UpdatePhase;
  manifest: typeof DEMO_MANIFEST | null;
  /** 下载进度（0-100）。 */
  progress: number;
  /** 用户已关闭提示（Toast 不再弹；设置里仍可继续）。 */
  toastDismissed: boolean;
  dismissToast: () => void;
  check: () => void;
  download: () => void;
  /** 立即重启（真实 = 壳退出重启，退出时完成冷替换）。 */
  restart: () => void;
}

const Ctx = createContext<UpdateValue | null>(null);

export function UpdateProvider({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<UpdatePhase>("idle");
  const [progress, setProgress] = useState(0);
  const [toastDismissed, setToastDismissed] = useState(false);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const check = useCallback(() => {
    setPhase("checking");
    // 演示：1s 后发现有新版本（真实 = GET manifest.json 比较版本）
    setTimeout(() => setPhase("available"), 900);
  }, []);

  const download = useCallback(() => {
    setPhase("downloading");
    setProgress(0);
    // 模拟下载进度（真实 = 流式下载 + sha256 校验）
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
  }, []);

  const restart = useCallback(() => {
    // 演示：重置为最新态（真实 = 壳退出重启，退出时完成 asar 冷替换）
    setPhase("latest");
    setToastDismissed(false);
  }, []);

  // 启动自动检查（演示「定时检查」——真实 = 定时 + 人工确认，发现新版本
  // 才弹提示，不打断）
  useEffect(() => {
    const t = setTimeout(() => check(), 1200);
    return () => { clearTimeout(t); if (timerRef.current) clearInterval(timerRef.current); };
  }, [check]);

  const value = useMemo(
    () => ({
      current: CURRENT_VERSION, phase, manifest: DEMO_MANIFEST, progress,
      toastDismissed, dismissToast: () => setToastDismissed(true), check, download, restart,
    }),
    [phase, progress, toastDismissed, check, download, restart],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useUpdate(): UpdateValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useUpdate 必须在 UpdateProvider 内使用");
  return v;
}
