// 使用现有 Electron 的 Chromium 验证浏览器/壳两种形态的真实布局，不启动应用主进程或 Go。
// 用法：node shell/node_modules/electron/cli.js scripts/check-preview-layout.cjs --no-sandbox --disable-gpu
// 注意：
// - Windows 上 Electron 主进程的 stdout 在管道下可能丢失（实测只丢 stdout 不丢 stderr），
//   所有输出走 stderr；app.exit 前留冲刷时间。
// - 实测同实例「销毁窗口再建新窗口」会让第二次 loadURL 网络层 ERR_FAILED——
//   因此全程复用同一个窗口，逐场景改尺寸/UA 后重载。
// - 隐藏窗口（show:false）里 rAF 不触发，测量不等待，getBoundingClientRect 本就强制同步排布。
const { app, BrowserWindow } = require('electron');
const assert = require('node:assert/strict');

const url = process.env.LXCODE_PREVIEW_URL || 'http://127.0.0.1:5190/?mode=demo';
const log = (...a) => process.stderr.write(a.join(' ') + '\n');

const timeout = setTimeout(() => { log('预览布局检查超时'); app.exit(1); }, 30000);
// 结束前给 stderr 一点冲刷时间再退（app.exit 立断管道会吞输出）
function finish(code) { clearTimeout(timeout); setTimeout(() => app.exit(code), 150); }

