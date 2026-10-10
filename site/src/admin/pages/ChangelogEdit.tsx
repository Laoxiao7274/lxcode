// 后台更新日志编辑：条目列表 + 新增 / 编辑 / 删除（都打后端管理接口）。
//
// 版本号只能从已有版本里选（下拉）：日志归属不存在的版本会让更新日志页出现
// 一个「没有版本的条目」，所以这里从结构上避免它，而不是靠事后校验。
// 分类的标签与配色来自 labels.ts，与前台更新日志页共用一份。
// 写操作后重新拉一遍（服务端是唯一事实源）。

import { useState } from "react";

import { sortByVersionDesc } from "../../shared/release";
import { KIND_TAG } from "../../shared/labels";
import { CHANGELOG_KINDS, type ChangelogEntry, type ChangelogKind } from "../../shared/release";
import { addChangelogEntry, removeChangelogEntry, updateChangelogEntry } from "../../shared/store";
import { useStore } from "../../shared/useStore";
import { AdminScopeNote, DataStatus } from "../../components/DataStatus";

/** 表单里的日期是 YYYY-MM-DD（input[type=date] 的格式），存的是 ISO 8601 UTC。 */
function dateInputOf(iso: string): string {
  return iso.slice(0, 10);
}

function isoFromDateInput(date: string): string {
  return `${date}T00:00:00Z`;
}

export default function ChangelogEdit() {
  const state = useStore();
  const { changelog } = state;
  const versions = sortByVersionDesc(state.releases).map((r) => r.version);
  const [editing, setEditing] = useState<number | null>(null);
  const [version, setVersion] = useState(() => versions[0] ?? "");
  const [date, setDate] = useState(() => new Date().toISOString().slice(0, 10));
  const [kind, setKind] = useState<ChangelogKind>("features");
  const [text, setText] = useState("");

  const complete = text.trim() !== "" && version !== "" && date !== "";
  const [failure, setFailure] = useState<string | null>(null);

  function resetForm() {
    setEditing(null);
    setText("");
  }

  function save() {
    if (!complete) return;
    const payload = { version, date: isoFromDateInput(date), kind, text: text.trim() };
    const action = editing === null ? addChangelogEntry(payload) : updateChangelogEntry(editing, payload);
    setFailure(null);
    void action
      .then(resetForm)
      .catch((error: unknown) => {
        setFailure(error instanceof Error ? error.message : String(error));
      });
  }

  function startEdit(entry: ChangelogEntry) {
    setEditing(entry.id);
    setVersion(entry.version);
    setDate(dateInputOf(entry.date));
    setKind(entry.kind);
    setText(entry.text);
  }

  return (
    <div className="adm-page">
        <p className="adm-lede">条目与版本绑定（版本号从已有版本里选，避免出现没有版本的条目）；
            改动只写内存态，刷新即回初始数据。</p>

        <div className="panel" style={{ marginBottom: 22 }}>
          <div className="panel-head">
            <span className="panel-title">{editing === null ? "新增条目" : "编辑条目"}</span>
            {editing !== null && (
              <button className="btn btn-sm btn-quiet" type="button" onClick={resetForm}>
                取消编辑
              </button>
            )}
          </div>
          <div className="panel-pad">
            <div className="form-grid">
              <label className="field">
                <span className="lbl">归属版本</span>
                <select className="sel" value={version} onChange={(e) => setVersion(e.target.value)}>
                  {versions.map((v) => (
                    <option key={v} value={v}>
                      {v}
                    </option>
                  ))}
                </select>
                <span className="hint">只列已有版本（含草稿与已撤回）</span>
              </label>
              <label className="field">
                <span className="lbl">分类</span>
                <select
                  className="sel"
                  value={kind}
                  onChange={(e) => setKind(e.target.value as ChangelogKind)}
                >
                  {CHANGELOG_KINDS.map((k) => (
                    <option key={k} value={k}>
                      {KIND_TAG[k].label}
                    </option>
                  ))}
                </select>
                <span className="hint">分类标签与前台更新日志页同一套</span>
              </label>
              <label className="field">
                <span className="lbl">日期</span>
                <input
                  className="inp mono"
                  type="date"
                  value={date}
                  onChange={(e) => setDate(e.target.value)}
                />
                <span className="hint">存成 ISO 8601（UTC）</span>
              </label>
              <label className="field form-span">
                <span className="lbl">条目正文</span>
                <textarea
                  className="txt"
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                />
                <span className="hint">一条一句话，中文，面向用户</span>
              </label>
            </div>
            <div className="row-actions">
              <button className="btn btn-p" type="button" disabled={!complete} onClick={save}>
                {editing === null ? "新增条目" : "保存修改"}
              </button>
              {!complete && <span className="meta">版本、日期、正文都填上才能保存</span>}
              <span className="meta">写操作打后端接口，服务端是唯一事实源</span>
            </div>
            {failure && <p className="err small">{failure}</p>}
          </div>
        </div>

        <div className="panel">
          <div className="panel-head">
            <span className="panel-title">全部条目</span>
            <span className="meta">共 {changelog.length} 条</span>
          </div>
          <div className="panel-pad">
            <DataStatus state={state} />
            <AdminScopeNote state={state} />
            <div>
              {changelog.map((entry) => {
                const tag = KIND_TAG[entry.kind];
                return (
                  <div className="adm-log" key={entry.id}>
                    <div className="adm-log-main">
                      <div className="adm-log-head">
                        <span className={tag.cls}>{tag.label}</span>
                        <span className="meta">
                          {entry.version} · {dateInputOf(entry.date)}
                        </span>
                      </div>
                      <p className="small">{entry.text}</p>
                    </div>
                    <div className="row-actions">
                      <button className="btn btn-sm" type="button" onClick={() => startEdit(entry)}>
                        编辑
                      </button>
                      <button
                        className="btn btn-sm btn-danger"
                        type="button"
                        onClick={() => {
                          if (editing === entry.id) resetForm();
                          setFailure(null);
                          void removeChangelogEntry(entry.id).catch((error: unknown) => {
                            setFailure(error instanceof Error ? error.message : String(error));
                          });
                        }}
                      >
                        删除
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
    </div>
  );
}
