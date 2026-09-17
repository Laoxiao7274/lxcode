// 静态架构守卫：业务通信只在 WS 适配层；宿主桥只允许窗口和目录选择。
// 用 TypeScript AST 而不是扫描注释，避免文档里的示例触发误报。
import { createRequire } from 'node:module';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, extname, relative, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../', import.meta.url));
const require = createRequire(new URL('../frontend/package.json', import.meta.url));
const ts = require('typescript');
const sourceRoot = resolve(root, 'frontend/src');
const hostConsumers = new Set(['components/topbar/Topbar.tsx', 'components/sidebar/AddProjectDialog.tsx']);

export function checkFrontendSource(name, source) {
  const errors = [];
  const file = ts.createSourceFile(name, source, ts.ScriptTarget.Latest, true, name.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
  const isTransport = name.startsWith('agent/ws/');
  const isFactory = name === 'agent/index.ts';
  function reject(node, message) {
    const line = file.getLineAndCharacterOfPosition(node.getStart(file)).line + 1;
    errors.push(`${name}:${line}: ${message}`);
  }
  function checkImport(node, spec) {
    if (spec.startsWith('.')) {
      const target = resolve(sourceRoot, dirname(name), spec);
      const inside = relative(sourceRoot, target);
      if (inside.startsWith(`..${sep}`) || inside === '..') reject(node, '前端不能导入 src 之外的实现');
      const normalized = inside.replaceAll('\\', '/');
      if (!isTransport && !isFactory && (normalized === 'agent/ws' || normalized.startsWith('agent/ws/'))) {
        reject(node, '消费者应依赖能力接口，而不是具体 WS 适配器');
      }
    } else if (!['react', 'react-dom', 'gsap', '@fontsource-variable'].some((pkg) => spec === pkg || spec.startsWith(`${pkg}/`))) {
      reject(node, `未经边界声明的运行时依赖: ${spec}`);
    }
  }
  function visit(node) {
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
      checkImport(node, node.moduleSpecifier.text);
    }
    if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword || (ts.isIdentifier(node.expression) && node.expression.text === 'require'))) {
      const spec = node.arguments[0];
      if (spec && ts.isStringLiteral(spec)) checkImport(node, spec.text);
      else reject(node, '禁止无法静态校验的动态模块加载');
    }
    if (ts.isIdentifier(node)) {
      if (['WebSocket', 'fetch', 'XMLHttpRequest', 'EventSource', 'sendBeacon', 'WebTransport'].includes(node.text) && !isTransport) {
        reject(node, '网络通信必须封装在 agent/ws 适配层');
      }
      if (['require', 'process', 'Buffer', 'ipcRenderer'].includes(node.text)) reject(node, '渲染层不得获得 Node/IPC 通用能力');
      if (node.text === '__LX__' && !hostConsumers.has(name)) reject(node, '宿主桥仅用于窗口控制与目录选择');
    }
    ts.forEachChild(node, visit);
  }
  visit(file);
  return errors;
}

function sourceFiles(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    if (entry.isSymbolicLink()) throw new Error(`源码目录不允许符号链接: ${entry.name}`);
    const path = resolve(dir, entry.name);
    return entry.isDirectory() ? sourceFiles(path) : ['.ts', '.tsx'].includes(extname(path)) ? [path] : [];
  });
}

export function checkWorkspace() {
  return sourceFiles(sourceRoot).flatMap((path) => checkFrontendSource(relative(sourceRoot, path).replaceAll('\\', '/'), readFileSync(path, 'utf8')));
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const errors = checkWorkspace();
  if (errors.length) {
    console.error(errors.join('\n'));
    process.exitCode = 1;
  } else console.log('PASS: 前端运行时依赖、传输适配层与宿主桥边界');
}
