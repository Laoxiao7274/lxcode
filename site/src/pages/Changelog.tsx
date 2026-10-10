// 更新日志页：每版本一张卡 + 渠道过滤 + 该版本的下载入口。
//
// 数据来自**后端**（state.ts → GET /api/releases?include=revoked 与 GET /api/changelog）：
// 公开页只呈现已发布过的版本（草稿不出现），已撤回版本如实标注、不提供下载。
// 分类的标签与配色来自 labels.ts（与后台编辑页共用一份）。
//
// 渠道过滤仍是纯 UI 状态（useState），不动数据。

import { useState } from "react";

import { ArtifactLink } from "../components/ArtifactLink";
import { DataStatus } from "../components/DataStatus";
import { HashLine } from "../components/HashLine";
import {
  findArtifact,
  formatBytes,
  sortByVersionDesc,
  type Release,
} from "../shared/release";
import { CHANNEL_PILL, KIND_TAG, STATUS_PILL, dateOf, entriesByKind } from "../shared/labels";
import type { ChangelogEntry } from "../shared/release";
import { useStore } from "../shared/useStore";
import "../styles/page-site.css";

/** 渠道过滤：全部 / stable / beta（纯 UI 状态，不改数据）。 */
const FILTERS = [
  { key: "all", label: "全部" },
  { key: "stable", label: "stable" },
  { key: "beta", label: "beta" },
] as const;
type FilterKey = (typeof FILTERS)[number]["key"];

function releasesOf(all: Release[], key: FilterKey): Release[] {
  return key === "all" ? all : all.filter((r) => r.channel === key);
}

/** 一张版本卡：头（版本/渠道/状态/日期）→ 身（更新提示 + 分组条目）→ 脚（下载动作 + 常驻哈希行）。 */
function VersionCard({ release, entries }: { release: Release; entries: ChangelogEntry[] }) {
  const installer = findArtifact(release, "installer");
  const update = findArtifact(release, "update");
  const groups = entriesByKind(entries.filter((e) => e.version === release.version));
  const channel = CHANNEL_PILL[release.channel];
  const status = STATUS_PILL[release.status];
  const revoked = release.status === "revoked";

  return (
    <article className="panel log-card">
      <header className="log-card-head">
        <div className="log-card-id">
          <span className="log-ver">{release.version}</span>
          <span className={channel.cls}>{channel.label}</span>
          <span className={status.cls}>
            <span className="pill-dot" aria-hidden="true" />
            {status.label}
          </span>
        </div>
        <span className="meta">{dateOf(release.publishedAt)} 发布</span>
      </header>

      <div className="log-card-body">
        {release.notes.length > 0 && (
          <div className="log-group">
            <p className="log-group-k">
              <span className="tag">更新提示</span>
              <span className="meta">客户端更新提示里展示的要点</span>
            </p>
            <ul className="log-items">
              {release.notes.map((note) => (
                <li className="log-item" key={note}>
                  {note}
                </li>
              ))}
            </ul>
          </div>
        )}

        {groups.map((group) => {
          const tag = KIND_TAG[group.kind];
          return (
            <div className="log-group" key={group.kind}>
              <p className="log-group-k">
                <span className={tag.cls}>{tag.label}</span>
              </p>
              <ul className="log-items">
                {group.items.map((entry) => (
                  <li
                    className={group.kind === "breaking" ? "log-item log-item-hot" : "log-item"}
                    key={entry.id}
                  >
                    {entry.text}
                  </li>
                ))}
              </ul>
            </div>
          );
        })}

        {release.notes.length === 0 && groups.length === 0 && (
          <p className="meta">这个版本没有单独的日志条目。</p>
        )}
      </div>

      <footer className="log-card-foot">
        {revoked ? (
          <p className="log-revoked">
            <span className="pill pill-danger">
              <span className="pill-dot" aria-hidden="true" />
              已撤回 · 不提供下载
            </span>
            <span className="meta">已从更新链摘掉，只保留历史记录</span>
          </p>
        ) : (
          <>
            <div className="log-actions">
              {installer && (
                <ArtifactLink artifact={installer} className="btn btn-p">
                  下载安装包
                </ArtifactLink>
              )}
              {update && (
                <ArtifactLink artifact={update} className="btn">
                  下载更新包
                </ArtifactLink>
              )}
              <span className="meta">
                {installer ? `安装包 ${formatBytes(installer.size)}` : "无安装包"}
                {update ? ` · 更新包 ${formatBytes(update.size)}` : ""}
              </span>
            </div>
            <div className="log-hashes">
              {installer && <HashLine label="安装包" hash={installer.sha256} />}
              {update && <HashLine label="更新包" hash={update.sha256} />}
            </div>
          </>
        )}
      </footer>
    </article>
  );
}

export default function Changelog() {
  const state = useStore();
  const [filter, setFilter] = useState<FilterKey>("all");
  // 公开页只呈现已发布过的版本：草稿没发布过（与下载页同一口径）
  const publicReleases = sortByVersionDesc(state.releases).filter((r) => r.status !== "draft");
  const releases = releasesOf(publicReleases, filter);

  return (
    <section className="sec">
      <div className="wrap">
        <div className="sec-head">
          <p className="eyebrow">更新日志</p>
          <h1 style={{ fontSize: "var(--fs-h1)" }}>每个版本改了什么</h1>
          <p className="sec-lede">
            一个版本一张卡：改了什么、能不能升级、下载哪个包都在卡里。
            破坏性变更排在最前，升级前先看它。
          </p>
        </div>

        <DataStatus state={state} />

        <div className="log-filter">
          <span className="meta">渠道</span>
          {FILTERS.map((item) => (
            <button
              key={item.key}
              className="pill"
              type="button"
              aria-pressed={item.key === filter}
              onClick={() => setFilter(item.key)}
            >
              {item.label} {releasesOf(publicReleases, item.key).length}
            </button>
          ))}
          <span className="meta">
            共 {releases.length} 个版本 · 草稿不进公开页
          </span>
        </div>

        <div className="log-list">
          {state.status === "ready" && releases.length === 0 ? (
            <div className="panel panel-pad log-empty">
              <p className="small">
                {filter} 渠道暂无已发布版本：这个渠道下的版本还在草稿里，发布后才会出现在这里。
              </p>
              <p className="meta">切到「全部」可以看到其它渠道的历史版本。</p>
            </div>
          ) : (
            releases.map((release) => (
              <VersionCard release={release} entries={state.changelog} key={release.version} />
            ))
          )}
        </div>
      </div>
    </section>
  );
}
