// 上传新版本：真实 multipart 上传 + 后端 dry-run 校验与 manifest 预览。
//
// 与上轮（纯前端 mock 表单）的差别：
//   · 用户选**本地产物文件**，前端把元数据 + 三个文件作为 multipart 打给后端；
//   · 「校验并预览」调 POST /api/admin/releases/check（dry-run，不落盘）：
//     前端先用 crypto.subtle 算三个文件与两个内部路径的 sha256，后端跑同一套
//     CheckReleaseDraft，把阻断项与按契约生成的 manifest 回给页面；
//   · 「上传并入库」调 POST /api/admin/releases：服务端再算一遍真哈希、命名校验、
//     版本占用校验、落盘、入库，状态 = draft；校验不过就是 400 + 阻断项。
//
// 取舍（如实）：dry-run 与真上传各传一次文件（同一份字节传两遍），换来的是
// 「校验规则只有服务端一份」——正式形态应改成一次上传 + 两阶段提交。

import { useState } from "react";

import { latestPublished, type Channel, type Manifest, type PublishCheck } from "../../shared/release";
import { ApiError, apiUpload } from "../../shared/api/client";
import { reload } from "../../shared/store";
import { useStore } from "../../shared/useStore";
import { showNotice } from "../../components/protoNotice";

const CHECK_PILL = {
  pass: { cls: "pill pill-ok", label: "通过" },
  warn: { cls: "pill pill-warn", label: "提醒" },
  fail: { cls: "pill pill-danger", label: "阻断" },
} as const;

interface CheckResult {
  checks: PublishCheck[];
  canPublish: boolean;
  manifest: Manifest;
}

/**
 * 表单默认填「下一个补丁版本」：一打开就是干净状态，而不是先撞已有版本号。
 * 一个版本都没有时返回空串（不编一个版本号出来，让人自己填）。
 */
function nextVersion(current: string | undefined): string {
  if (!current) return "";
  const [major = 0, minor = 0, patch = 0] = current
    .split(".")
    .map((n) => parseInt(n, 10) || 0);
  return `${major}.${minor}.${patch + 1}`;
}

function TextField(props: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  hint?: string;
  span?: boolean;
}) {
  return (
    <label className={props.span ? "field form-span" : "field"}>
      <span className="lbl">{props.label}</span>
      <input
        className="inp mono"
        value={props.value}
        onChange={(e) => props.onChange(e.target.value)}
      />
      {props.hint && <span className="hint">{props.hint}</span>}
    </label>
  );
}

function FileField(props: {
  label: string;
  desc: string;
  file: File | null;
  onPick: (file: File | null) => void;
}) {
  return (
    <label className="field">
      <span className="lbl">{props.label}</span>
      <input className="inp" type="file" onChange={(e) => props.onPick(e.target.files?.[0] ?? null)} />
      <span className="hint">{props.desc}</span>
      {props.file && (
        <span className="meta">
          已选 {props.file.name}（{props.file.size} 字节）
        </span>
      )}
    </label>
  );
}

