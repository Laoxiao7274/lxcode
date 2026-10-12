// 下载页：按平台分区（Windows / Android）+ 更新语义 + 历史版本（含已撤回版本的如实标注）。
//
// 两个平台的版本号**来源分开、互不引用**：
//   Windows — /api/releases 里最新已发布的 stable（latestPublished），读不到回落到
//             WINDOWS_FALLBACK_VERSION 常量（只展示版本号，产物列表仍以接口为准）；
//   Android — ANDROID_APK 常量（安卓独立版本源，不进发布模型）。
// 桌面与安卓发版节奏不同（桌面 0.2.3、安卓 0.2.3——本次安卓重发包修复安装器
// 「packageinfo is null」问题，改 v1+v2 双签名），共用一个版本号会让用户把
// 两个平台的版本混为一谈，所以页面头部不再报「最新 stable：x.y.z」，改由各平台
// 区块各自标注自己的版本。
//
// 桌面三块不是三种格式，而是三条更新路径（见 release.ts 的 ArtifactKind 注释）：
// 安装包 = 唯一能带 Electron 升级的全量包；免安装 = 绿色分发；更新包 = 两级替换的增量包。
// 撤回版本保留在历史列表里但**不提供下载链接**（如实标注，不假装它不存在）。
// 产物链接走 ArtifactLink：/releases/<文件名> 是真下载（后端静态服务直发）。
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
import { SITE_BASE } from "../shared/base";
import "../styles/page-site.css";

/**
 * Windows 桌面版版本号回落值。
 *
 * 正常路径：版本号取 /api/releases 里最新已发布的 stable（latestPublished，与
 * 管理后台 / manifest 同源）。接口读不到、或还没有已发布版本时，回落到这个常量
 * ——只用于展示「桌面版当前是什么版本」，产物列表仍以接口为准（读不到就不列，
 * 不编造下载链接）。发版后手工同步这里。
 */
const WINDOWS_FALLBACK_VERSION = "0.2.3";

/**
 * Android APK（原型）：不进发布模型，版本号**独立于桌面版**演进。
 *
 * 后端产物模型只有 installer/portable/update 三类（`domain.ArtifactKind`），APK 塞进去
 * 会污染桌面更新链（manifest / 校验 / 历史表都按桌面语义建的）。所以走静态文件方案：
 * APK 放站点 dist/downloads/ 下，由后端 --static 兜底直接发。
 * 这里的常量是安卓自己的版本源（与 WINDOWS_FALLBACK_VERSION 分开，互不引用）——
 * 桌面与安卓发版节奏不同，versionName 不再与桌面版本对齐，换包必须同步改。
 */
