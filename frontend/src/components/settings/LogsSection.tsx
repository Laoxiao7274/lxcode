// 日志分区（设置——桌面错误日志查看器，2026-12）：后端日志尾 + 操作错误历史。
//
// 两个数据源分工清楚：
// - 后端日志尾：logs.tail 拉取（懒——进入分区才拉第一次，之后手动刷新；
//   **刻意不轮询**，与 JobOutput 同一条纪律：用户点「刷新」就是显式意图）；
// - 操作错误历史：useAgent 的 operationErrors 环形缓冲（最近 50 条带时间戳）
//   经 props 传入——它是请求级失败的一次性提示之外留下的完整历史。
//
// 复制走 navigator.clipboard 的可选链 + 1.2s 反馈（CopyBtn/DiffBody 同款写法）。
import { useEffect, useState } from "react";
import type { LogsTailResult } from "../../shared/types";
import type { OperationErrorEntry } from "../../shared/store";
import { Button } from "../form";
import { Section } from "./rows";

export function LogsSection({ operationErrors, tail }: {
  operationErrors: OperationErrorEntry[];
  /** 拉日志尾（AgentSource.tail / logsAdmin.tail 已在调用方适配；缺省 = 不支持）。 */
  tail?: (bytes?: number) => Promise<LogsTailResult>;
}) {
  const [loaded, setLoaded] = useState<LogsTailResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const fetchTail = () => {
    if (!tail) {
      setError("当前数据源不支持查看后端日志（演示模式没有后端日志可看）");
      return;
    }
    setBusy(true);
    setError(null);
    tail()
      .then((r) => setLoaded(r))
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };
  // 进入分区才拉第一次（懒加载——设置页不是日志监控台，打开就想要最新状态）。
  useEffect(() => {
    fetchTail();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const copy = () => {
    if (!loaded) return;
    void navigator.clipboard?.writeText(loaded.content).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    });
  };

  return (
    <Section title="日志" desc="后端日志尾与操作错误历史——排查问题时看这里。日志只读当前文件（轮转的旧文件在日志目录里按 .1/.2/.3 排列）。">
      <div className="ls-block" data-testid="logs-backend">
        <div className="ls-block-head">
          <span className="ls-block-title">后端日志（尾部）</span>
          <span className="ls-block-actions">
            <Button variant="ghost" className="ls-btn" onClick={copy} disabled={!loaded || busy}>
              {copied ? "已复制" : "复制"}
            </Button>
            <Button variant="ghost" className="ls-btn" onClick={fetchTail} disabled={busy}>
              {busy ? "读取中…" : "刷新"}
            </Button>
          </span>
        </div>
        {loaded ? (
          <>
            {loaded.truncated && <div className="ls-note">日志过大，只显示尾部（完整文件在下方路径）</div>}
            <pre className="ls-log mono">{loaded.content || "（日志为空）"}</pre>
            {loaded.path && <div className="ls-path mono" title={loaded.path}>{loaded.path}</div>}
          </>
        ) : (
          <div className="ls-log-empty">{busy ? "读取中…" : error ?? "尚未加载"}</div>
        )}
        {error && loaded && <div className="ls-err">{error}</div>}
      </div>

      <div className="ls-block" data-testid="logs-operation-errors">
        <div className="ls-block-head">
          <span className="ls-block-title">操作错误（最近 50 条）</span>
        </div>
        {operationErrors.length === 0 ? (
          <div className="ls-log-empty">（没有操作错误记录）</div>
        ) : (
          <ul className="ls-err-list">
            {operationErrors.map((e, i) => (
              <li className="ls-err-item" key={i}>
                <span className="ls-err-at mono">{e.at.replace("T", " ").slice(0, 19)}</span>
                <span className="ls-err-msg">{e.message}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Section>
  );
}
