// 管理台外壳：左侧固定**深色侧栏** + 右侧内容区（粘性顶条 + 卡片化内容）。
//
// 与官网刻意分开：官网是「白底大字 + 营销留白」，管理台是「深侧栏 + 紧凑密度」——
// 两者共用品牌色与字体，但密度、层级与对比是管理台自己的（--adm-* token 见 admin.css）。
// 顶条常驻页标题 + 数据状态 + 刷新：管理台每一页都要能一眼看到「数据是不是最新的」。

import type { ReactNode } from "react";

import { useStore } from "../shared/useStore";
import { DataStatus } from "../components/DataStatus";
import { reload } from "../shared/store";

/** 管理台导航（顺序即工作流：先看概览，再管版本，然后上传、编辑日志）。 */
export const ADMIN_TABS = [
  { path: "/", label: "概览", title: "概览" },
  { path: "/releases", label: "版本管理", title: "版本管理" },
  { path: "/releases/new", label: "上传新版本", title: "上传新版本" },
  { path: "/changelog", label: "更新日志", title: "更新日志" },
];

export default function AdminShell({
  path,
  onLogout,
  children,
}: {
  path: string;
  onLogout: () => void;
  children: ReactNode;
}) {
  const state = useStore();
  const current = ADMIN_TABS.find((tab) => tab.path === path) ?? ADMIN_TABS[0];

  return (
    <div className="adm">
      <aside className="adm-side">
        <a className="adm-brand" href="#/">
          <span className="adm-brand-mark" aria-hidden="true">
            LX
          </span>
          <span className="adm-brand-text">
            <span className="adm-brand-name">LXCode</span>
            <span className="adm-brand-sub">管理台</span>
          </span>
        </a>
        <nav className="adm-nav" aria-label="管理台导航">
          {ADMIN_TABS.map((tab) => (
            <a
              key={tab.path}
              className={tab.path === path ? "adm-link on" : "adm-link"}
              href={`#${tab.path}`}
              aria-current={tab.path === path ? "page" : undefined}
            >
              {tab.label}
            </a>
          ))}
        </nav>
        <div className="adm-side-foot">
          <button className="adm-logout" type="button" onClick={onLogout}>
            退出登录
          </button>
          <span className="adm-side-note">会话 24 小时 · 重启后端需重登</span>
        </div>
      </aside>

      <div className="adm-body">
        <header className="adm-top">
          <h1 className="adm-title">{current.title}</h1>
          <div className="adm-top-actions">
            <DataStatus state={state} />
            <button className="adm-refresh" type="button" onClick={() => void reload("admin")}>
              刷新
            </button>
          </div>
        </header>
        <div className="adm-content">{children}</div>
      </div>
    </div>
  );
}
