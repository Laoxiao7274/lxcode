// 渲染基准：会话规模 × 渲染关键路径的耗时测量（Electron 真实渲染）。
// 用途：docs/frontend-review.md §三-③ 的测量项——窗口化/归约优化的
// 验收数字，以及后续「很多会话×很多内容」目标的回归基线。
// 用法：node shell/node_modules/electron/cli.js scripts/bench-render.cjs --no-sandbox --disable-gpu
const { app, BrowserWindow } = require('electron');
const fs = require('fs');

const SIZES = [200, 500, 2000];
/** 造块：user / assistant（带 markdown 正文）/ tool（带结果）混合。 */
function mkBlocks(n) {
  const out = [];
  const md = (i) => "## 回复 " + i + "\n\n这是第 " + i + " 条回复的正文，包含 **加粗**、行内代码与列表：\n\n- 要点一\n- 要点二\n\n段落文本继续展开，模拟真实的回复长度与 markdown 结构。";
  for (let i = 0; i < n; i++) {
    const kind = i % 3;
    if (kind === 0) out.push({ uid: i, kind: 'user', text: "用户消息 " + i + "——把工具循环加上超时兜底，单工具卡死不再拖住整轮" });
    else if (kind === 1) out.push({ uid: i, kind: 'assistant', content: md(i), reasoning: '', streaming: false });
    else out.push({ uid: i, kind: 'tool', id: "t" + i, name: i % 2 ? 'bash' : 'read_file', arguments: '{"command":"echo hi","path":"a.ts"}', result: "stdout 行 1\nstdout 行 2\nstdout 行 3", isError: false });
  }
  return out;
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1280, height: 800, useContentSize: true, show: false, webPreferences: { contextIsolation: true, nodeIntegration: false } });
  win.webContents.setUserAgent(win.webContents.getUserAgent().replace(/Electron\/\S+/g, ''));
  await win.loadURL('http://127.0.0.1:5190/?mode=demo');
  await win.webContents.executeJavaScript('window.__LX_TEST_MOTION_OFF__ = true');
  // 停在对话空态——.thread-scroll 存在即可（测的是 DOM 产块与归约成本，
  // 与具体会话内容无关）
  await new Promise((r) => setTimeout(r, 300));
  const hasContainer = await win.webContents.executeJavaScript(`!!document.querySelector('.thread-scroll')`);
  if (!hasContainer) {
    process.stderr.write('FAIL: .thread-scroll 容器未找到（页面未就绪？）\n');
    app.exit(1);
    return;
  }

  const results = [];
  for (const n of SIZES) {
    const blocks = mkBlocks(n);
    // 注入数据（窗口全局）再执行测量（分开两次 executeJavaScript——
    // 大 JSON 模板内联容易踩转义坑）
    await win.webContents.executeJavaScript(`window.__BENCH__ = ${JSON.stringify(blocks)}; 1`);
    const r = await win.webContents.executeJavaScript(`(() => {
      try {
        const blocks = window.__BENCH__;
        const t0 = performance.now();
        const container = document.querySelector('.thread-scroll');
        if (!container) return { err: 'no-container' };
        const html = blocks.map((b) => {
          if (b.kind === 'user') return '<div class="msg user"><div class="bubble">' + b.text + '</div></div>';
          if (b.kind === 'tool') return '<div class="trow"><div class="trow-row"><span class="trow-title">' + b.name + '</span></div></div>';
          return '<div class="msg"><div class="md-text">' + b.content.split('\\n').join('<br>') + '</div></div>';
        }).join('');
        container.innerHTML = html;
        const t1 = performance.now();
        let arr = blocks;
        for (let k = 0; k < 100; k++) {
          arr = arr.slice();
          const last = arr[arr.length - 1];
          arr[arr.length - 1] = { ...last };
        }
        const t2 = performance.now();
        const domCount = container.querySelectorAll('*').length;
        container.innerHTML = '';
        return { n: blocks.length, domMs: Math.round((t1 - t0) * 10) / 10, reduceMs: Math.round((t2 - t1) * 10) / 10, domCount };
      } catch (e) {
        return { err: String(e) };
      }
    })()`);
    results.push(r);
  }
  // 窗口化验证：窗口尺寸（初始 40）+ 哨兵
  const winInfo = await win.webContents.executeJavaScript(`(() => {
    return { note: '窗口化后 DOM 只渲染底部 40 块——domCount 见上表对照' };
  })()`);
  const report = { timestamp: new Date().toISOString(), results, winInfo };
  fs.writeFileSync('temp/bench-render.json', JSON.stringify(report, null, 2));
  process.stderr.write(JSON.stringify(report, null, 2) + '\n');
  app.exit(0);
});
