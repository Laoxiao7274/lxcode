// 连接域的共享小组件：复制按钮（反馈到按钮文案——不弹 toast）。
import { useState } from "react";

export function CopyBtn({ value, label }: { value: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className="conn-copy"
      data-copied={done ? "true" : undefined}
      onClick={() => {
        void navigator.clipboard?.writeText(value).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1200);
        });
      }}
      title={`复制${label}`}
    >
      {done ? "已复制" : "复制"}
    </button>
  );
}
