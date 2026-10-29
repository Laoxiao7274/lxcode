// 更新域：版本检查/下载/安装的共享状态机——统一更新弹窗 UpdateDialog
// 与设置面板的 UpdateBlock 共用（同一状态，一个提示形态：弹窗）。
//
// 两条「发现新版本」的路径都收敛到同一个 openDialog：
//   ① 渲染层启动检查 / 设置页「检查更新」（本文件 check()）；
//   ② 壳主进程运行期周期检查推送 update:available（onAvailable 订阅）。
// 两者都只做「setManifest + phase=available + 开弹窗」，不存在第二套 Toast。
//
// 契约对齐自建 zip 更新（AGENTS.md §2.1）：manifest.json + update-<version>.zip、
// 更新（独立 bat 完成全部替换与拉起，见 shell/src/apply-update.ts）、
// 哲学 = 定时检查 + 人工确认。
// 真链路 = getUpdateBridge()（壳主进程编排：manifest 拉取、sha256 校验、
// 更新（独立 bat 完成全部替换与拉起，见 shell/src/apply-update.ts）；浏览器模式无桥 → 演示回落。
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { getUpdateBridge, type UpdateBridge, type UpdateManifestView } from "../agent/host";

/** 状态机：available = 有新版本待下载；ready = 下载+校验完成、等待安装
 *  （apply 尚未排程，用户点「立即安装」才动）；installing = 已排程 bat，即将重启。 */
export type UpdatePhase = "idle" | "checking" | "latest" | "available" | "downloading" | "ready" | "installing";

/** 失败发生在哪一步——决定「重试」按钮重跑什么（检查 / 下载 / 安装）。 */
export type UpdateFailAction = "check" | "download" | "install";

/** 相对时间（「10 分钟前检查」）：设置页更新块的时间戳展示。
 *  纯函数（测试直接钉）；秒级以下不展示（刚检查完就显示「刚刚」）。 */
