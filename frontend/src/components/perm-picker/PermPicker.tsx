// 后端目前只有固定风险确认策略，不展示无实际作用的权限预设。
export function PermPicker() {
  return <button type="button" className="perm-chip" disabled
    title="低危工具自动执行，高危工具需确认；权限模式切换尚未实现">
    固定确认策略
  </button>;
}
