// 樱花frp 节点选择面板（创建公网隧道前的一步）。
//
// 为什么不直接创建：节点是**隧道级属性**（建完不能改），选错了只能删了重建；
// 而樱花 API 没有延迟字段、节点也不响应 ICMP，延迟只能自己测——所以把"选哪台"
// 摊开给用户看：所有节点 + 负载 + 实测延迟 + 推荐项。
//
// 交互约定：
//   · 列表先出、延迟异步填（壳侧并发 6 探测，每有结果推一次）——不阻塞用户；
//   · 推荐项 = 可用节点里延迟最低者（延迟相同比负载），默认选中；
//   · 不可用节点置灰 + 原因（离线 / 需更高等级 / 不可创建隧道 / 节点地址缺失）；
//   · 「自动选择（推荐）」= 走壳的原有逻辑（负载升序，不探测）——不想挑就一键建。
import { useEffect, useMemo, useRef, useState } from "react";
import type { SakuraLatencyEvent, SakuraNode, SakuraNodeList } from "../../agent/sakura";
import { useEnterRef } from "../../shared/anim";
import { latencyText, latencyTone, loadText, recommendNode } from "../../shared/sakura-nodes";

/** 列表本体（纯展示：取数与选中态在容器里）——静态渲染可测。 */
export function SakuraNodeList({
  nodes,
  selected,
  recommended,
  disabled,
  probing,
  onSelect,
}: {
  nodes: SakuraNode[];
  selected: number | "auto";
  recommended: SakuraNode | null;
  disabled: boolean;
  /** 延迟是否会随后补上（false = 没有探测，未知就显示「—」而不是「测试中…」）。 */
  probing: boolean;
  onSelect: (value: number | "auto") => void;
}) {
  return (
    <div className="conn-node-list" role="listbox" aria-label="可用节点">
      <button
        type="button"
        role="option"
        aria-selected={selected === "auto"}
        className={"conn-node-item auto" + (selected === "auto" ? " on" : "")}
        data-conn="node-auto"
        disabled={disabled}
        onClick={() => onSelect("auto")}
      >
        <span className="conn-node-main">
          <span className="conn-node-name">自动选择（推荐）</span>
          <span className="conn-node-host">按节点负载自动挑一台（不测延迟）</span>
        </span>
        <span className="conn-node-metrics" />
      </button>

      {nodes.map((n) => {
        const isRecommended = recommended?.id === n.id && n.usable;
        const on = selected === n.id;
        return (
          <button
            key={n.id}
            type="button"
            role="option"
            aria-selected={on}
            aria-disabled={!n.usable}
            className={"conn-node-item" + (on ? " on" : "") + (n.usable ? "" : " off")}
            data-conn="node-item"
            data-node-id={n.id}
            data-usable={n.usable ? "true" : "false"}
            disabled={disabled || !n.usable}
            title={n.usable ? undefined : (n.reason ?? "不可用")}
            onClick={() => onSelect(n.id)}
          >
            <span className="conn-node-main">
              <span className="conn-node-name">
                {n.name}
                {isRecommended && <span className="conn-node-tag">推荐</span>}
              </span>
              <span className="conn-node-host mono">{n.host || "—"}</span>
            </span>
            <span className="conn-node-metrics">
              {!n.usable ? (
                <span className="conn-node-reason">{n.reason ?? "不可用"}</span>
              ) : (
                <>
                  <span
                    className={"conn-node-latency" + (latencyTone(n.latency) ? ` tone-${latencyTone(n.latency)}` : "")}
                    data-conn="node-latency"
                    title={typeof n.latency === "number" ? `实测 TCP 连接延迟 ${latencyText(n.latency)}` : "正在测试延迟"}
                  >
                    {typeof n.latency === "number" ? latencyText(n.latency) : probing ? "测试中…" : "—"}
                  </span>
                  <span className="conn-node-load">负载 {loadText(n.load)}</span>
                </>
              )}
            </span>
          </button>
        );
      })}
    </div>
  );
}

