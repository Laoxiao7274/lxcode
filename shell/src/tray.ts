// tray.ts —— 系统托盘（用户报「隐藏栏没有显示」：托盘从未实现过）。
// 语义：关闭按钮 = 隐藏到托盘（后端/隧道保持运行）；托盘左键 = 显示/聚焦；
// 右键菜单 = 显示 / 退出（退出走 before-quit 的统一收尾：杀后端 + frpc）。
// 注意与更新器的配合：asar 冷替换脚本等 Lxcode.exe 退出——用户点了「退出」
// 才会真退（点关闭只是藏起来），这是托盘应用的常规语义。
import { app, Menu, nativeImage, Tray, type BrowserWindow } from "electron";
import { existsSync } from "node:fs";
import { join } from "node:path";

let tray: Tray | null = null;
let quitting = false;

/** 托盘图标：dev 用 shell/build/icon.png；产线从 asar 里取（files 已含 build/icon.png）。 */
function trayIconPath(): string {
  const packaged = join(app.getAppPath(), "build", "icon.png");
  if (app.isPackaged && existsSync(packaged)) return packaged;
  return join(__dirname, "..", "build", "icon.png");
}

/** 装配托盘 + 关闭→隐藏语义。返回「是否真退出」的判定给 close 拦截用。 */
export function createTray(getWin: () => BrowserWindow | null): void {
  const showMain = (): void => {
    const win = getWin();
    if (!win) return;
    win.show();
    win.focus();
  };

  tray = new Tray(nativeImage.createFromPath(trayIconPath()));
  tray.setToolTip("Lxcode");
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: "显示主窗口", click: showMain },
    { type: "separator" },
    {
      label: "退出",
      click: () => {
        quitting = true;
        app.quit();
      },
    },
  ]));
  tray.on("click", showMain); // 左键单击 = 显示

  // 关闭按钮 → 隐藏到托盘（后端/隧道继续跑）；托盘菜单「退出」才真退。
  app.on("before-quit", () => { quitting = true; });
  getWin()?.on("close", (e) => {
    if (quitting) return;
    e.preventDefault();
    getWin()?.hide();
  });
}

export function isQuitting(): boolean {
  return quitting;
}
