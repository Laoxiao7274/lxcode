// 管理站：会话门 + 路由（独立于官网的第二个站点）。
//
// 为什么独立：管理台是「少数人、低频、要看清」的界面，官网是「所有人、高频、要好看」的界面；
// 把两者塞在一个壳里，导航、密度、留白都会被对方拉平（用户反馈的「后台样式丑」就是这么来的）。
//
// 路由用 hash（见 shared/hash.ts）；会话用 HttpOnly cookie（见 shared/api/client.ts）：
// 未登录 → 整页登录页；已登录 → 管理台外壳 + 四个功能页。

import { useEffect, useState } from "react";

import { fetchSession, logout } from "../shared/api/client";
import { reload } from "../shared/store";
import { useHashPath } from "../shared/hash";
import AdminShell from "./AdminShell";
import LoginPage from "./LoginPage";
import Overview from "./pages/Overview";
import Releases from "./pages/Releases";
import ReleaseNew from "./pages/ReleaseNew";
import ChangelogEdit from "./pages/ChangelogEdit";

function route(path: string) {
  switch (path) {
    case "/":
      return <Overview />;
    case "/releases":
      return <Releases />;
    case "/releases/new":
      return <ReleaseNew />;
    case "/changelog":
      return <ChangelogEdit />;
    default:
      return (
        <div className="adm-page">
          <div className="panel panel-pad">
            <p className="small">没有这个页面：{path}</p>
            <p className="meta">管理台只有 概览 / 版本管理 / 上传新版本 / 更新日志 四个页面。</p>
          </div>
        </div>
      );
  }
}

export default function AdminApp() {
  const path = useHashPath();
  // null = 还在探测会话（避免先闪一下登录页再跳进管理台）
  const [authed, setAuthed] = useState<boolean | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const ok = await fetchSession();
        if (ok) await reload("admin");
        setAuthed(ok);
      } catch (error) {
        setFailure(error instanceof Error ? error.message : String(error));
        setAuthed(false);
      }
    })();
  }, []);

  function signOut() {
    void logout().finally(() => setAuthed(false));
  }

  if (authed === null) {
    return (
      <div className="adm-login">
        <p className="meta">正在检查会话…</p>
      </div>
    );
  }
  if (!authed) {
    return <LoginPage onSuccess={() => setAuthed(true)} initialError={failure} />;
  }
  return (
    <AdminShell path={path} onLogout={signOut}>
      {route(path)}
    </AdminShell>
  );
}
