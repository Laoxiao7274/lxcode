// 首页：非对称 hero + 亮点区块 + 最新 stable 版本条。
//
// 数据全部来自后端（src/shared/store.ts → GET /api/releases）：版本、日期、产物体积、
// 哈希都由 Release 记录算出来，本页不写死任何数据。
//
// 例外只有 hero 的三个产品事实（工具数 / 会话存储 / 服务数）与终端卡里的命令行——
// 它们是**产品固有属性**，接口里没有对应字段，只能写死；来源与核实日期见下方注释。

import { findArtifact, formatBytes, latestPublished } from "../shared/release";
import { dateOf } from "../shared/labels";
import { useStore } from "../shared/useStore";
import { DataStatus } from "../components/DataStatus";
import "../styles/page-site.css";

/**
 * hero 数字条：三个产品固有事实（接口里没有这些字段，只能写死）。
 * 来源 = 主仓 AGENTS.md §2/§3，核实日期 2026-10-08：
 *   · §3「内置 14 个（+ 目录动态注入）」→ 14 个内置工具；
 *   · §2 会话存储 SQLite（WAL，纯 Go）→ 会话与历史都在本机；
 *   · §2 服务化 Windows SCM 服务（开机自启 + 崩溃自动重启）→ 1 个常驻后端服务。
 * 这三个数字若与产品不符，改这里并同步改日期——不要凭印象改。
 */
const HERO_FACTS = [
  { value: "14", label: "内置工具（带风险分级）" },
  { value: "SQLite", label: "会话与历史都存本机" },
  { value: "1 个", label: "常驻后端服务（开机自启）" },
];

/** 亮点：四个卖点，文案对齐 AGENTS.md 的真实定位（不是编的功能）。 */
const FEATURES = [
  {
    title: "跑在自己机器上",
    body: "单用户、本机优先：会话、密钥与文件都留在本地，不经过我们的服务器。",
  },
  {
    title: "会话存在本地库",
    body: "会话落在本机 SQLite（WAL），支持长上下文压缩、历史检索与多会话并发。",
  },
  {
    title: "后端是常驻服务",
    body: "Go 后端注册为 Windows 服务，开机自启、崩溃自动重启，客户端只是壳。",
  },
  {
    title: "Electron 薄壳",
    body: "窗口、托盘与浏览器面板交给壳，重活在后端；壳连不上就给启动指引。",
  },
];

export default function Home() {
  const state = useStore();
  const latest = latestPublished(state.releases, "stable");
  const installer = latest ? findArtifact(latest, "installer") : null;
  const update = latest ? findArtifact(latest, "update") : null;

  return (
    <>
      <section className="hero">
        <div className="wrap hero-grid">
          <div>
            <p className="eyebrow">跑在自己机器上的个人智能体</p>
            <h1>会写代码，也管你的日常</h1>
            <p className="hero-lede">
              LXCode 把编程助手与通用助理合成一个本地应用：Go 内核负责工具与模型调用，
              React 客户端负责界面，Electron 薄壳只做窗口。
            </p>
            <div className="hero-cta">
              <a className="btn btn-p btn-lg" href="#/download">
                免费下载
              </a>
              <a className="btn btn-lg" href="#/changelog">
                看更新日志
              </a>
            </div>
            {latest ? (
              <div className="hero-badges">
                <span className="pill pill-ok">
                  <span className="pill-dot" aria-hidden="true" />
                  最新 stable
                </span>
                <span className="pill">{latest.version}</span>
                <span className="meta">{dateOf(latest.publishedAt)} 发布 · Windows 10/11</span>
              </div>
            ) : (
              state.status === "ready" && (
                <div className="hero-badges">
                  <span className="pill pill-warn">还没有已发布的版本</span>
                  <span className="meta">首个版本发布后，这里会出现版本徽章与下载入口</span>
                </div>
              )
            )}
            <div className="hero-stats">
              {HERO_FACTS.map((fact) => (
                <div className="hero-stat" key={fact.label}>
                  <span className="hero-stat-v">{fact.value}</span>
                  <span className="meta">{fact.label}</span>
                </div>
              ))}
            </div>
          </div>
          <div className="term" aria-hidden="true">
            <div className="term-bar">
              <span className="term-dots">
                <i />
                <i />
                <i />
              </span>
              <span className="meta term-dim">lxcode 后端</span>
              <span className="meta term-dim">ws://127.0.0.1:7789/rpc</span>
            </div>
            {/* 真实产品事实（不是会话记录假输出）：flag 与端口来自 AGENTS.md §2，核实日期 2026-10-08 */}
            <div className="term-body">{`$ lxcode --serve
后端就绪   ws://127.0.0.1:7789/rpc
$ lxcode --probe
服务化验收  开机自启 · 崩溃自动重启
会话存储     SQLite（WAL）· 全部留在本机`}</div>
          </div>
        </div>
      </section>

      {state.status !== "ready" && (
        <section className="sec">
          <div className="wrap">
            <DataStatus state={state} />
          </div>
        </section>
      )}

      <section className="sec">
        <div className="wrap">
          <div className="sec-head">
            <p className="eyebrow">为什么是它</p>
            <h2>本地优先，工具面完整</h2>
            <p className="sec-lede">
              不是聊天窗口套壳：工具、会话、服务与壳都是自己的实现，能力与边界写得清楚。
            </p>
          </div>
          <div className="grid-2">
            {FEATURES.map((feature, index) => (
              <div className="panel panel-lift panel-pad card-body" key={feature.title}>
                <span className="card-idx">{String(index + 1).padStart(2, "0")}</span>
                <h3>{feature.title}</h3>
                <p>{feature.body}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {latest ? (
        <section className="sec sec-soft">
          <div className="wrap">
            <div className="panel">
              <div className="panel-head">
                <span className="panel-title">最新 stable 版本</span>
                <span className="pill pill-ok">
                  <span className="pill-dot" aria-hidden="true" />
                  已发布
                </span>
              </div>
              <div className="panel-pad rel-bar">
                <div className="rel-main">
                  <span className="rel-ver">{latest.version}</span>
                  <span className="meta">{dateOf(latest.publishedAt)} 发布</span>
                  {installer && <span className="meta">安装包 {formatBytes(installer.size)}</span>}
                  {update && <span className="meta">更新包 {formatBytes(update.size)}</span>}
                </div>
                <div className="row-actions">
                  <a className="btn btn-p" href="#/download">
                    下载 {latest.version}
                  </a>
                  <a className="btn" href="#/changelog">
                    这次改了什么
                  </a>
                </div>
              </div>
            </div>
          </div>
        </section>
      ) : (
        state.status === "ready" && (
          <section className="sec sec-soft">
            <div className="wrap">
              <div className="panel panel-pad log-empty">
                <p className="small">还没有已发布的版本：暂时没有可下载的安装包。</p>
                <p className="meta">版本发布后会出现在这里，也会出现在「下载」页。</p>
              </div>
            </div>
          </section>
        )
      )}
    </>
  );
}
