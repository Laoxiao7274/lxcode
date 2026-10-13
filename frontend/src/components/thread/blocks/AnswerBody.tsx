/** 正文输出：DSH 同款打字机缓冲（useStreamReveal）——新到字符按 0.35s
 *  浇注窗口匀速流出（30~1000 字/秒自适应积压），done 后缓冲继续排空。
 *  页面隐藏（document.hidden）期间冻结 shown、恢复可见统一落账（大积压
 *  整块落下）——hidden/恢复纪律见 shared/stream-reveal.ts 头注。
 *  揭示前缀实时走完整 markdown（shared/markdown）：标题/表格/列表/行内
 *  语法逐块淡入，逐行到达即渲染（不等闭合——见 markdown.tsx 流式纪律）。 */
import { TextResponse } from "../../../aicss/TextResponse";
import { useStreamReveal } from "../../../shared/stream-reveal";
import { Markdown } from "../../../shared/markdown";

export function AnswerBody({ text, streaming }: { text: string; streaming: boolean }) {
  const shown = useStreamReveal(text);
  return (
    <TextResponse>
      <Markdown text={shown} />
      {streaming && <span className="md-caret" aria-hidden />}
    </TextResponse>
  );
}
