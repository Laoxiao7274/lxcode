// 后台版本管理：全部版本 + 发布 / 撤回（两段确认）。
//
// 数据来自后端：带管理 token 时 GET /api/admin/releases（含草稿），否则回落公开数据。
// 发布 / 撤回打管理接口，服务端是唯一事实源，改完重新拉一遍。
// 「撤回」做成行内两段确认（不弹 window.confirm）：确认动作留在页面上，看得见、可取消。

import { useState } from "react";

import { findArtifact, formatBytes, sortByVersionDesc } from "../../shared/release";
import { CHANNEL_PILL, STATUS_PILL, dateOf } from "../../shared/labels";
import { setReleaseStatus } from "../../shared/store";
import { useStore } from "../../shared/useStore";
import { AdminScopeNote, DataStatus } from "../../components/DataStatus";

export default function Releases() {
  const state = useStore();
  // 待确认撤回的版本号（行内两段确认；null = 没有待确认项）
  const [pendingRevoke, setPendingRevoke] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const ordered = sortByVersionDesc(state.releases);

  function change(version: string, status: "published" | "revoked") {
    setFailure(null);
    setPendingRevoke(null);
    void setReleaseStatus(version, status).catch((error: unknown) => {
      setFailure(error instanceof Error ? error.message : String(error));
    });
  }

  return (
    <div className="adm-page">
        <p className="adm-lede">草稿可以发布（进更新链），已发布可以撤回（从更新链摘掉但保留历史）。
            这两步只改内存态，刷新即回初始数据。</p>
        <DataStatus state={state} />
        <AdminScopeNote state={state} />
        {failure && <p className="err small">{failure}</p>}
        <div className="tbl-wrap">
          <table className="tbl">
            <thead>
              <tr>
                <th>版本</th>
                <th>渠道</th>
                <th>状态</th>
                <th>发布日期</th>
                <th>安装包</th>
                <th>更新包</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {ordered.map((release) => {
                const status = STATUS_PILL[release.status];
                const channel = CHANNEL_PILL[release.channel];
                const installer = findArtifact(release, "installer");
                const update = findArtifact(release, "update");
                return (
                  <tr key={release.version}>
                    <td className="mono">{release.version}</td>
                    <td>
                      <span className={channel.cls}>{channel.label}</span>
                    </td>
                    <td>
                      <span className={status.cls}>
                        <span className="pill-dot" aria-hidden="true" />
                        {status.label}
                      </span>
                    </td>
                    <td className="meta">{dateOf(release.publishedAt)}</td>
                    <td className="meta">{installer ? formatBytes(installer.size) : "—"}</td>
                    <td className="meta">{update ? formatBytes(update.size) : "—"}</td>
                    <td>
                      <div className="row-actions">
                        {release.status === "draft" && (
                          <button
                            className="btn btn-sm btn-p"
                            type="button"
                            onClick={() => change(release.version, "published")}
                          >
                            发布
                          </button>
                        )}
                        {release.status === "published" &&
                          (pendingRevoke === release.version ? (
                            <>
                              <span className="meta">确认从更新链摘掉？</span>
                              <button
                                className="btn btn-sm btn-danger"
                                type="button"
                                onClick={() => change(release.version, "revoked")}
                              >
                                确认撤回
                              </button>
                              <button
                                className="btn btn-sm btn-quiet"
                                type="button"
                                onClick={() => setPendingRevoke(null)}
                              >
                                取消
                              </button>
                            </>
                          ) : (
                            <button
                              className="btn btn-sm"
                              type="button"
                              onClick={() => setPendingRevoke(release.version)}
                            >
                              撤回
                            </button>
                          ))}
                        {release.status === "revoked" && (
                          <span className="meta">已从更新链摘掉，历史保留</span>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <div className="row-actions" style={{ marginTop: 18 }}>
          <a className="btn btn-p" href="#/releases/new">
            上传新版本
          </a>
          <span className="meta">共 {ordered.length} 个版本</span>
        </div>
    </div>
  );
}
