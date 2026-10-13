// 手动压缩（chat.compact）：请求类失败进一次性提示（不动 blocks）；没有可压
// 收益时给一句人话原因（不是错误——历史还太短是正常态）。压缩成功由
// chat.compacted 事件渲染标记块。
// 审查 M5 前端重构：从 App.tsx 纯移动，语义零改动。
import { useCallback } from "react";
import type { AgentSource } from "../shared/types";

export function useCompaction({ source, currentId, reportError }: {
  source: AgentSource;
  currentId: string;
  reportError: (message: string) => void;
}) {
  const handleCompact = useCallback(async () => {
    try {
      const res = await source.compact(currentId);
      if (!res.compacted) reportError(res.reason ?? "没有可压缩的历史");
    } catch (e) {
      reportError(`压缩失败: ${e instanceof Error ? e.message : String(e)}`);
    }
  }, [source, reportError, currentId]);
  // Composer → ContextIndicator 的压缩入口（2026-12 渲染性能 Top1）：内联箭头
  // 每帧换身份会击穿 ContextIndicator 的 memo，用 useCallback 固定。
  const handleCompactClick = useCallback(() => { void handleCompact(); }, [handleCompact]);
  return { handleCompact, handleCompactClick };
}
