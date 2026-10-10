// 下载页：三类产物 + 更新语义 + 历史版本（含已撤回版本的如实标注）。
//
// 三块不是三种格式，而是三条更新路径（见 release.ts 的 ArtifactKind 注释）：
// 安装包 = 唯一能带 Electron 升级的全量包；免安装 = 绿色分发；更新包 = 两级替换的增量包。
// 数据全部来自后端；撤回版本保留在列表里但**不提供下载链接**（如实标注，不假装它不存在）。
// 产物链接走 ArtifactLink：原型阶段 /releases/<文件名> 没有真实文件，点击不导航、只出提示条（页面级单条 fixed）。
// sha256 常驻哈希行（HashLine）：不折叠 → 三块卡等高、高度恒定。

import {
  REQUIRED_UPDATE_FILES,
  findArtifact,
  formatBytes,
  latestPublished,
  sortByVersionDesc,
  type Artifact,
  type Release,
} from "../shared/release";
import { CHANNEL_PILL, STATUS_PILL, dateOf } from "../shared/labels";
import { useStore } from "../shared/useStore";
import { DataStatus } from "../components/DataStatus";
import { ArtifactLink } from "../components/ArtifactLink";
import { HashLine } from "../components/HashLine";
import "../styles/page-site.css";

const KINDS: Array<{
  kind: Artifact["kind"];
  title: string;
  /** 哈希行的标签（比 title 短，哈希行要一眼扫得完）。 */
  short: string;
  desc: string;
  cta: string;
}> = [
  {
    kind: "installer",
    title: "安装包（推荐）",
    short: "安装包",
    desc: "NSIS 一键安装、per-user、免管理员。唯一能升级 Electron 的包，首次安装走它。",
    cta: "下载安装包",
  },
  {
    kind: "portable",
    title: "免安装版",
    short: "免安装版",
    desc: "解压即用的全量包，适合绿色分发与排障，不带开机自启与自动更新。",
    cta: "下载免安装版",
  },
  {
    kind: "update",
    title: "更新包",
    short: "更新包",
    desc: `增量更新包，zip 内只含 ${REQUIRED_UPDATE_FILES.join(" 与 ")}，由客户端两级替换。`,
    cta: "下载更新包",
  },
];

/** 一块产物：文件名 + 体积 + 下载按钮 + 可折叠的 sha256。 */
function ArtifactBlock({ release, kind }: { release: Release; kind: Artifact["kind"] }) {
  const artifact = findArtifact(release, kind);
  const meta = KINDS.find((k) => k.kind === kind);
  if (!artifact || !meta) return null;
  return (
    <div className="panel panel-lift">
      <div className="panel-head">
        <span className="panel-title">{meta.title}</span>
        {kind === "installer" && (
          <span className="pill pill-ok">
            <span className="pill-dot" aria-hidden="true" />
            推荐
          </span>
        )}
      </div>
      <div className="panel-pad dl-block">
        <p className="small muted">{meta.desc}</p>
        <div className="dl-file">
          <span className="dl-name">{artifact.name}</span>
          <span className="meta">{formatBytes(artifact.size)}</span>
        </div>
        <HashLine label={meta.short} hash={artifact.sha256} />
        <div className="dl-actions">
          <ArtifactLink artifact={artifact} className="btn btn-p">
            {meta.cta}
          </ArtifactLink>
          <span className="meta">渠道 {release.channel}</span>
        </div>
      </div>
    </div>
  );
}

