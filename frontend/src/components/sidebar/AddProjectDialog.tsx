// AddProjectDialog —— 添加项目对话框（侧栏「项目」区 + 号进入）：
// 项目名 + 目录路径（壳内可浏览选择，浏览器模式手输）。
// 添加规则在后端：目录已是 git 仓库 → 直接注册；不是 → git init。
import { useEffect, useRef, useState } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../shared/motion";
import { useEscape } from "../../shared/popover";
import { TextInput } from "../form";

/** 壳桥（preload 注入；浏览器模式无）。 */
const shell = (window as unknown as {
  __LX__?: { selectDirectory: () => Promise<string | null> };
}).__LX__;

/** 路径末段作默认项目名（C:\x\my\lxcode → lxcode）。 */
const basename = (p: string) => {
  const t = p.replace(/[\\/]+$/, "");
  const i = Math.max(t.lastIndexOf("\\"), t.lastIndexOf("/"));
  return i >= 0 ? t.slice(i + 1) : t;
};

export function AddProjectDialog({
  onAdd,
  onClose,
}: {
  onAdd: (name: string, path: string) => void;
  onClose: () => void;
}) {
  const [path, setPath] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const dialogRef = useRef<HTMLDivElement>(null);
  const nameRef = useRef<HTMLInputElement>(null);

  useEscape(true, onClose);

  // 入场动画（mset-connect 同款：淡入 + 轻微上移）
  useEffect(() => {
    if (!dialogRef.current || !motionAllowed()) return;
    gsap.fromTo(dialogRef.current, { opacity: 0, y: 8 }, { opacity: 1, y: 0, duration: 0.2, ease: "power2.out", clearProps: "transform,opacity" });
    nameRef.current?.focus();
  }, []);

  const pickPath = async () => {
    const dir = await shell?.selectDirectory();
    if (!dir) return;
    setPath(dir);
    if (!name.trim()) setName(basename(dir));
  };

  const submit = () => {
    const n = name.trim();
    if (!path.trim()) {
      setError("请填写项目目录");
      return;
    }
    if (!n) {
      setError("请填写项目名");
      return;
    }
    onAdd(n, path.trim());
    onClose();
  };

  return (
    <div className="proj-add-mask" role="dialog" aria-label="添加项目" aria-modal="true" onPointerDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="proj-add" ref={dialogRef}>
        <div className="proj-add-head">
          <span className="proj-add-title">添加项目</span>
          <button type="button" className="proj-add-close" aria-label="关闭" onClick={onClose}>
            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="proj-add-body">
          <label className="proj-field">
            <span className="proj-field-label">目录</span>
            <div className="proj-path-row">
              <TextInput
                value={path}
                placeholder="D:\path\to\repo"
                spellCheck={false}
                onChange={(v) => {
                  setPath(v);
                  if (!name.trim()) setName(basename(v));
                }}
                onKeyDown={(e) => e.key === "Enter" && submit()}
              />
              {shell && (
                <button type="button" className="proj-browse" onClick={pickPath}>
                  浏览…
                </button>
              )}
            </div>
            <span className="proj-field-hint">已是 git 仓库则直接接入；否则自动 git init</span>
          </label>
          <label className="proj-field">
            <span className="proj-field-label">项目名</span>
            <TextInput
              inputRef={nameRef}
              value={name}
              placeholder="从目录名自动带出"
              spellCheck={false}
              onChange={setName}
              onKeyDown={(e) => e.key === "Enter" && submit()}
            />
          </label>
          {error && (
            <div className="proj-add-error" role="alert">{error}</div>
          )}
        </div>
        <div className="proj-add-foot">
          <button type="button" className="proj-add-cancel" onClick={onClose}>取消</button>
          <button type="button" className="proj-add-ok" onClick={submit}>添加</button>
        </div>
      </div>
    </div>
  );
}
