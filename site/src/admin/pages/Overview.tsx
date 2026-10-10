// 后台概览：版本数 / 最新 stable / 草稿数 / 已撤回数 + 发布口径 + 最近版本。
//
// 数字全部由后端数据算出来（不是写死的），因此「发布 / 撤回」之后回到本页数字会变。

import {
  REQUIRED_UPDATE_FILES,
  formatBytes,
  findArtifact,
  latestPublished,
  sortByVersionDesc,
  updatePackageName,
} from "../../shared/release";
import { CHANNEL_PILL, STATUS_PILL, dateOf } from "../../shared/labels";
import { useStore } from "../../shared/useStore";
import { AdminScopeNote, DataStatus } from "../../components/DataStatus";

export default function Overview() {
  const state = useStore();
  const { releases, changelog } = state;
  const latest = latestPublished(releases, "stable");
  const recent = sortByVersionDesc(releases).slice(0, 3);
  const update = latest ? findArtifact(latest, "update") : null;

  const numbers = [
    { key: "版本总数", value: String(releases.length) },
    { key: "最新 stable", value: latest ? latest.version : "—" },
    { key: "草稿", value: String(releases.filter((r) => r.status === "draft").length) },
    { key: "已撤回", value: String(releases.filter((r) => r.status === "revoked").length) },
  ];

  return (
    <div className="adm-page">
        <p className="adm-lede">版本列表与更新日志来自后端接口；「版本管理」里发布或撤回后，这里的数字立刻跟着变。</p>

        <DataStatus state={state} />
        <AdminScopeNote state={state} />

        <div className="adm-nums">
          {numbers.map((n) => (
            <div className="adm-num" key={n.key}>
              <div className="adm-num-v">{n.value}</div>
              <div className="adm-num-k meta">{n.key}</div>
            </div>
          ))}
        </div>

        <div className="panel" style={{ marginTop: 22 }}>
          <div className="panel-head">
            <span className="panel-title">发布口径</span>
            <span className="pill">后端实时数据</span>
          </div>
          <div className="panel-pad">
            <div className="spec">
              <div className="spec-row">
                <span className="spec-k">版本唯一源</span>
                <span className="spec-v">
                  <span className="mono">shell/package.json（版本 / 二进制烙印 / 更新清单三方同源）</span>
                </span>
              </div>
              <div className="spec-row">
                <span className="spec-k">更新包命名</span>
                <span className="spec-v">
                  <span className="mono">
                    {latest ? updatePackageName(latest.version) : "update-<版本>.zip（发布后按版本号命名）"}
                  </span>
                </span>
              </div>
              <div className="spec-row">
                <span className="spec-k">更新包内文件</span>
                <span className="spec-v">
                  <span className="mono">{REQUIRED_UPDATE_FILES.join(" · ")}</span>
                </span>
              </div>
              <div className="spec-row">
                <span className="spec-k">最新更新包体积</span>
                <span className="spec-v">更新包 {update ? formatBytes(update.size) : "—"}</span>
              </div>
              <div className="spec-row">
                <span className="spec-k">下载计数</span>
                <span className="spec-v">
                  {/* 真实计数：后端按产物 GET 计数（包含我自己的验收下载） */}
                  {latest ? `${latest.downloads} 次（按产物下载计数）` : "—"}
                </span>
              </div>
              <div className="spec-row">
                <span className="spec-k">日志条目</span>
                <span className="spec-v">共 {changelog.length} 条</span>
              </div>
            </div>
          </div>
        </div>

        <div className="panel" style={{ marginTop: 22 }}>
          <div className="panel-head">
            <span className="panel-title">最近三个版本</span>
            <a className="btn btn-sm" href="#/releases">
              去版本管理
            </a>
          </div>
          <div className="panel-pad">
            <div className="tbl-wrap">
              <table className="tbl">
                <thead>
                  <tr>
                    <th>版本</th>
                    <th>渠道</th>
                    <th>状态</th>
                    <th>发布日期</th>
                    <th>更新包</th>
                  </tr>
                </thead>
                <tbody>
                  {recent.map((release) => {
                    const status = STATUS_PILL[release.status];
                    const channel = CHANNEL_PILL[release.channel];
                    const updateArtifact = findArtifact(release, "update");
                    return (
                      <tr key={release.version}>
                        <td className="mono">{release.version}</td>
                        <td>
                          <span className={channel.cls}>{channel.label}</span>
                        </td>
                        <td>
                          <span className={status.cls}>{status.label}</span>
                        </td>
                        <td className="meta">{dateOf(release.publishedAt)}</td>
                        <td className="meta">
                          {updateArtifact ? formatBytes(updateArtifact.size) : "—"}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        </div>

        <div className="row-actions" style={{ marginTop: 22 }}>
          <a className="btn btn-p" href="#/releases/new">
            上传新版本
          </a>
          <a className="btn" href="#/changelog">
            编辑更新日志
          </a>
        </div>
    </div>
  );
}
