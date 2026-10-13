/** 正文输出：DSH 同款打字机缓冲（useStreamReveal）——新到字符按 0.35s
 *  浇注窗口匀速流出（30~1000 字/秒自适应积压），done 后缓冲继续排空。
 *  页面隐藏（document.hidden）期间冻结 shown、恢复可见统一落账（大积压
 *  整块落下）——hidden/恢复纪律见 shared/stream-reveal.ts 头注。
 *
 *  两段渲染（2026-12 渲染性能 Top2）：shown 每帧推进，整段走 Markdown 解析
 *  每帧成本 O(全文)。这里把 shown 切成「已定稿前缀 committed + 尾段 tail」
 *  （splitCommitted 纯函数：候选切点在倒数第 2 行之前，回退避开未闭合的
 *  围栏/列表/引用/表格，保证两段各自解析出的块序列与整段解析逐块一致）——
 *  committed 只在整行完成时才前进，TextBlocks 的 memo 按字符串比较恒命中
 *  （零解析零协调）；每帧真正重解析的只有 tail（≤2 行）。hidden hold 冻结
 *  shown → committed 一并冻结；恢复 snap 一次落全量，两段渲染天然兼容。 */
import { useMemo } from "react";
import { TextResponse } from "../../../aicss/TextResponse";
import { splitCommitted, useStreamReveal } from "../../../shared/stream-reveal";
import { Markdown } from "../../../shared/markdown";

export function AnswerBody({ text, streaming }: { text: string; streaming: boolean }) {
  const shown = useStreamReveal(text);
  const { committed, tail } = useMemo(() => splitCommitted(shown), [shown]);
  return (
    <TextResponse>
      {/* keyPrefix 区分两段的段序号（撞 key 会让 React 复用错段）；committed 为
          空串时第一段渲染 null——短文本（≤2 行）就是单段形态，与原行为一致 */}
      <Markdown text={committed} keyPrefix="c" />
      <Markdown text={tail} keyPrefix="t" />
      {streaming && <span className="md-caret" aria-hidden />}
    </TextResponse>
  );
}