const ANDROID_APK = {
  version: "0.2.3",
  name: "lxcode-0.2.3-android.apk",
  size: 11622193,
  sha256: "b92b9899c802c951a64046badd726d807de407d4edcc45a60ada5d711c1d91a8",
  url: "downloads/lxcode-0.2.3-android.apk",
};

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
  // 桌面版版本号：接口优先，回落常量兜底（安卓版本只读 ANDROID_APK 常量，不经过这里）。
  const winVersion = latest?.version ?? WINDOWS_FALLBACK_VERSION;
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

  return (
    <>
      <section className="sec">
        <div className="wrap">
          <div className="sec-head">
            <p className="eyebrow">下载</p>
            <h1 style={{ fontSize: "var(--fs-h1)" }}>下载 lxcode</h1>
            <p className="sec-lede">
              各平台独立发版：Windows 桌面版与 Android 客户端的版本号各自演进、互不共用，
              每个平台区块标注自己的当前版本。
            </p>
          </div>
        </div>
      </section>

      {/* Windows：版本号与产物全部来自 releases 接口（读不到时版本号回落、产物不列）。 */}
      <section className="sec sec-soft">
        <div className="wrap">
          <div className="sec-head">
            <h2>
              Windows 桌面版{" "}
              <span className="pill pill-ok">
                <span className="pill-dot" aria-hidden="true" />
                v{winVersion}
              </span>
            </h2>
            <p className="sec-lede">
              {latest
                ? `${dateOf(latest.publishedAt)} 发布 · Electron ${latest.electronVersion} · 系统要求：Windows 10 / 11（x64）`
                : "发布数据暂时不可读，版本号显示为回落值，产物列表以接口恢复后为准。系统要求：Windows 10 / 11（x64）。"}
            </p>
          </div>
          {latest ? (
            <div className="grid-3">
              {KINDS.map((kind) => (
                <ArtifactBlock key={kind.kind} release={latest} kind={kind.kind} />
              ))}
            </div>
          ) : (
            <div className="panel panel-pad log-empty">
              <p className="small">没有读到已发布的 stable 版本，暂不列出产物链接。</p>
              <p className="meta">
                接口恢复后这里会列出安装包与更新包；上方版本号是回落值，不代表存在可下载产物。
              </p>
            </div>
          )}
        </div>
      </section>

      {/* Android：版本号只来自 ANDROID_APK 常量（安卓独立版本源，与桌面 releases 接口无关）。 */}
      <section className="sec">
        <div className="wrap">
          <div className="sec-head">
            <h2>
              Android 远端客户端{" "}
              <span className="pill pill-ok">
                <span className="pill-dot" aria-hidden="true" />
                v{ANDROID_APK.version}
              </span>
            </h2>
            <p className="sec-lede">
              当前版本 v{ANDROID_APK.version}（独立于桌面版）· 远端客户端（早期版）：连接自己机器上的
              lxcode 后端跑会话，不走应用商店。
            </p>
          </div>
          <div className="panel panel-lift">
            <div className="panel-pad dl-block">
              <div className="dl-file">
                <span className="dl-name">{ANDROID_APK.name}</span>
                <span className="meta">{formatBytes(ANDROID_APK.size)}</span>
              </div>
              <HashLine label="APK" hash={ANDROID_APK.sha256} />
              <div className="dl-actions">
                <a
                  className="btn btn-p"
                  href={SITE_BASE + ANDROID_APK.url}
                  download={ANDROID_APK.name}
                >
                  下载 APK
                </a>
                <span className="meta">versionCode 2 · Android 8.0+</span>
              </div>
            </div>
          </div>
        </div>
      </section>

      <section className="sec sec-soft">
        <div className="wrap">
          <div className="sec-head">
            <h2>Windows 更新语义</h2>
            <p className="sec-lede">
              桌面客户端拿到新版本之后怎么升级，字段与更新清单一一对应（安卓 APK 不进这套更新模型）。
            </p>
          </div>
          <div className="spec">
            <div className="spec-row">
              <span className="spec-k">更新包命名</span>
              <span className="spec-v">
                <span className="mono">{latest?.manifest.url ?? "—"}</span>
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
                {latest ? (
                  <>
                    低于 <span className="mono">{latest.minVersion}</span> 必须走安装包（旧布局的替换路径不覆盖）
                  </>
                ) : (
                  "以接口数据为准"
                )}
              </span>
            </div>
            <div className="spec-row">
              <span className="spec-k">强制更新</span>
              <span className="spec-v">
                {latest ? (latest.required ? "是，客户端不允许跳过" : "否，客户端可以跳过") : "—"}
              </span>
            </div>
            <div className="spec-row">
              <span className="spec-k">Electron 版本</span>
              <span className="spec-v">
                <span className="mono">{latest?.electronVersion ?? "—"}</span>
                <span className="muted">（与上一版不同时只能发安装包）</span>
              </span>
            </div>
            <div className="spec-row">
              <span className="spec-k">更新包哈希</span>
              <span className="spec-v hash">{latest?.manifest.sha256 ?? "—"}</span>
            </div>
          </div>
        </div>
      </section>

      <section className="sec">
        <div className="wrap">
          <div className="sec-head">
            <h2>Windows 历史版本</h2>
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
