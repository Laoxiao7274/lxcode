// 模型选择器（Codex 式双面板）：打开先显示模型列表（左，按提供商分组，
// 只显示启用的模型），点底部「推理强度 >」展开右面板调强度。两面板独立错位浮层。
// 档位是模型元数据（ModelMeta.efforts）：右面板只渲染当前模型支持的档位；
// 无档位模型（如 deepseek-chat）整个强度入口隐藏；切模型时当前档位不支持则回落。
// 开合/点外/Esc 关闭（含退场动画）由 usePopover 承担。
import { useMemo, useState } from "react";
import { useSettings, EFFORTS, type EffortId } from "../../shared/settings";
import { usePopover } from "../../shared/popover";
import { IconCheck, IconChevronDown, IconChevronRight } from "../icons";

export function ModelPicker() {
  const { settings, set, providers } = useSettings();
  const { open, toggle, requestClose, rootRef } = usePopover();
  // 打开先显示模型（先选模型，再调强度）
  const [panel, setPanel] = useState<"model" | "effort">("model");

  // 只显示已连接且启用的提供商下的可见模型（设置「模型」分区控制）
  const groups = providers
    .filter((p) => p.connected && p.enabled)
    .map((p) => ({ name: p.name, models: p.models.filter((m) => m.visible) }))
    .filter((g) => g.models.length > 0);

  // 当前模型的档位元数据
  const current = useMemo(
    () => providers.flatMap((p) => p.models).find((m) => m.id === settings.model),
    [providers, settings.model],
  );
  const effortOptions = useMemo(() => EFFORTS.filter((e) => current?.efforts.includes(e.id)), [current]);
  const effortSupported = effortOptions.length > 0;
  const effortLabel = effortSupported ? effortOptions.find((e) => e.id === settings.effort)?.label ?? effortOptions[effortOptions.length - 1].label : undefined;

  // 重新打开回到模型面板
  const onToggle = () => {
    if (!open) setPanel("model");
    toggle();
  };

  // 切模型：当前档位在新模型上不存在 → 回落（优先"中"，否则新模型的首档）；
  // 选完带退场动画收起（最常见的关闭路径，手感要完整）
  const pickModel = (id: string) => {
    const m = providers.flatMap((p) => p.models).find((x) => x.id === id);
    const supported = m?.efforts ?? [];
    if (supported.length > 0 && !supported.includes(settings.effort)) {
      set({ model: id, effort: (supported.includes("medium") ? "medium" : supported[0]) as EffortId });
    } else {
      set({ model: id });
    }
    requestClose();
  };

  return (
    <div className="pop-wrap" ref={rootRef}>
      <span className="pop-trigger" onClick={onToggle}>
        <button type="button" className="model-chip" title="模型与推理强度">
          {settings.model}
          {effortSupported && <span className="chip-dim">· {effortLabel}</span>}
          <IconChevronDown />
        </button>
      </span>
      {open && (
        <>
          {/* 左面板：模型列表（按提供商分组，只显示启用的）+ 底部强度跳转行（无档位模型隐藏） */}
          <div className="mp-panel mp-left" role="menu" data-pop>
            <div className="mp-title">模型</div>
            {groups.length === 0 && <div className="mp-empty">没有启用的模型——到 设置 → 模型 里开启</div>}
            {groups.map((g) => (
              <div key={g.name} className="mp-group">
                <div className="mp-group-title">{g.name}</div>
                {g.models.map((m) => (
                  <button
                    key={m.id}
                    type="button"
                    className={"mp-item" + (settings.model === m.id ? " on" : "")}
                    role="menuitem"
                    onClick={() => pickModel(m.id)}
                  >
                    <span className="mp-label">
                      <span className="mp-model-name">{m.name}</span>
                      <span className="mp-model-desc">{m.desc}</span>
                    </span>
                    {settings.model === m.id && <span className="mp-check">{<IconCheck />}</span>}
                  </button>
                ))}
              </div>
            ))}
            {effortSupported && (
              <>
                <div className="mp-sep" />
                <button
                  type="button"
                  className={"mp-item mp-jump" + (panel === "effort" ? " hl" : "")}
                  onClick={() => setPanel(panel === "effort" ? "model" : "effort")}
                >
                  <span className="mp-label">推理强度 · {effortLabel}</span>
                  <span className="mp-jump-arrow">{<IconChevronRight />}</span>
                </button>
              </>
            )}
          </div>
          {/* 右面板：只渲染当前模型支持的档位（点左面板底部行展开） */}
          {effortSupported && panel === "effort" && (
            <div className="mp-panel mp-right" role="menu" data-pop>
              <div className="mp-title">推理强度</div>
              {effortOptions.map((e) => (
                <button
                  key={e.id}
                  type="button"
                  className={"mp-item" + (effortLabel === e.label ? " on" : "")}
                  role="menuitem"
                  title={e.hint}
                  onClick={() => { set({ effort: e.id }); requestClose(); }}
                >
                  <span className="mp-label">{e.label}</span>
                  {effortLabel === e.label && <span className="mp-check">{<IconCheck />}</span>}
                </button>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
