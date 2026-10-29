// ProjectInstructionsDialog —— 项目守则编辑器（项目行 hover 的「守则」入口进入）。
//
// 这是「自定义指令」的项目级落点：内容写进项目根 AGENTS.md，后端**每轮组装提示词
// 时现读**（改了下一轮就生效，不用重启）；优先级低于 Agent 自身设定（段内显式声明）。
// 路径由服务端解析（只传项目 id）——渲染层不碰文件系统。
import { useEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../shared/motion";
import { useEscape } from "../../shared/popover";
import type { AgentSource, ProjectMeta } from "../../shared/types";
import { Button, Textarea } from "../form";

export function ProjectInstructionsDialog({
  source,
  project,
  onClose,
}: {
  source: AgentSource;
  project: ProjectMeta;
  onClose: () => void;
}) {
  const [path, setPath] = useState("");
  const [exists, setExists] = useState(false);
  const [note, setNote] = useState("");
  const [text, setText] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const dialogRef = useRef<HTMLDivElement>(null);

  useEscape(true, onClose);

  useEffect(() => {
    if (!dialogRef.current || !motionAllowed()) return;
    gsap.fromTo(dialogRef.current, { opacity: 0, y: 8 }, { opacity: 1, y: 0, duration: 0.2, ease: "power2.out", clearProps: "transform,opacity" });
  }, []);

  useEffect(() => {
    let alive = true;
    source
      .readInstructions(project.id)
      .then((r) => {
        if (!alive) return;
        setPath(r.path);
        setExists(r.exists);
        setNote(r.note ?? "");
        setText(r.content);
      })
      .catch((e) => alive && setError(e instanceof Error ? e.message : String(e)))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, [source, project.id]);

  const save = async () => {
    setSaving(true);
    setError("");
    try {
      await source.saveInstructions(project.id, text);
      setExists(true);
      setSaved(true);
      setTimeout(() => setSaved(false), 1600);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="proj-add-mask" role="dialog" aria-label="项目守则" aria-modal="true" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="proj-add proj-ins" ref={dialogRef}>
        <div className="proj-add-head">
          <span className="proj-add-title">项目守则 · {project.name}</span>
          <button type="button" className="proj-add-close" aria-label="关闭" onClick={onClose}>
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="proj-add-body">
          <div className="proj-ins-path" title={path}>
            {path || "（路径未知）"}
            <span className={"proj-ins-state" + (exists ? " on" : "")}>{exists ? "已存在" : "新建"}</span>
          </div>
          <div className="proj-field-hint">
            写进项目根的 AGENTS.md——该项目的每个会话**每轮现读**进提示词（改完下一轮生效，不用重启）；
            优先级低于 Agent 自身设定，与你的指令冲突时以你为准。
          </div>
          {note && <div className="proj-add-error" role="alert">{note}</div>}
          <Textarea
            value={text}
            rows={14}
            placeholder={"# 项目守则\n\n- 提交信息一律用中文\n- 改动前先读相关代码\n- 跑构建/测试才算完成"}
            spellCheck={false}
            ariaLabel="项目守则内容"
            onChange={(v) => setText(v)}
            disabled={loading}
          />
          {error && <div className="proj-add-error" role="alert">{error}</div>}
        </div>
        <div className="proj-add-foot">
          <span className="proj-ins-tip">{loading ? "读取中…" : saved ? "已保存——下一轮生效" : ""}</span>
          <button type="button" className="proj-add-cancel" onClick={onClose}>关闭</button>
          <Button variant="primary" data-ins="save" disabled={loading || saving} onClick={save}>
            {saving ? "保存中…" : "保存"}
          </Button>
        </div>
      </div>
    </div>
  );
}