export function SakuraNodePicker({
  busy,
  onCancel,
  onConfirm,
  listNodes,
  onLatency,
}: {
  /** 创建进行中的描述（非 null 时禁用按钮）。 */
  busy: string | null;
  onCancel: () => void;
  /** 确认创建：nodeId 为 null 表示「自动选择」。 */
  onConfirm: (nodeId: number | null) => void;
  listNodes: () => Promise<SakuraNodeList>;
  onLatency: (cb: (e: SakuraLatencyEvent) => void) => () => void;
}) {
  const [nodes, setNodes] = useState<SakuraNode[] | null>(null);
  const [groupLevel, setGroupLevel] = useState(0);
  const [probing, setProbing] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<number | "auto">("auto");
  const entered = useEnterRef<HTMLDivElement>();
  // 用户已经动过选择就不再自动改写（延迟陆续回来时推荐项会变，抢用户的选择很烦）
  const touched = useRef(false);

  useEffect(() => {
    let alive = true;
    const un = onLatency((e) => {
      if (!alive) return;
      // 同一 host 可能被多个节点共用：按 host 批量回填
      setNodes((list) => list && list.map((n) => (n.host === e.host ? { ...n, latency: e.latency } : n)));
    });
    listNodes()
      .then((res) => {
        if (!alive) return;
        setNodes(res.nodes);
        setGroupLevel(res.groupLevel);
        setProbing(res.probing);
      })
      .catch((e: unknown) => {
        if (alive) setError(e instanceof Error ? e.message : String(e));
      });
    return () => { alive = false; un(); };
  }, [listNodes, onLatency]);

  // 选中态跟随推荐：用户没手动选过时，延迟陆续回来（推荐项随之变化）要跟上；
  // 动过就完全交给用户，不再抢。
  useEffect(() => {
    if (!nodes || touched.current) return;
    setSelected(recommendNode(nodes)?.id ?? "auto");
  }, [nodes]);

  const usable = useMemo(() => (nodes ?? []).filter((n) => n.usable), [nodes]);
  const recommended = useMemo(() => recommendNode(nodes ?? []), [nodes]);
  const pending = useMemo(
    () => (probing ? usable.filter((n) => typeof n.latency !== "number").length : 0),
    [usable, probing],
  );
  const disabled = busy !== null;

  return (
    <div className="conn-node-picker" ref={entered} data-conn="node-picker">
      <div className="conn-node-head">
        <span className="conn-node-title">选择节点</span>
        <span className="conn-node-sub">
          {nodes === null
            ? "正在获取节点列表…"
            : pending > 0
              ? `正在测延迟（还剩 ${pending} 个）…`
              : `共 ${nodes.length} 个节点 · 你的等级 ${groupLevel}`}
        </span>
      </div>

      {error && <div className="conn-sakura-err" role="alert">{error}</div>}

      {nodes === null && !error && (
        <div className="conn-node-loading" role="status">
          <span className="conn-busy-spinner" aria-hidden="true" />
          正在获取节点列表…
        </div>
      )}

      {nodes !== null && (
        <SakuraNodeList
          nodes={nodes}
          selected={selected}
          recommended={recommended}
          disabled={disabled}
          probing={probing}
          onSelect={(value) => { touched.current = true; setSelected(value); }}
        />
      )}

      <div className="conn-node-foot">
        <button type="button" className="conn-copy" data-conn="node-cancel" disabled={disabled} onClick={onCancel}>
          取消
        </button>
        <button
          type="button"
          className="conn-add conn-node-create"
          data-conn="node-create"
          disabled={disabled || nodes === null || (selected !== "auto" && !nodes.some((n) => n.id === selected && n.usable))}
          onClick={() => onConfirm(selected === "auto" ? null : selected)}
        >
          {busy ?? (selected === "auto" ? "创建隧道（自动选择）" : "创建隧道")}
        </button>
      </div>
      <div className="conn-ra-hint">
        节点是隧道级属性，创建后不能更改（要换节点得删掉重建）。延迟是 TCP 连接实测值，仅供参考——节点不响应 ping。
      </div>
    </div>
  );
}
