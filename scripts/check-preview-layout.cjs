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

// 全局兜底超时（含 dispatch 全链路冒烟 ~40s——纯结构检查时代是 30s）
const timeout = setTimeout(() => { log('预览布局检查超时'); app.exit(1); }, 120000);
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
      const ts = document.querySelector(".thread-scroll");
      const main = document.querySelector(".main");
      const cz = document.querySelector(".composer-zone");
      const bar = document.querySelector(".tabbar");
      const app = document.querySelector(".app");
      const r2 = (s) => { const e = document.querySelector(s); if (!e) return null; const b = e.getBoundingClientRect(); return { top: Math.round(b.top), bottom: Math.round(b.bottom), h: Math.round(b.height) }; };
      return {
        top: Math.round(r.top),
        bottom: Math.round(r.bottom),
        cards: document.querySelectorAll(".suggest-card").length,
        agentChip: !!document.querySelector(".empty-agent"),
        // 诊断：空态居中依赖线程区 padding-bottom（= 输入区实测高度）
        win: { w: window.innerWidth, h: window.innerHeight },
        app: r2(".app"),
        topbar: r2(".topbar"),
        main: r2(".main"),
        mainChildren: main ? [...main.children].map((c) => c.className) : null,
        tsH: ts ? Math.round(ts.getBoundingClientRect().height) : null,
        tsPadBottom: ts ? getComputedStyle(ts).paddingBottom : null,
        composerH: cz ? Math.round(cz.getBoundingClientRect().height) : null,
        composerVar: main ? main.style.getPropertyValue("--composer-h") : null,
        barTop: bar ? Math.round(bar.getBoundingClientRect().top) : null,
      };
    })()`);
    log("empty-state", JSON.stringify(empty));
    assert.ok(empty && empty.cards === 4, "chat: 空态应有 4 张建议卡");
    assert.ok(empty && empty.agentChip, "chat: 空态应显示当前 Agent 芯片");
    assert.ok(empty && empty.top > 100 && empty.bottom < 900, "chat: 空态应纵向居中（不贴顶）");
    // 侧栏范围：启动即「未分组」（用户拍板——打开软件就是空会话，没有「全部」
    // 视图），点项目行切换且**不可再点取消**（再点仍是该项目）。
    // 注意：这一段只点项目行，不打开会话（后面的空态/建议卡用例依赖对话区仍空）。
    const sidebarBoot = await win.webContents.executeJavaScript(`(() => {
      const rows = [...document.querySelectorAll(".session-item")];
      return {
        rows: rows.length,
        looseActive: !!document.querySelector(".proj-row.loose.active"),
        projActive: document.querySelectorAll(".proj-row.active:not(.loose)").length,
        chip: !!document.querySelector(".group-filter"),
        subs: document.querySelectorAll(".session-item .session-sub").length,
      };
    })()`);
    log("sidebar-scope", JSON.stringify(sidebarBoot));
    assert.ok(sidebarBoot.looseActive, "sidebar: 启动态应选中「未分组」");
    assert.equal(sidebarBoot.projActive, 0, "sidebar: 启动态不应有项目被选中");
    assert.ok(!sidebarBoot.chip, "sidebar: 不应再有可清除的范围 chip（没有「全部」）");
    assert.equal(sidebarBoot.subs, 0, "sidebar: 会话行不应再带归属标签（范围恒唯一）");

    const sidebarScoped = await win.webContents.executeJavaScript(`(() => {
      const proj = document.querySelector(".proj-row:not(.loose)");
      if (proj) proj.click();
      return new Promise((res) => setTimeout(() => res({
        active: document.querySelectorAll(".proj-row.active").length,
        looseActive: !!document.querySelector(".proj-row.loose.active"),
        rows: document.querySelectorAll(".session-item").length,
      }), 250));
    })()`);
    log("sidebar-scoped", JSON.stringify(sidebarScoped));
    assert.equal(sidebarScoped.active, 1, "sidebar: 点项目行后应恰好一个范围选中");
    assert.ok(!sidebarScoped.looseActive, "sidebar: 选中项目后「未分组」不再选中");

    // 再点同一个项目行：不允许取消（仍是该项目选中）
    const sidebarAgain = await win.webContents.executeJavaScript(`(() => {
      const proj = document.querySelector(".proj-row.active:not(.loose)") || document.querySelector(".proj-row:not(.loose)");
      if (proj) proj.click();
      return new Promise((res) => setTimeout(() => res({
        active: document.querySelectorAll(".proj-row.active:not(.loose)").length,
        looseActive: !!document.querySelector(".proj-row.loose.active"),
      }), 250));
    })()`);
    log("sidebar-again", JSON.stringify(sidebarAgain));
    assert.equal(sidebarAgain.active, 1, "sidebar: 再点已选中的项目行不应取消（必须仍有选中）");
    assert.ok(!sidebarAgain.looseActive, "sidebar: 再点项目行不应退回未分组");

    // 回到「未分组」范围（点未分组行，同样是单向选择）
    const sidebarLoose = await win.webContents.executeJavaScript(`(() => {
      const loose = document.querySelector(".proj-row.loose");
      if (loose) loose.click();
      return new Promise((res) => setTimeout(() => res({
        looseActive: !!document.querySelector(".proj-row.loose.active"),
        projActive: document.querySelectorAll(".proj-row.active:not(.loose)").length,
      }), 250));
    })()`);
    log("sidebar-loose", JSON.stringify(sidebarLoose));
    assert.ok(sidebarLoose.looseActive, "sidebar: 点未分组行应选中未分组");
    assert.equal(sidebarLoose.projActive, 0, "sidebar: 选未分组后项目不再选中");
    // 斜杠命令面板：输入 / → 弹出 → 过滤（/set）→ 键盘导航 → 选中进设置 → 回来
    await win.webContents.executeJavaScript(`(() => {
      const ta = document.querySelector(".piInput");
      if (!ta) throw new Error("输入框缺失");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(ta, "/");
      ta.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    const slashAll = await win.webContents.executeJavaScript(`(() => ({
      palette: !!document.querySelector(".slash-palette"),
      items: document.querySelectorAll(".cmd-item").length,
      first: document.querySelector(".cmd-item .cmd-name")?.textContent,
    }))()`);
    log("slash-open", JSON.stringify(slashAll));
    assert.ok(slashAll.palette && slashAll.items >= 4 && slashAll.first === "/new", "chat: 输入 / 应弹出命令面板（含 /new）");
    // 关键字过滤
    await win.webContents.executeJavaScript(`(() => {
      const ta = document.querySelector(".piInput");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(ta, "/set");
      ta.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    const slashFilter = await win.webContents.executeJavaScript(`(() => ({
      items: document.querySelectorAll(".cmd-item").length,
      onlySettings: [...document.querySelectorAll(".cmd-item .cmd-name")].every((n) => n.textContent.includes("settings")),
    }))()`);
    log("slash-filter", JSON.stringify(slashFilter));
    assert.ok(slashFilter.items === 1 && slashFilter.onlySettings, "chat: /set 应只匹配 settings");
    // 键盘：Enter 选中 → 设置面板打开
    await win.webContents.executeJavaScript(`(() => {
      const ev = new KeyboardEvent("keydown", { key: "Enter", bubbles: true });
      window.dispatchEvent(ev);
    })()`);
    const settingsOpened = await win.webContents.executeJavaScript(`!!document.querySelector(".settings-view")`);
    log("slash-picked", settingsOpened);
    assert.ok(settingsOpened, "chat: Enter 应选中命令（打开设置）");
    await win.webContents.executeJavaScript(`document.querySelector(".settings-back").click()`);
    // Esc 关闭路径：再开面板 → 清空关闭
    await win.webContents.executeJavaScript(`(() => {
      const ta = document.querySelector(".piInput");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(ta, "/");
      ta.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    await win.webContents.executeJavaScript(`(() => {
      const ev = new KeyboardEvent("keydown", { key: "Escape", bubbles: true });
      window.dispatchEvent(ev);
    })()`);
    const slashClosed = await win.webContents.executeJavaScript(`(() => ({
      palette: !!document.querySelector(".slash-palette"),
      input: document.querySelector(".piInput")?.value,
    }))()`);
    log("slash-escaped", JSON.stringify(slashClosed));
    assert.ok(!slashClosed.palette && slashClosed.input === "", "chat: Esc 应关闭面板并清输入");
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

    // 主 Agent 调度子 Agent（dispatch 卡——M3 形态）：发消息 → 确认门放行
    // → dispatchStart → 子执行挂卡内 → dispatchEnd 定格（demo 时间轴全程 ~22s）
    await win.webContents.executeJavaScript(`(() => {
      const ta = document.querySelector(".piInput");
      if (!ta) throw new Error("输入框缺失");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(ta, "给会话历史加个超时兜底");
      ta.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    await win.webContents.executeJavaScript(`document.querySelector(".send-btn").click()`);
    // 发送后的状态探针（8s：reasoning/工具应已出现——确认门 ~10.4s）
    await new Promise((r) => setTimeout(r, 8000));
    const sentState = await win.webContents.executeJavaScript(`(() => ({
      userMsg: !!document.querySelector(".msg.user .bubble"),
      busy: !!document.querySelector(".busy-row"),
      trows: document.querySelectorAll(".trow").length,
      reasoning: document.querySelectorAll(".msg").length,
    }))()`);
    log("dispatch-send-state", JSON.stringify(sentState));
    // 等确认卡（demo ~12.2s 到 bash 确认门）→ 点允许（CSS module 类名
    // 是哈希——定位用 data-variant 语义锚点；按钮组内第二个 = 允许）。
    // 断言「卡在 dispatch 卡内」是本段的核心回归守卫：子 Agent 的确认若落到
    // 外层时间线（旧后端不发 dispatch_id / 归属逻辑坏），全局选择器照样点得到，
    // 测试会假绿——必须用 closest('.dispatch-card') 判作用域。
    let confirmScope = null;
    for (let i = 0; i < 120 && !confirmScope; i++) {
      await new Promise((r) => setTimeout(r, 250));
      confirmScope = await win.webContents.executeJavaScript(`(() => {
        const card = document.querySelector('[data-variant="command"]:not([data-resolved])');
        if (!card) return null;
        return { inDispatch: !!card.closest('.dispatch-card'), scope: card.closest('.dispatch-card') ? 'dispatch' : 'main' };
      })()`);
    }
    log("dispatch-confirm-shown", JSON.stringify(confirmScope));
    assert.ok(confirmScope && confirmScope.inDispatch, `chat: 子 Agent 的确认卡必须落在 dispatch 卡内（实际 ${JSON.stringify(confirmScope)}）`);
    await win.webContents.executeJavaScript(`(() => {
      const card = document.querySelector('.dispatch-card');
      const btn = card && card.querySelector('[data-variant="command"]:not([data-resolved]) button:nth-of-type(2)');
      if (btn) btn.click();
    })()`);
    // 等 dispatch 卡（确认后 ~7s）
    let dispatch = null;
    for (let i = 0; i < 60 && !dispatch; i++) {
      await new Promise((r) => setTimeout(r, 250));
      dispatch = await win.webContents.executeJavaScript(`(() => {
        const card = document.querySelector(".dispatch-card");
        return card ? { agent: card.querySelector(".dispatch-agent")?.textContent, running: card.getAttribute("data-done") !== "true" } : null;
      })()`);
    }
    log("dispatch-card", JSON.stringify(dispatch));
    assert.ok(dispatch && dispatch.agent === "代码 Agent", "chat: 主 Agent 应派发 dispatch 卡（代码 Agent）");
    // 等 dispatchEnd（卡定格带结果）。完成后卡自动折叠（只留结果）——子块要
    // 展开才在 DOM 里（.dispatch-body 仅在 expanded 时渲染），故先点卡头展开
    // 再断言：结果 + 子执行块 + **无僵尸行**（result 未回填的工具行 = 用户报的
    // 「一直执行中」——toolResult 按 id 只回填第一条，同 id 重复行会残留）
    let done = null;
    for (let i = 0; i < 60 && !done; i++) {
      await new Promise((r) => setTimeout(r, 250));
      done = await win.webContents.executeJavaScript(`(() => {
        const card = document.querySelector('.dispatch-card[data-done="true"]');
        if (!card) return null;
        // 完成后默认收起（含最终结果——结果也在折叠区内，卡片必须收短）
        return {
          result: !!card.querySelector(".dispatch-result"),
          body: !!card.querySelector(".dispatch-body"),
          chev: !!card.querySelector(".dispatch-chev"),
        };
      })()`);
    }
    log("dispatch-done", JSON.stringify(done));
    assert.ok(done && done.chev, `chat: dispatch 完成后应可展开（有结果就有箭头）（${JSON.stringify(done)}）`);
    assert.ok(!done.body && !done.result, `chat: dispatch 完成后应默认收起（结果与子过程都在折叠区内）（${JSON.stringify(done)}）`);
    // 展开：子过程与最终结果都要出现（用户报告「自动收缩和手动都收不掉最终结果」）
    const expanded = await win.webContents.executeJavaScript(`(() => {
      const card = document.querySelector('.dispatch-card[data-done="true"]');
      const head = card && card.querySelector('.dispatch-head');
      if (head) head.click();
      return new Promise((res) => setTimeout(() => res({
        body: !!card.querySelector(".dispatch-body"),
        result: !!card.querySelector(".dispatch-result"),
        resultText: (card.querySelector(".dispatch-result")?.textContent ?? "").trim().length,
      }), 400));
    })()`);
    log("dispatch-expanded", JSON.stringify(expanded));
    assert.ok(expanded.body && expanded.result, `chat: 展开后应见子过程与最终结果（${JSON.stringify(expanded)}）`);
    assert.ok(expanded.resultText > 0, "chat: 展开后的最终结果不应为空");
    // 手动再收起：结果必须跟着收掉（这就是用户报告的那个 bug）
    const recollapsed = await win.webContents.executeJavaScript(`(() => {
      const card = document.querySelector('.dispatch-card[data-done="true"]');
      const head = card && card.querySelector('.dispatch-head');
      if (head) head.click();
      return new Promise((res) => setTimeout(() => res({
        body: !!card.querySelector(".dispatch-body"),
        result: !!card.querySelector(".dispatch-result"),
      }), 400));
    })()`);
    log("dispatch-recollapsed", JSON.stringify(recollapsed));
    assert.ok(!recollapsed.body && !recollapsed.result, `chat: 手动收起必须把结果一起收掉（${JSON.stringify(recollapsed)}）`);
    // 再展开回展开态，供后面的子块断言使用
    await win.webContents.executeJavaScript(`(() => {
      const card = document.querySelector('.dispatch-card[data-done="true"]');
      const head = card && card.querySelector('.dispatch-head');
      if (head) head.click();
    })()`);
    await new Promise((r) => setTimeout(r, 400));
    const subs = await win.webContents.executeJavaScript(`(() => {
      const card = document.querySelector('.dispatch-card[data-done="true"]');
      if (!card) return null;
      const body = card.querySelector('.dispatch-body');
      return {
        subBlocks: body ? body.children.length : 0,
        tools: card.querySelectorAll('.dispatch-body .trow').length,
        running: card.querySelectorAll('.dispatch-body .trow-running').length,
      };
    })()`);
    log("dispatch-subs", JSON.stringify(subs));
    assert.ok(subs && subs.subBlocks >= 2, `chat: dispatch 展开后应见子执行块（${JSON.stringify(subs)}）`);
    assert.equal(subs.running, 0, `chat: dispatch 完成后不应有「执行中」僵尸行（${JSON.stringify(subs)}）`);
    assert.ok(subs.tools >= 1, `chat: 子执行应含工具行（${JSON.stringify(subs)}）`);
    // 等本轮完全结束（streamAnswer + done——demo 计时器不停，中途清屏会串台）
    for (let i = 0; i < 80; i++) {
      const idle = await win.webContents.executeJavaScript(`!document.querySelector(".busy-row")`);
      if (idle) break;
      await new Promise((r) => setTimeout(r, 250));
    }
    // 回到空态（新会话清屏——后续场景不受本轮影响）
    await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector(".nav-item");
      if (btn) btn.click();
    })()`);
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
    assert.ok(cat.cards >= 12, "catalog: 工具页签应有 12 个条目");
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
    assert.strictEqual(afterImport.cards, 13, "catalog: 导入后工具应有 13 个条目");
    assert.ok(afterImport.del, "catalog: 导入条目应有删除入口");
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    const afterToolDel = await win.webContents.executeJavaScript(`document.querySelectorAll(".cg-card").length`);
    log("tool-import-deleted", afterToolDel);
    assert.strictEqual(afterToolDel, 12, "catalog: 删除导入条目后应回到 12 个");
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

    // 工具编写器（表单生成，不写 JSON）：切回工具页签 → 新建 → 表单 → 保存（10→11）→ 删除回 10
    await win.webContents.executeJavaScript(`(() => {
      const btns = document.querySelectorAll(".cg-tabs .seg-btn");
      btns[0].click();
    })()`);
    await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector('[data-cg="new"]');
      if (!btn) throw new Error("新建工具入口缺失");
      btn.click();
    })()`);
    const toolEd = await win.webContents.executeJavaScript(`(() => {
      return {
        form: !!document.querySelector(".ag-form"),
        idInput: !!document.querySelector("#cg-tool-id"),
        save: !!document.querySelector('[data-cg="save"]'),
      };
    })()`);
    log("tool-editor", JSON.stringify(toolEd));
    assert.ok(toolEd.form && toolEd.idInput && toolEd.save, "catalog: 新建应打开工具编写器");
    await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector("#cg-tool-id");
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, "smoke-form-tool");
      el.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    await win.webContents.executeJavaScript(`(() => {
      const el = [...document.querySelectorAll("input.fd-input")].find((i) => i.placeholder.includes("一句话说明"));
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, "表单生成的冒烟工具");
      el.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    // 运行命令必填（可执行载体）——按占位符匹配
    await win.webContents.executeJavaScript(`(() => {
      const el = [...document.querySelectorAll("input.fd-input")].find((i) => i.placeholder.includes("参数用 {名称} 占位"));
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
      setter.call(el, "smoke-tool {input}");
      el.dispatchEvent(new Event("input", { bubbles: true }));
    })()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="save"]').click()`);
    const afterToolCreate = await win.webContents.executeJavaScript(`(() => {
      return {
        cards: document.querySelectorAll(".cg-card").length,
        dlgClosed: !document.querySelector(".ag-doc"),
      };
    })()`);
    log("tool-form-created", JSON.stringify(afterToolCreate));
    assert.strictEqual(afterToolCreate.cards, 13, "catalog: 表单保存后工具应有 13 个条目");
    assert.ok(afterToolCreate.dlgClosed, "catalog: 保存后编写器应收起");
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    const afterToolFormDel = await win.webContents.executeJavaScript(`document.querySelectorAll(".cg-card").length`);
    log("tool-form-deleted", afterToolFormDel);
    assert.strictEqual(afterToolFormDel, 12, "catalog: 删除表单工具后应回到 12 个条目");

    // MCP 配置导入：MCP 页签 → 导入配置 → 粘贴 YAML → 导入（4→5）→ 删除回 4
    const mcTab = await win.webContents.executeJavaScript(`(() => {
      const btns = document.querySelectorAll(".cg-tabs .seg-btn");
      if (btns.length < 4) return { err: "seg-不足", n: btns.length };
      btns[3].click();
      return { ok: true, tab: btns[3].textContent };
    })()`);
    log("mc-tab", JSON.stringify(mcTab));
    assert.ok(mcTab.ok, "catalog: MCP 页签应存在 " + JSON.stringify(mcTab));
    const mcBtn = await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector('[data-cg="import-mc"]');
      if (!btn) return { err: "no-btn" };
      btn.click();
      return { ok: true };
    })()`);
    log("mc-import-open", JSON.stringify(mcBtn));
    assert.ok(mcBtn.ok, "catalog: 导入配置入口应可点击");
    const mcDlg = await win.webContents.executeJavaScript(`(() => {
      const doc = document.querySelector(".ag-doc");
      if (!doc) return { err: "no-dialog" };
      return {
        dlg: true,
        input: !!doc.querySelector(".ti-input"),
        file: !!doc.querySelector('[data-cg="pick-file"]'),
      };
    })()`);
    log("mc-import-dialog", JSON.stringify(mcDlg));
    assert.ok(mcDlg.dlg && mcDlg.input && mcDlg.file, "catalog: 配置导入弹窗应有粘贴区与文件选择 " + JSON.stringify(mcDlg));
    const mcFill = await win.webContents.executeJavaScript(`(() => {
      const el = document.querySelector(".ag-doc .ti-input");
      if (!el) return { err: "no-input" };
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value").set;
      setter.call(el, "mcpServers:\\n  smoke-mcp:\\n    command: node\\n    args: [\\"server.js\\"]");
      el.dispatchEvent(new Event("input", { bubbles: true }));
      return { ok: true, len: el.value.length };
    })()`);
    log("mc-fill", JSON.stringify(mcFill));
    assert.ok(mcFill.ok, "catalog: 粘贴区应可填充 " + JSON.stringify(mcFill));
    const mcDo = await win.webContents.executeJavaScript(`(() => {
      const b = document.querySelector('.ag-doc [data-cg="do-import"]');
      if (!b) return { err: "no-btn" };
      if (b.disabled) return { err: "disabled" };
      b.click();
      return { ok: true };
    })()`);
    log("mc-do", JSON.stringify(mcDo));
    assert.ok(mcDo.ok, "catalog: 导入按钮应可点击 " + JSON.stringify(mcDo));
    const mcImported = await win.webContents.executeJavaScript(`(() => {
      return {
        cards: document.querySelectorAll(".cg-card").length,
        dlgClosed: !document.querySelector(".ag-doc"),
      };
    })()`);
    log("mc-imported", JSON.stringify(mcImported));
    assert.strictEqual(mcImported.cards, 5, "catalog: 配置导入后 MCP 应有 5 个服务器");
    assert.ok(mcImported.dlgClosed, "catalog: 全新导入（无跳过）应直接收起");
    const mcDeleted0 = await win.webContents.executeJavaScript(`document.querySelectorAll(".cg-card").length`);
    log("mc-import-check", mcDeleted0);
    // React 提交 + gsap 入场后的短暂间隔（与探针一致——立即查询偶发取不到操作区）
    await new Promise((r) => setTimeout(r, 250));
    const mcDel1 = await win.webContents.executeJavaScript(`(() => {
      const del = document.querySelector('[data-cg="del"]');
      if (!del) return { err: "no-del", customs: [...document.querySelectorAll(".cg-card-title")].map((t) => t.textContent).join(",") };
      del.click();
      return { ok: true };
    })()`);
    log("mc-del-1", JSON.stringify(mcDel1));
    assert.ok(mcDel1.ok, "catalog: 导入条目应有删除入口 " + JSON.stringify(mcDel1));
    await win.webContents.executeJavaScript(`document.querySelector('[data-cg="del"]').click()`);
    const mcDeleted = await win.webContents.executeJavaScript(`document.querySelectorAll(".cg-card").length`);
    log("mc-import-deleted", mcDeleted);
    assert.strictEqual(mcDeleted, 4, "catalog: 删除导入服务器后应回到 4 个");

    // 连接管理：顶栏指示器 → 弹窗 → 远程访问开关 → 地址/token 展示 → 切换远程 → 添加表单
    const connPill = await win.webContents.executeJavaScript(`(() => {
      const pill = document.querySelector('[data-conn="pill"]');
      if (!pill) return { err: "no-pill" };
      return { ok: true, name: pill.textContent.trim() };
    })()`);
    log("conn-pill", JSON.stringify(connPill));
    assert.ok(connPill.ok, "topbar: 连接指示器应存在 " + JSON.stringify(connPill));
    assert.ok(connPill.name.includes("本机"), "topbar: 默认连接应显示本机");
    await win.webContents.executeJavaScript(`document.querySelector('[data-conn="pill"]').click()`);
    const connMgr = await win.webContents.executeJavaScript(`(() => {
      const doc = document.querySelector(".ag-doc");
      if (!doc) return { err: "no-dialog" };
      return {
        title: doc.querySelector(".ag-doc-title")?.textContent,
        local: doc.textContent.includes("127.0.0.1:7789"),
        ra: !!doc.querySelector(".toggle"),
      };
    })()`);
    log("conn-dialog", JSON.stringify(connMgr));
    assert.ok(connMgr.title === "连接" && connMgr.local, "topbar: 连接弹窗应含本机 " + JSON.stringify(connMgr));
    // 开启远程访问 → 地址与 token 展示（遮罩）
    await win.webContents.executeJavaScript(`(() => {
      const t = document.querySelector(".ag-doc .toggle");
      if (t) t.click();
    })()`);
    const ra = await win.webContents.executeJavaScript(`(() => {
      const panel = document.querySelector(".conn-ra-panel");
      return panel ? {
        addr: panel.textContent.includes("192.168.1.105:7789"),
        tokenMasked: !!panel.textContent.match(/[0-9a-f]{4}••••[0-9a-f]{4}/),
        copyBtns: panel.querySelectorAll(".conn-copy").length,
      } : { err: "no-panel" };
    })()`);
    log("conn-remote-access", JSON.stringify(ra));
    assert.ok(!ra.err && ra.addr && ra.tokenMasked && ra.copyBtns >= 2, "topbar: 远程访问应展示地址与遮罩 token " + JSON.stringify(ra));
    // 切换到远程连接（演示种子「公司开发机」）→ 指示器名称跟随
    const connSwitch = await win.webContents.executeJavaScript(`(() => {
      const btn = document.querySelector('[data-conn="connect"]');
      if (!btn) return { err: "no-connect-btn", text: document.querySelector(".ag-doc")?.textContent.slice(0, 80) };
      btn.click();
      return { ok: true };
    })()`);
    log("conn-switch-click", JSON.stringify(connSwitch));
    assert.ok(connSwitch.ok, "topbar: 远程连接应有连接按钮 " + JSON.stringify(connSwitch));
    const pillAfter = await win.webContents.executeJavaScript(`(() => {
      return {
        name: document.querySelector('[data-conn="pill"]')?.textContent.trim(),
        dialogTag: !!document.querySelector(".ag-doc .conn-tag"),
      };
    })()`);
    log("conn-switched", JSON.stringify(pillAfter));
    assert.ok(pillAfter.name.includes("公司开发机"), "topbar: 指示器应显示当前连接名 " + JSON.stringify(pillAfter));
    assert.ok(pillAfter.dialogTag, "topbar: 当前连接条目应有标记");
    // 断开回落本机 → 添加表单（名称/地址/token 三字段）
    await win.webContents.executeJavaScript(`document.querySelector('[data-conn="disconnect"]').click()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-conn="add"]').click()`);
    const connForm = await win.webContents.executeJavaScript(`(() => {
      return {
        fields: document.querySelectorAll(".ag-doc .fd-input").length,
        save: !!document.querySelector(".ag-doc [data-cg=save], .ag-doc .fd-btn-p"),
      };
    })()`);
    log("conn-add-form", JSON.stringify(connForm));
    assert.ok(connForm.fields >= 3, "topbar: 添加连接表单应有名称/地址/token 字段");
    await win.webContents.executeJavaScript(`document.querySelector(".ag-doc-close").click()`);

    // 樱花frp 公网穿透：登录（访问密钥）→ 账户流量展示 → 创建隧道 → 公网地址 → 断开 → 删除
    // （重新打开连接弹窗——remoteAccess 是 Provider 状态，前一步已开启，面板常驻）
    await win.webContents.executeJavaScript(`document.querySelector('[data-conn="pill"]').click()`);
    await new Promise((r) => setTimeout(r, 250));
    const skBefore = await win.webContents.executeJavaScript(`(() => {
      const doc = document.querySelector(".ag-doc");
      return doc ? { has: !!doc.querySelector(".conn-sakura") } : { err: "no-dialog" };
    })()`);
    log("sakura-block", JSON.stringify(skBefore));
    assert.ok(skBefore.has, "conn: 远程访问面板应含樱花frp 块");
    await win.webContents.executeJavaScript(`(() => {
      const input = document.querySelector(".conn-key-input");
      if (input) {
        const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
        setter.call(input, "demo-access-key");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
    })()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-conn="sakura-login"]').click()`);
    const skLogged = await win.webContents.executeJavaScript(`(() => {
      const blk = document.querySelector(".conn-sakura");
      return {
        loggedIn: blk?.getAttribute("data-on") === "true",
        traffic: !!blk?.textContent.match(/今日 .+ · 剩余 .+/),
      };
    })()`);
    log("sakura-logged", JSON.stringify(skLogged));
    assert.ok(skLogged.loggedIn && skLogged.traffic, "conn: 登录后应展示账户流量 " + JSON.stringify(skLogged));
    await win.webContents.executeJavaScript(`document.querySelector('[data-conn="tunnel-new"]').click()`);
    const skTunnel = await win.webContents.executeJavaScript(`(() => {
      const t = document.querySelector(".conn-tunnel");
      return t ? {
        name: t.querySelector(".conn-tunnel-name")?.textContent,
        addr: !!t.querySelector(".conn-cred-value.mono")?.textContent.match(/[a-z0-9.-]+[.]natfrp[.]io:\\d+/),
        online: t.getAttribute("data-online") === "true",
      } : { err: "no-tunnel" };
    })()`);
    log("sakura-tunnel", JSON.stringify(skTunnel));
    assert.ok(!skTunnel.err && skTunnel.addr && skTunnel.online, "conn: 创建后应有公网地址与在线态 " + JSON.stringify(skTunnel));
    await win.webContents.executeJavaScript(`document.querySelector('[data-conn="tunnel-toggle"]').click()`);
    const skOff = await win.webContents.executeJavaScript(`(() => {
      const t = document.querySelector(".conn-tunnel");
      return { online: t.getAttribute("data-online") === "true", label: t.querySelector('[data-conn="tunnel-toggle"]')?.textContent.trim() };
    })()`);
    log("sakura-tunnel-off", JSON.stringify(skOff));
    assert.ok(!skOff.online, "conn: 断开后隧道应离线");
    await win.webContents.executeJavaScript(`document.querySelector('[data-conn="tunnel-del"]').click()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-conn="tunnel-del"]').click()`);
    const skDeleted = await win.webContents.executeJavaScript(`(() => {
      return { empty: !!document.querySelector(".conn-sakura-empty") };
    })()`);
    log("sakura-tunnel-deleted", JSON.stringify(skDeleted));
    assert.ok(skDeleted.empty, "conn: 删除后应回到空态");

    // 更新提示（页面级 Toast + 侧栏角标 + 设置块）：自动检查发现新版本 →
    // Toast 弹出 → 立即更新（进度）→ 就绪 → 立即重启 → 侧栏回落 + 设置显示已是最新
    await new Promise((r) => setTimeout(r, 1800)); // 等 Provider 自动检查（1.2s 演示桩）
    const utShown = await win.webContents.executeJavaScript(`(() => {
      const t = document.querySelector(".upd-toast");
      return t ? {
        newVer: t.textContent.includes("0.2.0"),
        dl: !!t.querySelector('[data-ut="download"]'),
        badge: !!document.querySelector(".settings-upd-dot"),
        verLabel: document.querySelector(".settings-row .ver")?.textContent,
      } : { err: "no-toast", phase: document.querySelector(".set-update")?.getAttribute("data-phase") };
    })()`);
    log("update-toast", JSON.stringify(utShown));
    assert.ok(!utShown.err && utShown.newVer && utShown.dl, "toast: 自动检查后应弹更新提示 " + JSON.stringify(utShown));
    assert.ok(utShown.badge && utShown.verLabel === "可更新", "sidebar: 有更新时设置行应有角标与「可更新」");
    // Toast 立即更新 → 内联进度 → 就绪
    await win.webContents.executeJavaScript(`document.querySelector('[data-ut="download"]').click()`);
    await new Promise((r) => setTimeout(r, 400));
    const utDl = await win.webContents.executeJavaScript(`(() => {
      return {
        progress: !!document.querySelector(".upd-toast .set-update-progress-bar"),
        pct: document.querySelector(".upd-toast .upd-toast-title .mono")?.textContent,
      };
    })()`);
    log("update-toast-dl", JSON.stringify(utDl));
    assert.ok(utDl.progress, "toast: 下载中应有内联进度条");
    await new Promise((r) => setTimeout(r, 3000));
    const utReady = await win.webContents.executeJavaScript(`(() => {
      return { restart: !!document.querySelector('.upd-toast [data-ut="restart"]') };
    })()`);
    log("update-toast-ready", JSON.stringify(utReady));
    assert.ok(utReady.restart, "toast: 下载完成应进入就绪（重启入口）");
    await win.webContents.executeJavaScript(`document.querySelector('[data-ut="restart"]').click()`);
    const utAfter = await win.webContents.executeJavaScript(`(() => {
      return {
        toastGone: !document.querySelector(".upd-toast"),
        badgeGone: !document.querySelector(".settings-upd-dot"),
        ver: document.querySelector(".settings-row .ver")?.textContent,
      };
    })()`);
    log("update-toast-restarted", JSON.stringify(utAfter));
    assert.ok(utAfter.toastGone && utAfter.badgeGone && !utAfter.ver.includes("可更新"), "toast: 重启后提示与角标应消失");
    // 设置 · 通用：UpdateBlock 应显示「已是最新版本」
    await win.webContents.executeJavaScript(`document.querySelector(".settings-row").click()`);
    await new Promise((r) => setTimeout(r, 400));
    const suLatest = await win.webContents.executeJavaScript(`(() => {
      return { latest: document.querySelector(".set-update")?.textContent.includes("已是最新版本") };
    })()`);
    log("update-settings-latest", JSON.stringify(suLatest));
    assert.ok(suLatest.latest, "settings: 重启后设置应显示已是最新");
    await win.webContents.executeJavaScript(`document.querySelector(".settings-back").click()`);

    // 网页搜索：设置分区 → 渠道卡（Tavily 已配置为主渠道）→ Brave 配置
    // key（内联表单）→ 设为主渠道 → 添加自定义渠道
    await win.webContents.executeJavaScript(`document.querySelector(".settings-row").click()`);
    await new Promise((r) => setTimeout(r, 350));
    await win.webContents.executeJavaScript(`(() => {
      const btn = [...document.querySelectorAll(".settings-nav-item")].find((b) => b.textContent.trim() === "网页搜索");
      if (btn) btn.click();
    })()`);
    await new Promise((r) => setTimeout(r, 250));
    const spInit = await win.webContents.executeJavaScript(`(() => {
      const cards = [...document.querySelectorAll(".sp-card")];
      const tavily = cards.find((c) => c.textContent.includes("Tavily"));
      return {
        count: cards.length,
        tavilyPrimary: tavily?.getAttribute("data-primary") === "true",
        summary: document.querySelector(".sp-summary")?.textContent ?? "",
      };
    })()`);
    log("search-init", JSON.stringify(spInit));
    assert.ok(spInit.count >= 4 && spInit.tavilyPrimary, "settings: 搜索分区应有预设渠道且 Tavily 为主 " + JSON.stringify(spInit));
    // Brave 未配置 → 点「配置」→ 内联表单填 key → 保存 → 已配置
    await win.webContents.executeJavaScript(`(() => {
      const brave = [...document.querySelectorAll(".sp-card")].find((c) => c.textContent.includes("Brave"));
      const btn = brave?.querySelector('[data-sp="configure"]');
      if (btn) btn.click();
    })()`);
    const spEdit = await win.webContents.executeJavaScript(`(() => {
      const brave = [...document.querySelectorAll(".sp-card")].find((c) => c.textContent.includes("Brave"));
      return { form: !!brave?.querySelector(".sp-edit") };
    })()`);
    log("search-edit-open", JSON.stringify(spEdit));
    assert.ok(spEdit.form, "settings: 未配置渠道应可展开配置表单");
    await win.webContents.executeJavaScript(`(() => {
      const brave = [...document.querySelectorAll(".sp-card")].find((c) => c.textContent.includes("Brave"));
      const input = brave?.querySelector(".sp-edit .fd-input");
      if (input) {
        const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
        setter.call(input, "BSA-demo-key-12345");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
    })()`);
    await win.webContents.executeJavaScript(`(() => {
      const brave = [...document.querySelectorAll(".sp-card")].find((c) => c.textContent.includes("Brave"));
      brave?.querySelector('[data-sp="save"]')?.click();
    })()`);
    const spSaved = await win.webContents.executeJavaScript(`(() => {
      const brave = [...document.querySelectorAll(".sp-card")].find((c) => c.textContent.includes("Brave"));
      return {
        configured: brave?.getAttribute("data-configured") === "true",
        masked: !!brave?.querySelector(".sp-cred"),
        setPrimary: !!brave?.querySelector('[data-sp="set-primary"]'),
      };
    })()`);
    log("search-saved", JSON.stringify(spSaved));
    assert.ok(spSaved.configured && spSaved.masked && spSaved.setPrimary, "settings: 配置后应显示遮罩 key 与设为主渠道入口");
    // 设为主渠道 → Brave 变主渠道
    await win.webContents.executeJavaScript(`(() => {
      const brave = [...document.querySelectorAll(".sp-card")].find((c) => c.textContent.includes("Brave"));
      brave?.querySelector('[data-sp="set-primary"]')?.click();
    })()`);
    const spPrimary = await win.webContents.executeJavaScript(`(() => {
      const brave = [...document.querySelectorAll(".sp-card")].find((c) => c.textContent.includes("Brave"));
      return {
        primary: brave?.getAttribute("data-primary") === "true",
        summary: document.querySelector(".sp-summary-primary")?.textContent,
      };
    })()`);
    log("search-primary", JSON.stringify(spPrimary));
    assert.ok(spPrimary.primary && spPrimary.summary.includes("Brave"), "settings: 设为主渠道后摘要应跟随");
    // 添加自定义渠道
    await win.webContents.executeJavaScript(`document.querySelector('[data-sp="add"]').click()`);
    await win.webContents.executeJavaScript(`(() => {
      const form = document.querySelector(".sp-add-form");
      const inputs = form ? form.querySelectorAll(".fd-input") : [];
      if (inputs.length >= 2) {
        const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value").set;
        setter.call(inputs[0], "公司自建");
        inputs[0].dispatchEvent(new Event("input", { bubbles: true }));
        setter.call(inputs[1], "https://search.corp.example.com/api");
        inputs[1].dispatchEvent(new Event("input", { bubbles: true }));
      }
    })()`);
    await win.webContents.executeJavaScript(`document.querySelector('[data-sp="save-custom"]').click()`);
    const spCustom = await win.webContents.executeJavaScript(`(() => {
      const cards = [...document.querySelectorAll(".sp-card")];
      const corp = cards.find((c) => c.textContent.includes("公司自建"));
      return {
        added: !!corp,
        configured: corp?.getAttribute("data-configured") === "true",
        del: !!corp?.querySelector('[data-sp="del"]'),
      };
    })()`);
    log("search-custom", JSON.stringify(spCustom));
    assert.ok(spCustom.added && spCustom.configured && spCustom.del, "settings: 自定义渠道应可添加并带删除入口");
    await win.webContents.executeJavaScript(`document.querySelector(".settings-back").click()`);

    assert.deepEqual(errors, [], '页面不应出现控制台错误');
    log('PASS: desktop / mobile / shell layout');
    finish(0);
  } catch (error) {
    log(String(error && error.stack || error));
    finish(1);
  }
});
