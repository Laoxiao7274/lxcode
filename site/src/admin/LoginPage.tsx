// 管理站登录页：整页居中卡片（未登录时整页显示，不套管理台外壳）。
//
// 为什么要有登录：上轮是「导航里挂一个后台标签 + 手填长期 token 存 localStorage」——
// 既丑（管理台混在官网里）又不安全（长期凭据存在 JS 能读的地方）。现在是会话 cookie。

import { useState, type FormEvent } from "react";

import { ApiError, login } from "../shared/api/client";

export default function LoginPage({
  onSuccess,
  initialError,
}: {
  onSuccess: () => void;
  /** 会话探测失败时的说明（例如后端没配凭据 / 连不上）。 */
  initialError?: string | null;
}) {
  const [password, setPassword] = useState("");
  const [visible, setVisible] = useState(false);
  const [error, setError] = useState<string | null>(initialError ?? null);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (password === "" || busy) return;
    setBusy(true);
    setError(null);
    try {
      await login(password);
      setPassword("");
      onSuccess();
    } catch (cause) {
      if (cause instanceof ApiError) {
        if (cause.status === 401) setError("密码错误");
        else if (cause.status === 429) setError("尝试过多，稍后再试");
        else setError(cause.message);
      } else {
        setError(String(cause));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="adm-login">
      <form className="adm-login-card" onSubmit={submit}>
        <div className="adm-login-head">
          <span className="adm-login-mark" aria-hidden="true">
            LX
          </span>
          <div>
            <p className="adm-login-title">LXCode 管理台</p>
            <p className="meta">版本发布 · 产物上传 · 更新日志</p>
          </div>
        </div>

        <label className="adm-login-field">
          <span className="lbl">管理密码</span>
          <span className="adm-login-input">
            <input
              className="inp"
              type={visible ? "text" : "password"}
              value={password}
              autoFocus
              autoComplete="current-password"
              aria-label="管理密码"
              onChange={(e) => setPassword(e.target.value)}
            />
            <button
              className="btn btn-quiet btn-sm"
              type="button"
              aria-pressed={visible}
              onClick={() => setVisible(!visible)}
            >
              {visible ? "隐藏" : "显示"}
            </button>
          </span>
        </label>

        {error && (
          <p className="err small" role="alert">
            {error}
          </p>
        )}

        <button className="btn btn-p btn-block" type="submit" disabled={busy || password === ""}>
          {busy ? "登录中…" : "登录"}
        </button>

        <p className="meta adm-login-note">
          密码 = 后端启动时配的凭据（--token / LXCODE_SITE_TOKEN）；登录后发 HttpOnly 会话 cookie，24 小时有效。
        </p>
      </form>
    </div>
  );
}