/** 历史版本：撤回版保留在列表里但不给下载链接。 */
function HistoryTable({ releases }: { releases: Release[] }) {
  return (
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
          </tr>
        </thead>
        <tbody>
          {releases.map((release) => {
            const status = STATUS_PILL[release.status];
            const channel = CHANNEL_PILL[release.channel];
            const installer = findArtifact(release, "installer");
            const update = findArtifact(release, "update");
            // 草稿没发布过，公开下载页不列它；已撤回保留在列表里但不给下载链接
            const downloadable = release.status === "published";
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
                <td>
                  {installer && downloadable ? (
                    <ArtifactLink artifact={installer} className="mono">
                      {formatBytes(installer.size)}
                    </ArtifactLink>
                  ) : (
                    <span className="meta">{installer ? "不提供下载" : "—"}</span>
                  )}
                </td>
                <td>
                  {update && downloadable ? (
                    <ArtifactLink artifact={update} className="mono">
                      {formatBytes(update.size)}
                    </ArtifactLink>
                  ) : (
                    <span className="meta">{update ? "不提供下载" : "—"}</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export default function Download() {
  const state = useStore();
  const latest = latestPublished(state.releases, "stable");
  // 公开下载页只列已发布过的版本（草稿还没发布，撤回版保留但只做如实标注）
  const history = sortByVersionDesc(state.releases).filter(
    (r) => r.version !== latest?.version && r.status !== "draft",
  );

  if (state.status !== "ready") {
    return (
      <section className="sec">
        <div className="wrap">
          <DataStatus state={state} />
        </div>
      </section>
    );
  }

  if (!latest) {
    return (
      <section className="sec">
        <div className="wrap">
          <p className="muted">还没有已发布的 stable 版本，暂无可下载的产物。</p>
        </div>
      </section>
    );
  }

  return (
    <>
      <section className="sec">
        <div className="wrap">
          <div className="sec-head">
            <p className="eyebrow">下载</p>
            <h1 style={{ fontSize: "var(--fs-h1)" }}>
              最新 stable：{latest.version}
            </h1>
            <p className="sec-lede">
              {dateOf(latest.publishedAt)} 发布 · 平台 Windows x64 ·
              Electron {latest.electronVersion}
            </p>
          </div>
          <div className="grid-3">
            {KINDS.map((kind) => (
              <ArtifactBlock key={kind.kind} release={latest} kind={kind.kind} />
            ))}
          </div>
        </div>
      </section>

      <section className="sec sec-soft">
        <div className="wrap">
          <div className="sec-head">
            <h2>更新语义</h2>
            <p className="sec-lede">
              这几项决定客户端拿到新版本之后怎么升级，字段与更新清单一一对应。
            </p>
          </div>
          <div className="spec">
            <div className="spec-row">
              <span className="spec-k">更新包命名</span>
              <span className="spec-v">
                <span className="mono">{latest.manifest.url}</span>
              </span>
            </div>
            <div className="spec-row">
              <span className="spec-k">更新包内文件</span>
              <span className="spec-v">
                <span className="mono">{REQUIRED_UPDATE_FILES.join(" · ")}</span>
              </span>
            </div>
            <div className="spec-row">
              <span className="spec-k">最低可更新版本</span>
              <span className="spec-v">
                低于 <span className="mono">{latest.minVersion}</span> 必须走安装包（旧布局的替换路径不覆盖）
              </span>
            </div>
            <div className="spec-row">
              <span className="spec-k">强制更新</span>
              <span className="spec-v">{latest.required ? "是，客户端不允许跳过" : "否，客户端可以跳过"}</span>
            </div>
            <div className="spec-row">
              <span className="spec-k">Electron 版本</span>
              <span className="spec-v">
                <span className="mono">{latest.electronVersion}</span>
                <span className="muted">（与上一版不同时只能发安装包）</span>
              </span>
            </div>
            <div className="spec-row">
              <span className="spec-k">更新包哈希</span>
              <span className="spec-v hash">{latest.manifest.sha256}</span>
            </div>
          </div>
        </div>
      </section>

      <section className="sec">
        <div className="wrap">
          <div className="sec-head">
            <h2>历史版本</h2>
            <p className="sec-lede">
              草稿没发布过，不列在这里；已撤回的版本保留在列表里但不提供下载：
              它已从更新链摘掉，不再分发给客户端。
            </p>
          </div>
          {history.length === 0 ? (
            <div className="panel panel-pad log-empty">
              <p className="small">还没有历史版本：只有最新一个已发布版本时，这里为空。</p>
              <p className="meta">发布下一个版本后，旧版本会按时间倒序出现在这里；已撤回的版本也会保留在此但不提供下载。</p>
            </div>
          ) : (
            <HistoryTable releases={history} />
          )}
        </div>
      </section>
    </>
  );
}
