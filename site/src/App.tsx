// 官网外壳 + 哈希路由（只有公开页面）。
//
// 为什么不引路由库：官网是静态托管、只有 3 个公开页面、没有嵌套布局与数据加载器，
// 而 hash 路由在静态目录下不需要服务端回退（`#/download` 永远不会打到服务端）。
// 路由状态用 useSyncExternalStore 订阅 hashchange（见 shared/hash.ts，与管理站共用一套）。
//
// **管理台已经拆成独立站点**（admin.html，见 src/admin/）：
// 官网导航里不再挂后台标签，只在页脚留一个低调的「管理」链接指向 /admin/。

import { useState } from "react";

import Home from "./pages/Home";
import Download from "./pages/Download";
import ChangelogPage from "./pages/Changelog";
import NotFound from "./pages/NotFound";
import { ProtoNoticeBar } from "./components/ProtoNoticeBar";
import { SITE_BASE } from "./shared/base";
import { useHashPath } from "./shared/hash";
import { latestPublished } from "./shared/release";
import { useStore } from "./shared/useStore";

/** 主导航：path 即 hash 路径（`#/download` → `/download`）。 */
const NAV = [
  { path: "/", label: "首页" },
  { path: "/download", label: "下载" },
  { path: "/changelog", label: "更新日志" },
] as const;

/** 导航高亮：取匹配最长的那一项（`/download` 不该把「首页」也点亮）。 */
function activeNav(path: string): string {
  const matched = NAV.filter((item) =>
    item.path === "/" ? path === "/" : path === item.path || path.startsWith(`${item.path}/`),
  );
  return matched.length > 0 ? matched[matched.length - 1].path : "";
}

function route(path: string) {
  switch (path) {
    case "/":
      return <Home />;
    case "/download":
      return <Download />;
    case "/changelog":
      return <ChangelogPage />;
    default:
      return <NotFound path={path} />;
  }
}

export default function App() {
  const path = useHashPath();
  const active = activeNav(path);
  // 页脚版本号取后端最新已发布版本（不写死；没有版本时整块不渲染）
  const latest = latestPublished(useStore().releases, "stable");
  // 窄屏抽屉：≤860px 时主导航收进抽屉（CSS 里 .nav-burger/.nav-drawer 早已备好，
  // 但一直没有 DOM——窄屏下等于没有导航）。这是纯 UI 状态，不碰数据逻辑。
  const [drawerOpen, setDrawerOpen] = useState(false);

  return (
    <>
      <a className="skip-link" href="#main">
        跳到正文
      </a>
      <header className="nav">
        <div className="wrap nav-in">
          <a className="brand" href="#/">
            <span className="brand-mark" aria-hidden="true">
              LX
            </span>
            <span className="brand-name">LXCode</span>
            <span className="brand-ver">官网</span>
          </a>
          <nav className="nav-links" aria-label="主导航">
            {NAV.map((item) => (
              <a
                key={item.path}
                className={item.path === active ? "nav-link on" : "nav-link"}
                href={`#${item.path}`}
                aria-current={item.path === active ? "page" : undefined}
              >
                {item.label}
              </a>
            ))}
            <a className="btn btn-p btn-sm nav-cta" href="#/download">
              免费下载
            </a>
          </nav>
          <button
            className="nav-burger"
            type="button"
            aria-expanded={drawerOpen}
            aria-label={drawerOpen ? "收起导航" : "展开导航"}
            onClick={() => setDrawerOpen(!drawerOpen)}
          >
            <span aria-hidden="true">{drawerOpen ? "✕" : "☰"}</span>
          </button>
        </div>
        {drawerOpen && (
          <div className="nav-drawer open">
            {NAV.map((item) => (
              <a
                key={item.path}
                className={item.path === active ? "nav-link on" : "nav-link"}
                href={`#${item.path}`}
                onClick={() => setDrawerOpen(false)}
              >
                {item.label}
              </a>
            ))}
          </div>
        )}
      </header>
      <main id="main">{route(path)}</main>
      {/* 页面级唯一提示条：fixed 不占文档流 → 出现/消失都不改页面高度 */}
      <ProtoNoticeBar />
      <footer className="foot">
        <div className="wrap">
          <div className="foot-grid">
            <div className="foot-brand">
              <a className="brand" href="#/">
                <span className="brand-mark" aria-hidden="true">
                  LX
                </span>
                <span className="brand-name">LXCode</span>
              </a>
              <p>
                跑在自己机器上的个人智能体：编程助手与通用助理合一，会话、密钥与文件都留在本机。
              </p>
              <span className="meta">Windows 10/11 · x64</span>
            </div>
            <div className="foot-col">
              <h3>产品</h3>
              <ul>
                <li>
                  <a href="#/">首页</a>
                </li>
                <li>
                  <a href="#/download">下载</a>
                </li>
                <li>
                  <a href="#/changelog">更新日志</a>
                </li>
              </ul>
            </div>
            <div className="foot-col">
              <h3>其它</h3>
              <ul>
                <li>
                  {/* 管理台是独立站点（admin.html）：官网只在页脚留一个低调入口 */}
                  <a href={`${SITE_BASE}/admin/`}>管理</a>
                </li>
                <li>
                  <a href="#/download">系统要求</a>
                </li>
              </ul>
            </div>
          </div>
          <div className="foot-note">
            <span>© 2026 LXCode · 官网（数据来自后端 site-backend）</span>
            {latest && <span className="mono">v{latest.version}</span>}
          </div>
        </div>
      </footer>
    </>
  );
}
