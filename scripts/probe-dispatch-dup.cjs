// 探针：dispatch 不重复渲染——主时间线无 agent_dispatch 工具行，卡只有一张
const { app, BrowserWindow } = require("electron");
const fs = require("fs");
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const OUT = "C:\\Users\\xzy\\Desktop\\my\\lxcode\\temp\\dispatch-dup-report.json";
const url = process.env.LXCODE_PREVIEW_URL || "http://127.0.0.1:5190/?mode=demo";

app.whenReady().then(async () => {
  const report = {};
  const win = new BrowserWindow({ width: 1440, height: 900, show: true });
  try {
    await win.loadURL(url);
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    await win.webContents.executeJavaScript(`(() => {
      const ta = document.querySelector(".piInput");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(ta, "给工具循环加个超时保护");
      ta.dispatchEvent(new Event("input", { bubbles: true }));
      document.querySelector(".send-btn").click();
    })()`);
    // 等 dispatch 卡出现
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 300));
      const has = await win.webContents.executeJavaScript(`!!document.querySelector(".dispatch-card")`);
      if (has) break;
    }
    // 采样三次（dispatch 期间 / 确认后 / 收尾后）
    const sample = async (label) => {
      const s = await win.webContents.executeJavaScript(`(() => {
        const main = document.querySelector(".thread-scroll");
        const rows = [...(main?.querySelectorAll(".trow") ?? [])].map((r) => (r.textContent || "").slice(0, 40));
        return {
          mainToolRows: rows.length,
          rows,
          dispatchRows: rows.filter((t) => /agent\.dispatch/i.test(t)).length,
          dispatchCards: document.querySelectorAll(".dispatch-card").length,
          cardsInMain: main ? main.querySelectorAll(".dispatch-card").length : -1,
        };
      })()`);
      report[label] = s;
    };
    await sample("during");
    // 等确认门 → 放行 → 等收尾
    for (let i = 0; i < 60; i++) {
      await new Promise((r) => setTimeout(r, 300));
      const ok = await win.webContents.executeJavaScript(`(() => {
        const b = document.querySelector('[data-variant="command"]:not([data-resolved]) button:nth-of-type(2)');
        if (!b) return false;
        b.click();
        return true;
      })()`);
      if (ok) break;
    }
    for (let i = 0; i < 80; i++) {
      await new Promise((r) => setTimeout(r, 300));
      const idle = await win.webContents.executeJavaScript(`!document.querySelector(".busy-row")`);
      if (idle) break;
    }
    await sample("after");
    fs.writeFileSync(OUT, JSON.stringify(report, null, 1), "utf8");
    log("WROTE " + OUT);
    process.exit(0);
  } catch (e) {
    fs.writeFileSync(OUT, JSON.stringify({ fail: e.message, report }, null, 1), "utf8");
    log("FAIL " + e.message);
    process.exit(1);
  }
});
