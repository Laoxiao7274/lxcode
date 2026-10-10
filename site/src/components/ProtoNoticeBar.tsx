// 页面级唯一提示条：fixed 定位在视口底部居中。
//
// 关键点：**不占文档流**——所以出现/消失时页面总高度一点不变（上轮就地撑开会 +110px）。
// 常驻挂载、用 .on 切显隐，进出各带一段轻微过渡；reduced-motion 下由 base.css 全局归零。

import { useProtoNotice } from "./protoNotice";

export function ProtoNoticeBar() {
  const notice = useProtoNotice();
  return (
    <div className={notice ? "notice-bar on" : "notice-bar"} role="status" aria-live="polite">
      <span className="pill pill-warn">原型</span>
      <span className="meta notice-text">{notice?.text ?? ""}</span>
    </div>
  );
}