export function relTime(ts: number, now: number): string {
  const s = Math.max(0, Math.floor((now - ts) / 1000));
  if (s < 60) return "刚刚";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} 小时前`;
  return `${Math.floor(h / 24)} 天前`;
}

interface UpdateValue {
  current: string;
  phase: UpdatePhase;
  manifest: UpdateManifestView | null;
  /** 下载进度（0-100）。 */
  progress: number;
  /** 已下载 / 总字节（总量未知时 total = 0，弹窗不编数字）。 */
  downloaded: number;
  total: number;
  /** 下载阶段：verifying = 已下完、正在校验解压（进度条 100% 但仍在忙）。 */
  stage: "downloading" | "verifying";
  /** 最近一次检查完成的时刻（epoch ms；null = 从未检查过）。纯展示态——
   *  设置页用它显示「N 分钟前检查」，不影响状态机与管线。 */
  checkedAt: number | null;
  /** 最近一次操作的失败原因（检查失败/下载校验失败/安装排程失败——UI 显示，不静默）。 */
  error: string | null;
  /** 失败步骤（配合 error；null = 无失败）。 */
  failAction: UpdateFailAction | null;
  /** 统一更新弹窗是否打开（两条发现路径都置 true；设置页也可手动打开）。 */
  dialogOpen: boolean;
  openDialog: () => void;
  closeDialog: () => void;
  check: () => void;
  download: () => void;
  /** 排程安装（apply-update.bat）并重启壳——安装会关闭并重启应用。 */
  install: () => void;
  /** 立即重启（壳退出 → apply-update.bat 完成全部替换与拉起）。 */
  restart: () => void;
  /** 按失败步骤重试（检查 / 下载 / 安装）。 */
  retry: () => void;
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
  const [downloaded, setDownloaded] = useState(0);
  const [total, setTotal] = useState(0);
  const [stage, setStage] = useState<"downloading" | "verifying">("downloading");
  const [checkedAt, setCheckedAt] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [failAction, setFailAction] = useState<UpdateFailAction | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  // 桥钉在挂载时的那一个；null = 浏览器演示模式
  const bridgeRef = useRef<UpdateBridge | null>(getUpdateBridge());
  const bridge = bridgeRef.current;

  /** 失败落态：错误文案 + 失败步骤 + 回到可重试的 phase（检查→idle，
   *  下载→available，安装→ready）——弹窗据此显示「重试」并知道重跑什么。 */
  const fail = useCallback((e: unknown, action: UpdateFailAction) => {
    setError(e instanceof Error ? e.message : String(e));
    setFailAction(action);
    setPhase(action === "check" ? "idle" : action === "download" ? "available" : "ready");
  }, []);

  // 当前版本（真桥 = 壳 package.json）
  useEffect(() => {
    if (!bridge) return;
    bridge.version().then(setCurrent).catch(() => { /* 保持演示值 */ });
  }, [bridge]);

  const check = useCallback(() => {
    const b = bridgeRef.current;
    setError(null);
    setFailAction(null);
    if (!b) {
      // 演示：900ms 后发现有新版本 → 进统一弹窗
      setPhase("checking");
      setTimeout(() => {
        setManifest(DEMO_MANIFEST);
        setCheckedAt(Date.now());
        setPhase("available");
        setDialogOpen(true);
      }, 900);
      return;
    }
    setPhase("checking");
    b.check()
      .then((r) => {
        setManifest(r.manifest);
        setCheckedAt(Date.now());
        if (r.available) {
          setPhase("available");
          setDialogOpen(true); // 路径①：启动/手动检查发现新版本 → 同一弹窗
        } else {
          setPhase("latest");
        }
      })
      .catch((e) => fail(e, "check"));
  }, [fail]);

  const download = useCallback(async () => {
    const b = bridgeRef.current;
    setError(null);
    setFailAction(null);
    if (!b) {
      // 演示：模拟下载进度 → 就绪
      setPhase("downloading");
      setStage("downloading");
      setProgress(0);
      setDownloaded(0);
      setTotal(DEMO_MANIFEST.size);
      const t = setInterval(() => {
        setProgress((p) => {
          const next = Math.min(100, p + 4);
          setDownloaded(Math.round((DEMO_MANIFEST.size * next) / 100));
          if (next >= 100) {
            clearInterval(t);
            setStage("verifying");
            setTimeout(() => setPhase("ready"), 500);
          }
          return next;
        });
      }, 60);
      return;
    }
    setPhase("downloading");
    setStage("downloading");
    setProgress(0);
    setDownloaded(0);
    setTotal(0);
    const un = b.onProgress((p) => {
      setProgress(p.percent);
      setDownloaded(p.downloaded);
      setTotal(p.total);
      setStage(p.stage);
    });
    try {
      // download = 下载 + 整包/逐文件校验（暂存就绪，不碰运行中的文件）。
      // apply 留给用户点「立即安装」——下载完成不等于安装已排程。
      const d = await b.download();
      if (!d.ok) throw new Error("下载/校验失败");
      setProgress(100);
      setPhase("ready");
      setDialogOpen(true); // 下载期间关过弹窗 → 完成时重新引导到安装
    } catch (e) {
      fail(e, "download");
    } finally {
      un();
    }
  }, [fail]);

  const install = useCallback(async () => {
    const b = bridgeRef.current;
    setError(null);
    setFailAction(null);
    if (!b) { setPhase("latest"); setDialogOpen(false); return; } // 演示
    setPhase("installing");
    try {
      // apply = 暂存到 update-pending/staged + 排程 apply-update.bat（壳不退）。
      // 排程成功后立即退出壳：bat 等 PID 退出后完成替换并拉起新版。
      const r = await b.apply();
      if (!r.ok) throw new Error("安装排程失败");
      b.restart();
    } catch (e) {
      fail(e, "install");
    }
  }, [fail]);

  const restart = useCallback(() => {
    const b = bridgeRef.current;
    if (!b) { setPhase("latest"); setDialogOpen(false); return; } // 演示
    // 仅退出壳；替换由 install() 排程的 apply-update.bat 完成。弹窗正常路径
    // 走 install()（它排程后自动调这里），本方法保留给「已排程、只差退出」的场景。
    b.restart(); // 壳退出 → apply-update.bat 完成全部替换与拉起
  }, []);

  const retry = useCallback(() => {
    if (failAction === "download") void download();
    else if (failAction === "install") void install();
    else check();
  }, [failAction, download, install, check]);

  const openDialog = useCallback(() => setDialogOpen(true), []);
  // 关闭语义（弹窗里也写明）：available/ready = 稍后（状态留在设置页可继续）；
  // downloading = 下载在后台继续，完成后自动重新打开引导安装；
  // installing = 已排程、应用即将重启，弹窗不可关（close 在组件层屏蔽）。
  const closeDialog = useCallback(() => setDialogOpen(false), []);

  // 启动自动检查（定时检查的人工确认形态：发现新版本才开弹窗，不打断）
  useEffect(() => {
    if (!bridge) return;
    const t = setTimeout(() => check(), 1200);
    return () => clearTimeout(t);
  }, [bridge, check]);

  // 运行期周期检查（主进程每 6h+抖动拉 manifest）发现新版本 → 主进程推送
  // update:available（shell updater.ts 的 startUpdatePolling）→ 同一弹窗。
  // 失败静默是主进程侧的纪律，这里收到的推送必是「发现新版本」。
  useEffect(() => {
    if (!bridge?.onAvailable) return;
    return bridge.onAvailable((m) => {
      setError(null);
      setFailAction(null);
      setManifest(m);
      setPhase((p) => (p === "idle" || p === "latest" ? "available" : p));
      setDialogOpen(true); // 路径②：周期检查发现新版本 → 同一弹窗
    });
  }, [bridge]);

  // 上次更新失败（apply-update.bat 失败标记，壳启动时消费并推一次）→
  // 走与 install 失败同一条展示路径：error + failAction=install + 开弹窗，
  // 弹窗里给出可读原因与「重试安装 / 手工安装」指引。
  useEffect(() => {
    if (!bridge?.onUpdateFailed) return;
    return bridge.onUpdateFailed((message) => {
      setError(message);
      setFailAction("install");
      setPhase("ready");
      setDialogOpen(true);
    });
  }, [bridge]);

  const value = useMemo(
    () => ({
      current, phase, manifest, progress, downloaded, total, stage, checkedAt, error, failAction,
      dialogOpen, openDialog, closeDialog, check, download, install, restart, retry,
    }),
    [current, phase, manifest, progress, downloaded, total, stage, checkedAt, error, failAction,
      dialogOpen, openDialog, closeDialog, check, download, install, restart, retry],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useUpdate(): UpdateValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useUpdate 必须在 UpdateProvider 内使用");
  return v;
}