export default function ReleaseNew() {
  const state = useStore();
  const latest = latestPublished(state.releases, "stable");
  const [version, setVersion] = useState(() => nextVersion(latest?.version));
  const [channel, setChannel] = useState<Channel>("stable");
  // 不编一个 Electron 版本：留空让人按 shell/package.json 填
  const [electronVersion, setElectronVersion] = useState(latest?.electronVersion ?? "");
  // 不预填假的 minVersion：留空让人按「低于它的客户端必须走安装包」填
  const [minVersion, setMinVersion] = useState("");
  const [required, setRequired] = useState(false);
  const [notes, setNotes] = useState("");
  const [installer, setInstaller] = useState<File | null>(null);
  const [portable, setPortable] = useState<File | null>(null);
  const [update, setUpdate] = useState<File | null>(null);
  const [result, setResult] = useState<CheckResult | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState<"check" | "upload" | null>(null);

  const picked: Array<[string, File | null]> = [
    ["installer", installer],
    ["portable", portable],
    ["update", update],
  ];

  function buildForm(): FormData {
    const form = new FormData();
    form.set("version", version.trim());
    form.set("channel", channel);
    form.set("electronVersion", electronVersion.trim());
    form.set("minVersion", minVersion.trim());
    form.set("required", String(required));
    form.set("notes", notes);
    for (const [kind, file] of picked) {
      if (file) form.set(kind, file, file.name);
    }
    return form;
  }

  async function check() {
    setBusy("check");
    setFailure(null);
    try {
      const checked = await apiUpload<CheckResult>("/api/admin/releases/check", buildForm());
      setResult(checked);
    } catch (error) {
      setResult(null);
      setFailure(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(null);
    }
  }

  async function upload() {
    setBusy("upload");
    setFailure(null);
    try {
      const created = await apiUpload<{ release: { version: string } }>(
        "/api/admin/releases",
        buildForm(),
      );
      await reload("admin");
      showNotice(`版本 ${created.release.version} 已上传并入库（状态 draft）`);
      window.location.hash = "#/admin/releases";
    } catch (error) {
      if (error instanceof ApiError && error.checks) {
        setResult({ checks: error.checks, canPublish: false, manifest: previewManifest() });
      }
      setFailure(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(null);
    }
  }

  function previewManifest(): Manifest {
    if (result) return result.manifest;
    return {
      version: version.trim(),
      url: `update-${version.trim()}.zip`,
      sha256: "",
      size: update?.size ?? 0,
      files: {},
    };
  }

  return (
    <div className="adm-page">
        <p className="adm-lede">选本地产物文件 → 「校验并预览」调后端 dry-run（不落盘）→「上传并入库」真上传。
            校验规则只有后端一份（与前端 release.ts 同口径），所以不会出现「清单全绿但按钮灰着」。</p>

        <div className="panel">
          <div className="panel-head">
            <span className="panel-title">版本与渠道</span>
            <span className="meta">产物名由后端按版本号校验</span>
          </div>
          <div className="panel-pad">
            <div className="form-grid">
              <TextField
                label="版本号"
                value={version}
                onChange={setVersion}
                hint="三段数字；与已有版本重复会被后端拦下"
              />
              <label className="field">
                <span className="lbl">渠道</span>
                <select
                  className="sel"
                  value={channel}
                  onChange={(e) => setChannel(e.target.value as Channel)}
                >
                  <option value="stable">stable（默认更新链）</option>
                  <option value="beta">beta（需显式切换才收到）</option>
                </select>
                <span className="hint">上传后是草稿，发布后才进更新链</span>
              </label>
              <TextField
                label="Electron 版本"
                value={electronVersion}
                onChange={setElectronVersion}
                hint="与 shell/package.json 的 electron 版本一致；与上一版不同时更新包不可用（只是提醒）"
              />
              <TextField
                label="最低可更新版本"
                value={minVersion}
                onChange={setMinVersion}
                hint="低于它的客户端必须走安装包"
              />
              <label className="field form-span">
                <span className="lbl">更新提示要点</span>
                <textarea
                  className="txt"
                  value={notes}
                  onChange={(e) => setNotes(e.target.value)}
                />
                <span className="hint">一行一条，客户端更新提示直接展示</span>
              </label>
              <label className="field form-span">
                <span className="lbl">强制更新</span>
                <span className="inline-note">
                  <input
                    type="checkbox"
                    checked={required}
                    onChange={(e) => setRequired(e.target.checked)}
                  />
                  <span className="meta">勾选后客户端不允许跳过这个版本</span>
                </span>
              </label>
            </div>
          </div>
        </div>

        <div className="panel" style={{ marginBottom: 18 }}>
          <div className="panel-head">
            <span className="panel-title">产物文件</span>
            <span className="meta">文件名要与后端按版本号算出的命名一致，否则会被拦下</span>
          </div>
          <div className="panel-pad">
            <div className="form-grid">
              <FileField
                label="安装包（NSIS）"
                desc={`建议文件名：Lxcode Setup ${version.trim()}.exe`}
                file={installer}
                onPick={setInstaller}
              />
              <FileField
                label="免安装版（win-unpacked）"
                desc={`建议文件名：Lxcode-${version.trim()}-win-x64.zip；没有只是提醒`}
                file={portable}
                onPick={setPortable}
              />
              <FileField
                label="更新包（zip）"
                desc={`必须是含 resources/app.asar 与 resources/bin/lxcode.exe 的 zip：update-${version.trim()}.zip`}
                file={update}
                onPick={setUpdate}
              />
            </div>
            <div className="row-actions" style={{ marginTop: 8 }}>
              <button className="btn" type="button" disabled={busy !== null} onClick={check}>
                {busy === "check" ? "校验中…" : "校验并预览"}
              </button>
              <button
                className="btn btn-p"
                type="button"
                disabled={busy !== null}
                onClick={upload}
              >
                {busy === "upload" ? "上传中…" : "上传并入库（draft）"}
              </button>
              <span className="meta">传的是你本机选中的文件；后端会再算一遍真哈希</span>
            </div>
            {failure && <p className="err small">{failure}</p>}
          </div>
        </div>

        <div className="panel">
          <div className="panel-head">
            <span className="panel-title">后端校验结果</span>
            {result && (
              <span className={result.canPublish ? "pill pill-ok" : "pill pill-danger"}>
                {result.canPublish ? "清单齐备" : "存在阻断项"}
              </span>
            )}
          </div>
          <div className="panel-pad">
            {result ? (
              <>
                <div className="adm-checks">
                  {result.checks.map((item) => {
                    const pill = CHECK_PILL[item.level];
                    return (
                      <div className={`adm-check adm-check-${item.level}`} key={item.label}>
                        <span className="adm-check-k">
                          <span className={pill.cls}>{pill.label}</span>
                          {item.label}
                        </span>
                        <span className="adm-check-d">{item.detail}</span>
                      </div>
                    );
                  })}
                </div>
                <p className="meta" style={{ marginTop: 12 }}>
                  manifest 预览（后端按契约生成：url = 更新包文件名，files 是 zip 内两个路径的真哈希）
                </p>
                <pre className="code">{JSON.stringify(result.manifest, null, 2)}</pre>
              </>
            ) : (
              <p className="muted small">
                还没校验。点「校验并预览」会带着你选的文件调后端 dry-run——校验与发布同源（都在服务端）。
              </p>
            )}
          </div>
        </div>
    </div>
  );
}