app.whenReady().then(async () => {
  const errors = [];
  const win = new BrowserWindow({
    width: 1200, height: 800, useContentSize: true, show: false,
    webPreferences: { contextIsolation: true, nodeIntegration: false },
  });
  const electronUA = win.webContents.getUserAgent();
  const browserUA = electronUA.replace(/Electron\/\S+/g, '');
  win.webContents.on('console-message', (ev) => { if (ev.level === 3) errors.push(ev.message); });

  const measure = () => win.webContents.executeJavaScript(`(() => {
    const box = (selector) => {
      const el = document.querySelector(selector);
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { x: r.x, y: r.y, width: r.width, height: r.height, right: r.right, bottom: r.bottom, display: getComputedStyle(el).display };
    };
    return {
      outer: box('.app-window'),
      stage: box('.window-stage'),
      app: box('.app'),
      topbar: box('.topbar'),
      sidebar: box('.sidebar'),
      main: box('.main'),
      input: box('textarea'),
    };
  })()`);

  try {
    // [名称, 宽, 高, 是否保留 Electron UA（壳形态渲染路径）]
    const cases = [
      ['desktop', 1912, 948, false],
      ['mobile', 600, 800, false],
      ['shell', 1440, 900, true],
    ];
    for (const [name, width, height, keepUA] of cases) {
      win.setContentSize(width, height);
      win.webContents.setUserAgent(keepUA ? electronUA : browserUA);
      await win.loadURL(url);
      const result = await measure();
      log(name, JSON.stringify(result));
      // 壳形态没有 .app-window 外壳，回退用窗口内容尺寸当边界
      const bounds = result.outer || { width, height, right: width, bottom: height };
      assert.ok(result.app && result.app.width >= bounds.width - 3, `${name}: 应用应填满窗口宽度`);
      assert.ok(result.app.height >= bounds.height - 3, `${name}: 应用应填满窗口高度`);
      assert.ok(result.topbar && result.topbar.height >= 40, `${name}: 顶栏应存在`);
      assert.ok(result.main && result.main.width > 200 && result.main.height > 200, `${name}: 主区应可见`);
      assert.ok(result.input && result.input.height > 0 && result.input.bottom <= bounds.bottom + 1, `${name}: 输入框应在窗口内`);
      assert.ok(result.main.right <= bounds.right + 1, `${name}: 主区不应水平溢出`);
      assert.equal(result.sidebar ? result.sidebar.display === 'none' : true, width <= 768, `${name}: 侧栏响应式`);
    }
    // 空态（上一场景页仍是对话视图）：4 张建议卡 + 纵向光学居中 + Agent 芯片
    const empty = await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector(".empty-state");
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return {
        top: Math.round(r.top),
        bottom: Math.round(r.bottom),
        cards: document.querySelectorAll(".suggest-card").length,
        agentChip: !!document.querySelector(".empty-agent"),
      };
    })()`);
    log("empty-state", JSON.stringify(empty));
    assert.ok(empty && empty.cards === 4, "chat: 空态应有 4 张建议卡");
    assert.ok(empty && empty.agentChip, "chat: 空态应显示当前 Agent 芯片");
    assert.ok(empty && empty.top > 100 && empty.bottom < 900, "chat: 空态应纵向居中（不贴顶）");
    // Agent 选择器 + 委派面板：打开菜单 → 可委派入口 → 面板 → 切换（会话级）
    await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector(".agent-chip");
      if (!btn) throw new Error("Agent 选择器缺失");
      btn.click();
    })()`);
    const apMenu = await win.webContents.executeJavaScript(`(() => ({
      menu: !!document.querySelector(".perm-menu"),
      jump: !!document.querySelector('[data-ap="delegates"]'),
    }))()`);
    log("agent-menu", JSON.stringify(apMenu));
    assert.ok(apMenu.menu, "chat: Agent 菜单应打开");
    assert.ok(apMenu.jump, "chat: 主 Agent 应有可委派入口");
    await win.webContents.executeJavaScript(`document.querySelector('[data-ap="delegates"]').click()`);
    const apPanel = await win.webContents.executeJavaScript(`(() => {
      const panel = document.querySelector(".ap-panel");
      return panel ? { items: panel.querySelectorAll(".mp-item").length, checked: panel.querySelectorAll(".mp-item.on").length } : null;
    })()`);
    log("agent-delegate-panel", JSON.stringify(apPanel));
    assert.ok(apPanel && apPanel.items >= 3, "chat: 委派面板应列出启用的子 Agent");
    assert.ok(apPanel && apPanel.checked >= 3, "chat: 名单默认应有 3 个启用子 Agent 勾选");
    await win.webContents.executeJavaScript(`document.querySelector(".ap-panel .mp-item").click()`);
    const afterToggle = await win.webContents.executeJavaScript(`(() => ({
      stillOpen: !!document.querySelector(".ap-panel"),
      checked: document.querySelectorAll(".ap-panel .mp-item.on").length,
    }))()`);
    log("agent-delegate-toggle", JSON.stringify(afterToggle));
    assert.ok(afterToggle.stillOpen, "chat: 切换委派不应关闭面板");
    assert.ok(afterToggle.checked === apPanel.checked - 1, "chat: 切换应改变委派计数");
    // Agents 视图点击路径（桌面，浏览器 UA）：选择器在对话视图 → 进名单 → 开组装 → 取消回名单
    win.setContentSize(1440, 900);
    win.webContents.setUserAgent(browserUA);
    await win.loadURL(url);
    // 隐藏窗口里 gsap ticker（rAF）冻结——退场补间永不完成、菜单不卸载。
    // 项目动效门控的测试钩子：关掉动效，关闭路径走同步分支，断言可测。
    await win.webContents.executeJavaScript("window.__LX_TEST_MOTION_OFF__ = true");
    const chipW = await win.webContents.executeJavaScript(
      `document.querySelector('.agent-chip') ? document.querySelector('.agent-chip').getBoundingClientRect().width : 0`,
    );
    assert.ok(chipW > 10, "chat: 输入区应有 Agent 选择器");
    await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector('[data-nav="agents"]');
      if (!btn) throw new Error("agents 导航入口缺失");
      btn.click();
    })()`);
    const roster = await win.webContents.executeJavaScript(`(() => {
      const r = (s) => {
        const el = document.querySelector(s);
        if (!el) return null;
        const b = el.getBoundingClientRect();
        return { w: b.width, h: b.height };
      };
      return {
        page: r(".ag-page"),
        grid: r(".ag-grid"),
        cards: document.querySelectorAll(".ag-card").length,
        main: !!document.querySelector(".ag-card.main"),
        navOn: !!document.querySelector(".nav-item.on"),
      };
    })()`);
    log("agents-roster", JSON.stringify(roster));
    assert.ok(roster.page && roster.page.h > 300, "agents: 名单页应可见");
    assert.ok(roster.grid && roster.grid.w > 300, "agents: 名单网格应可见");
    assert.ok(roster.cards >= 5, "agents: 演示名单应有至少 5 张卡");
    assert.ok(roster.main, "agents: 主 Agent 卡应存在");
    assert.ok(roster.navOn, "agents: 侧栏导航应高亮");
    await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector('[data-ag="new"]');
      if (!btn) throw new Error("组装入口缺失");
      btn.click();
    })()`);
    const editor = await win.webContents.executeJavaScript(`(() => {
      const r = (s) => {
        const el = document.querySelector(s);
        return el ? { w: el.getBoundingClientRect().width, h: el.getBoundingClientRect().height } : null;
      };
      return { form: r(".ag-form"), preview: r(".ag-preview"), input: !!document.querySelector("#ag-name") };
    })()`);
    log("agents-editor", JSON.stringify(editor));
    assert.ok(editor.form && editor.form.h > 300, "agents: 组装表单应可见");
    assert.ok(editor.preview && editor.preview.h > 100, "agents: 预览面板应可见");
    assert.ok(editor.input, "agents: 名称输入框应存在");
    // 详情两级：chip 点击 → 右侧紧凑面板跟随（参数表）；点面板 → 完整文档弹窗；关闭
    await win.webContents.executeJavaScript(`(() => {
      const chip = document.querySelector(".fd-chip");
      if (!chip) throw new Error("工具 chip 缺失");
      chip.click();
    })()`);
    const panel = await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector(".ag-detail");
      return el ? { h: el.getBoundingClientRect().height, params: el.querySelectorAll(".ag-param").length, hint: !!el.querySelector(".ag-detail-open-hint") } : null;
    })()`);
    log("agents-panel", JSON.stringify(panel));
    assert.ok(panel && panel.h > 60, "agents: chip 点击应有紧凑详情面板");
    assert.ok(panel && panel.params > 0, "agents: 面板应含参数表");
    assert.ok(panel && panel.hint, "agents: 面板应有展开提示");
    await win.webContents.executeJavaScript(`document.querySelector(".ag-detail").click()`);
    const detail = await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector(".ag-doc");
      return el ? { h: el.getBoundingClientRect().height, params: el.querySelectorAll(".ag-param").length, md: !!el.querySelector(".ag-detail-md") } : null;
    })()`);
    log("agents-detail", JSON.stringify(detail));
    assert.ok(detail && detail.h > 200, "agents: 点面板应打开文档弹窗");
    assert.ok(detail && detail.params > 0, "agents: 工具文档应含参数表");
    assert.ok(detail && detail.md, "agents: 工具文档应含 markdown 扩展文档");
    await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector(".ag-doc-close");
      if (!btn) throw new Error("文档弹窗关闭钮缺失");
      btn.click();
    })()`);
    // 模型下拉：自绘 Select 打开 → 选中 → 等退场动画收起（插桩：错误带回主进程打印）
    const fdOpen = await win.webContents.executeJavaScript(`(() => {
      try {
        const btn = document.querySelector(".fd-trigger");
        if (!btn) return { err: "no-trigger" };
        btn.click();
        return { ok: true };
      } catch (e) {
        return { err: String(e && e.stack || e) };
      }
    })()`);
    log("fd-open", JSON.stringify(fdOpen));
    const fd = await win.webContents.executeJavaScript(`(() => ({
      menu: !!document.querySelector(".fd-menu"),
      items: document.querySelectorAll(".fd-item").length,
    }))()`);
    log("agents-select", JSON.stringify(fd));
    assert.ok(fdOpen.ok, "agents: 模型下拉应可打开 " + JSON.stringify(fdOpen));
    assert.ok(fd.menu && fd.items > 0, "agents: 模型下拉应有选项");
    await win.webContents.executeJavaScript(`document.querySelector(".fd-item").click()`);
    await new Promise((r) => setTimeout(r, 450));
    const fdClosed = await win.webContents.executeJavaScript(`!document.querySelector(".fd-menu")`);
    assert.ok(fdClosed, "agents: 选择后下拉应收起");
    await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector('[data-ag="cancel"]');
      if (!btn) throw new Error("取消按钮缺失");
      btn.click();
    })()`);
    const backToRoster = await win.webContents.executeJavaScript(
      `!!document.querySelector(".ag-grid") && !document.querySelector(".ag-form")`,
    );
    assert.ok(backToRoster, "agents: 取消应回到名单");

    // 目录管理页：导航进入 → 页签与条目卡 → 点卡片开文档弹窗 → 切页签
    await win.webContents.executeJavaScript(`document.querySelector('[data-nav="catalog"]').click()`);
    const cat = await win.webContents.executeJavaScript(`(() => {
      const r = (s) => {
        const el = document.querySelector(s);
        if (!el) return null;
        const b = el.getBoundingClientRect();
        return { w: b.width, h: b.height };
      };
      return {
        page: r(".cg-page"),
        grid: r(".cg-grid"),
        cards: document.querySelectorAll(".cg-card").length,
        navOn: !!document.querySelector('.nav-item.on[data-nav="catalog"]'),
      };
    })()`);
    log("catalog-page", JSON.stringify(cat));
    assert.ok(cat.page && cat.page.h > 300, "catalog: 目录页应可见");
    assert.ok(cat.cards >= 10, "catalog: 工具页签应有 10 个条目");
    assert.ok(cat.navOn, "catalog: 导航应高亮");
    // 工具导入：坏 JSON → 错误内联；合法 JSON → 入目录（10→11）→ 两步删除回 10
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="import"]').click()`);
    const importDlg = await win.webContents.executeJavaScript(`(() => {
      return { dialog: !!document.querySelector(".ag-doc"), input: !!document.querySelector(".ti-input") };
    })()`);
    log("tool-import-dialog", JSON.stringify(importDlg));
    assert.ok(importDlg.dialog && importDlg.input, "catalog: 导入弹窗应有粘贴区");
    await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector(".ti-input");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(el, '{"version": 1, "tools": []}');
      el.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="do-import"]').click()`);
    const importErr = await win.webContents.executeJavaScript(`(() => {
      return { warn: !!document.querySelector(".ag-doc-body .ag-warn") };
    })()`);
    log("tool-import-invalid", JSON.stringify(importErr));
    assert.ok(importErr.warn, "catalog: 空 tools 数组应报错");
    await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector(".ti-input");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(el, '{"version":1,"tools":[{"id":"smoke-tool","desc":"冒烟导入工具","risk":"low","source":"binary"}]}');
      el.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="do-import"]').click()`);
    const afterImport = await win.webContents.executeJavaScript(`(() => {
      return {
        cards: document.querySelectorAll(".cg-card").length,
        del: !!document.querySelector('[data-cg="del"]'),
      };
    })()`);
    log("tool-imported", JSON.stringify(afterImport));
    assert.strictEqual(afterImport.cards, 11, "catalog: 导入后工具应有 11 个条目");
    assert.ok(afterImport.del, "catalog: 导入条目应有删除入口");
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    const afterToolDel = await win.webContents.executeJavaScript(`document.querySelectorAll(".cg-card").length`);
    log("tool-import-deleted", afterToolDel);
    assert.strictEqual(afterToolDel, 10, "catalog: 删除导入条目后应回到 10 个");
    await win.webContents.executeJavaScript(`document.querySelector(".cg-card").click()`);
    const catDoc = await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector(".ag-doc");
      return el ? { h: el.getBoundingClientRect().height, params: el.querySelectorAll(".ag-param").length } : null;
    })()`);
    log("catalog-doc", JSON.stringify(catDoc));
    assert.ok(catDoc && catDoc.h > 150, "catalog: 点卡片应开文档弹窗");
    await win.webContents.executeJavaScript(`document.querySelector(".ag-doc-close").click()`);
    // 切到模板页签：条目数变化 + 重播交错入场
    await win.webContents.executeJavaScript(`(() => {
      const btns = document.querySelectorAll(".cg-tabs .seg-btn");
      if (btns.length < 3) throw new Error("目录页签缺失");
      btns[2].click();
    })()`);
    const catTpl = await win.webContents.executeJavaScript(`document.querySelectorAll(".cg-card").length`);
    log("catalog-templates", catTpl);
    assert.strictEqual(catTpl, 3, "catalog: 模板页签应有 3 个条目");
    // 自建模板全链路：新建 → 编写器 → 填 id → 保存 → 列表出现 → 两步删除（插桩：错误带回）
    const newClick = await win.webContents.executeJavaScript(`(() => {
      try {
        const btn = document.querySelector('[data-cg="new"]');
        if (!btn) return { err: "no-btn", tab: document.querySelector(".cg-tabs .seg-btn.on")?.textContent };
        btn.click();
        return { ok: true };
      } catch (e) {
        return { threw: String(e && e.stack || e).slice(0, 400) };
      }
    })()`);
    log("module-new-click", JSON.stringify(newClick));
    assert.ok(newClick.ok, "catalog: 新建入口应可点击 " + JSON.stringify(newClick));
    const modEd = await win.webContents.executeJavaScript(`(() => {
      return {
        form: !!document.querySelector(".ag-form"),
        idInput: !!document.querySelector("#cg-mod-id"),
        save: !!document.querySelector('[data-cg="save"]'),
      };
    })()`);
    log("module-editor", JSON.stringify(modEd));
    assert.ok(modEd.form && modEd.idInput && modEd.save, "catalog: 新建应打开模块编写器");
    await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector("#cg-mod-id");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, "smoke-template");
      el.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    // 摘要也是必填（chips 的 tooltip——Agent 靠它判断何时用）
    await win.webContents.executeJavaScript(`(() => {
      const el = [...document.querySelectorAll("input.fd-input")].find((i) => i.placeholder.includes("Agent 靠它"));
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, "冒烟模板——校验自建链路");
      el.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="save"]').click()`);
    const afterCreate = await win.webContents.executeJavaScript(`(() => {
      return {
        cards: document.querySelectorAll(".cg-card").length,
        hasCustom: !!document.querySelector('.cg-card [data-cg="del"]'),
      };
    })()`);
    log("module-created", JSON.stringify(afterCreate));
    assert.strictEqual(afterCreate.cards, 4, "catalog: 保存后模板应有 4 个条目");
    assert.ok(afterCreate.hasCustom, "catalog: 自建条目应有删除入口");
    await win.webContents.executeJavaScript(`(() => {
      const del = document.querySelector('[data-cg="del"]');
      if (!del) throw new Error("删除入口缺失");
      del.click();
    })()`);
    // 两步确认必须分两个任务（同步双击会被 React 批成一次更新——第二次点击看不到 confirming）
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    const afterDelete = await win.webContents.executeJavaScript(`document.querySelectorAll(".cg-card").length`);
    log("module-deleted", afterDelete);
    assert.strictEqual(afterDelete, 3, "catalog: 两步删除后应回到 3 个条目");
    // 模块导入：入口 → 弹窗 → 填 JSON → 导入 → 列表出现（3→4）→ 删除回 3
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="import-modules"]').click()`);
    const modImportDlg = await win.webContents.executeJavaScript(`(() => {
      const dlg = document.querySelector(".ag-doc");
      return dlg ? { title: dlg.querySelector(".ag-doc-title")?.textContent, input: !!dlg.querySelector(".ti-input"), file: !!dlg.querySelector('[data-cg="pick-file"]') } : null;
    })()`);
    log("module-import-dialog", JSON.stringify(modImportDlg));
    assert.ok(modImportDlg && modImportDlg.input && modImportDlg.file, "catalog: 模块导入弹窗应有粘贴区与文件选择");
    await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector(".ag-doc .ti-input");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(el, '{"version":1,"modules":[{"id":"smoke-module","desc":"冒烟导入","kind":"process","body":"# 冒烟"}]}');
      el.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    await win.webContents.executeJavaScript(`document.querySelector(".ag-doc [data-cg=\\"do-import\\"]").click()`);
    const afterModImport = await win.webContents.executeJavaScript(`(() => {
      return {
        cards: document.querySelectorAll(".cg-card").length,
        dlgClosed: !document.querySelector(".ag-doc"),
      };
    })()`);
    log("module-imported", JSON.stringify(afterModImport));
    assert.strictEqual(afterModImport.cards, 4, "catalog: 模块导入后应有 4 个条目");
    assert.ok(afterModImport.dlgClosed, "catalog: 导入后弹窗应收起");
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    const afterModDel = await win.webContents.executeJavaScript(`document.querySelectorAll(".cg-card").length`);
    log("module-import-deleted", afterModDel);
    assert.strictEqual(afterModDel, 3, "catalog: 删除导入模块后应回到 3 个条目");

    assert.deepEqual(errors, [], '页面不应出现控制台错误');
    log('PASS: desktop / mobile / shell layout');
    finish(0);
  } catch (error) {
    log(String(error && error.stack || error));
    finish(1);
  }
});
