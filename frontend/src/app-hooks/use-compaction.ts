// 手动压缩（chat.compact）：请求类失败进一次性提示（不动 blocks）；没有可压
// 收益时给一句人话原因（不是错误——历史还太短是正常态）。压缩成功由
// chat.compacted 事件渲染标记块。
// 审查 M5 前端重构：从 App.tsx 纯移动，语义零改动。
//
// **进行中要有反馈（2026-12 用户报「点了没反应」）**：压缩要同步等一次摘要调用
//（大会话 10~30s，chat.compact 在 HEAVY_METHODS 里放宽到 30s 就是佐证），期间
// 完全无反馈 = 用户以为按钮坏了。compacting state 驱动两处 UI：
//  - ContextIndicator 的按钮（spinner + 「压缩中…」+ 禁用——防重复点击）；
//  - Composer 上方的 op-notice 状态条（/compact 斜杠命令与按钮共用同一条链路，
//    入口在哪触发都能看到）。
//
// **「超时但成功」的尾巴（2026-12 用户报「压缩失败: 请求超时」）**：call 的超时
// 只 reject 前端请求、**不 abort 后端**——后端 Compact 用 context.Background()
// 没有 deadline，30s（现在是独立 5 分钟档）超时后它仍会继续跑完摘要并广播
// chat.compacted。所以超时不能定死失败：发请求前先订阅事件流，超时后监听仍存活
//（兜底窗口 = 压缩超时档同额度），chat.compacted 随后到达就认账为成功并重拉历史
//（检查点卡落位），否则如实提示「仍在后台进行」。重入闸（inFlight）在超时路径上
// 保持到事件到达或兜底过期——不然用户重试会触发第二次全量 prefill（双倍等待）。
import { useCallback, useEffect, useRef, useState } from "react";
import type { AgentSource } from "../shared/types";
import { COMPACT_TIMEOUT_MS } from "../agent/ws/connection";

/** 超时后的兜底窗口：与 chat.compact 的请求超时同额度（后端没有 deadline，
 *  正常情况下「超时 + 后端跑完剩余时间」不会超过请求档的两倍；取同档值已是
 *  宽裕的兜底）。期间 inFlight 保持、chat.compacted 到达即认账成功。 */
const COMPACT_TAIL_GRACE_MS = COMPACT_TIMEOUT_MS;

export function useCompaction({ source, currentId, reportError, reloadHistory }: {
  source: AgentSource;
  currentId: string;
  reportError: (message: string) => void;
  /** 压缩成功后重拉历史（落位检查点卡）：压缩把历史前缀换成了摘要检查点，而
   *  chat.compacted 事件只是往**时间线末尾**插一张标记块——被压缩的旧气泡还挂在
   *  屏上、检查点卡也不在其语义位置（前缀处）。重拉后历史应答的 checkpoints 让
   *  检查点卡落到正确位置、旧气泡从时间线消失。缺席（演示源）= 不重拉，行为同旧。 */
  reloadHistory?: (sessionId: string) => Promise<void>;
}) {
  const [compacting, setCompacting] = useState(false);
  /** 重入闸（ref 而不是依赖数组：compacting 进 useCallback 依赖会让入口身份
   *  每次翻转，击穿 ContextIndicator 的 memo——2026-12 渲染性能 Top1 的老坑）。
   *  超时路径上**不复位**（keepGate）——保持到 chat.compacted 到达或兜底过期，
   *  防止超时后的重复点击触发第二次全量 prefill。 */
  const inFlight = useRef(false);
  /** 「超时但成功」尾巴的资源（事件退订 + 兜底 timer）：handleCompact 内创建，
   *  这里只持引用供卸载清理——组件卸载时监听与 timer 必须撤掉（防泄漏）。 */
  const cleanupRef = useRef<(() => void) | null>(null);
  useEffect(() => () => { cleanupRef.current?.(); }, []);

  const handleCompact = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setCompacting(true);

    // 尾巴监听先行注册（在请求发出之前）：超时 reject 之后事件路由照常分发，
    // 这个监听就是「后端其实跑完了」的唯一的认账口。settled 防止正常应答与
    // 事件双路径各结算一次。
    let settled = false;
    let unsubscribe: (() => void) | null = null;
    let tailTimer: ReturnType<typeof setTimeout> | null = null;
    const cleanup = () => {
      cleanupRef.current = null;
      if (unsubscribe) { unsubscribe(); unsubscribe = null; }
      if (tailTimer) { clearTimeout(tailTimer); tailTimer = null; }
    };
    cleanupRef.current = cleanup;
    const finishSuccess = async () => {
      // 成功：重拉历史让检查点卡落位。失败只记提示（压缩本身已成功——
      // 标记块由 chat.compacted 事件照常到达，重拉失败不该把它说成失败）。
      if (reloadHistory) {
        try {
          await reloadHistory(currentId);
        } catch (e) {
          reportError(`压缩完成，但刷新历史失败: ${e instanceof Error ? e.message : String(e)}`);
        }
      }
    };
    unsubscribe = source.subscribe((ev) => {
      if (settled || ev.type !== "compacted" || ev.sessionId !== currentId) return;
      settled = true;
      cleanup();
      inFlight.current = false;
      setCompacting(false);
      void finishSuccess();
    });

    // 超时路径不复位闸门与反馈态（finally 据此跳过）。
    let keepGate = false;
    try {
      const res = await source.compact(currentId);
      settled = true;
      cleanup();
      if (!res.compacted) {
        reportError(res.reason ?? "没有可压缩的历史");
        return;
      }
      await finishSuccess();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // 「请求超时」只会来自 call 的计时器（send 同步失败/未连接是别的文案）——
      // 它意味着请求确实发出去了，后端此刻没有 deadline、仍在跑完摘要。
      if (msg === "请求超时") {
        // 请求已发出、后端没有 deadline——大概率仍在跑完摘要。不定死失败：
        // 保留监听与重入闸到兜底窗口结束，chat.compacted 到达即认账成功。
        keepGate = true;
        tailTimer = setTimeout(() => {
          tailTimer = null;
          cleanup();
          inFlight.current = false;
          setCompacting(false);
          reportError("压缩等待超时，后端可能仍在进行；完成后压缩标记块会出现在时间线（可刷新会话确认）");
        }, COMPACT_TAIL_GRACE_MS);
        reportError("压缩仍在后台进行（大会话摘要耗时较长），完成后会自动出现在时间线");
        return;
      }
      settled = true;
      cleanup();
      reportError(`压缩失败: ${msg}`);
    } finally {
      if (!keepGate) {
        inFlight.current = false;
        setCompacting(false);
      }
    }
  }, [source, reportError, currentId, reloadHistory]);
  // Composer → ContextIndicator 的压缩入口（2026-12 渲染性能 Top1）：内联箭头
  // 每帧换身份会击穿 ContextIndicator 的 memo，用 useCallback 固定。
  const handleCompactClick = useCallback(() => { void handleCompact(); }, [handleCompact]);
  return { handleCompact, handleCompactClick, compacting };
}
