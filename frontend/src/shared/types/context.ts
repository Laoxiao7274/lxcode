// 上下文计账与会话统计（对齐 protocol.ContextUsage / protocol.SessionStats）。

/** 上下文占用（对齐 protocol.ContextUsage）：used/window 是压力与环形依据
 *  （used 优先真实 prompt 总量），五个分类是估算拆分（已归一：分类之和 == used）。 */
export interface ContextUsage {
  used: number;
  /** 模型窗口上限（0/缺省 = 未知——不画环形百分比）。 */
  window?: number;
  /** 系统提示词。 */
  system?: number;
  /** 工具声明（wire 上的 JSON Schema）——与 system 分开一类，因为"工具占了窗口多少"
   *  是用户最想知道的其中一件事（DSH 的 ContextMeter 同样单列）。 */
  tools?: number;
  tool_results?: number;
  messages?: number;
  reasoning?: number;
  /** used 是**估算值**（后端按固定密度折算）：端点没回报用量，或库里没有真实测量、
   *  按已加载的历史回落（老会话/后端重启前跑过的会话）。UI 必须标注出来——用户
   *  看不出区别就会拿它做预算判断。缺省/false = 真实用量（provider 回报的
   *  prompt_tokens）。 */
  estimated?: boolean;
}

/** 整段会话的统计（对齐 protocol.SessionStats / DSH 的 sessionStats + tokenUsage）。
 *
 *  与 ContextUsage 的分工：context 回答「此刻窗口里有多少」，stats 回答「这条会话
 *  一共花了多少」。它折叠的是**整段日志**（含被压缩检查点影子掉的消息），所以压缩与
 *  翻页都改不了这些数字；撤回真删了行，数字跟着变小才是对的。
 *
 *  整键缺席 = 还没有任何一步（新会话/纯内存模式）——UI 不渲染统计胶囊，
 *  **不显示一排 0**（那是个假事实）。 */
export interface SessionStats {
  /** 轮数（用户发起的轮数，与右栏「轮次」面板同一口径）与步数（模型调用次数）。 */
  turns: number;
  steps: number;
  /** 墙钟（毫秒）：llm_ms = 各步请求耗时之和；tool_ms = 工具执行耗时之和。 */
  llm_ms: number;
  tool_ms: number;
  /** 首字：ttft_ms / ttft_steps = 均值（只有有首字可测的步才计入）。 */
  ttft_ms: number;
  ttft_steps: number;
  /** 解码：decode_ms = 首字 → 收尾的纯生成耗时，decode_tokens = 同期输出 token。
   *  生成速度 = decode_tokens / decode_ms（扣掉 prefill 才是"吐字速度"）。 */
  decode_ms: number;
  decode_tokens: number;
  /** 计费四桶（provider 回报；未回报 = 0）：未缓存输入 / 缓存读 / 缓存写 / 输出。 */
  input_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  output_tokens: number;
  /** 早期记录（本功能上线前落库的消息）的 token 之和：那时的口径是 provider 的
   *  total_tokens（输入+输出），与上面四桶不同——**单独给出、不混算**。
   *  缺席（0）= 这条会话没有早期记录。UI 在明细里如实说明它是什么，不参与速度。 */
  legacy_tokens?: number;
}
