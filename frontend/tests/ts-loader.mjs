// frontend 测试的 TS 加载器：node:test 直接跑 .ts/.tsx 源码，无独立构建产物。
// 用已有 TypeScript 做内存转译，擦除 type-only 导入；不改生产模块解析规则。
// 测试运行前仍需 tsc --noEmit 校验类型，transpileModule 本身不做类型检查。
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";

const isTsLike = (s) => s.endsWith(".ts") || s.endsWith(".tsx");

export async function resolve(specifier, context, next) {
  if (isTsLike(specifier)) {
    const parentPath = context.parentURL ? fileURLToPath(context.parentURL) : process.cwd() + "/";
    const resolved = new URL(specifier, pathToFileURL(parentPath));
    return { shortCircuit: true, url: resolved.href };
  }
  // 相对导入补扩展名：TS 源码里写 `./events`（生产侧 tsc/vite 自己按 TS 规则
  // 解析），而 node 的 ESM 解析要求带扩展名。不补的话，任何「跨模块的运行时
  // 相对导入」在测试里都加载不了（此前没人踩到，是因为被测试的模块恰好都是
  // 叶子——type-only 导入会被转译擦除，压根不走解析）。顺序照 TS：
  // 原样 → .ts → .tsx → /index.ts → /index.tsx。
  if (specifier.startsWith("./") || specifier.startsWith("../")) {
    const parentPath = context.parentURL ? fileURLToPath(context.parentURL) : process.cwd() + "/";
    for (const cand of [specifier, specifier + ".ts", specifier + ".tsx", specifier + "/index.ts", specifier + "/index.tsx"]) {
      if (!isTsLike(cand)) continue;
      const url = new URL(cand, pathToFileURL(parentPath));
      if (existsSync(fileURLToPath(url))) return { shortCircuit: true, url: url.href };
    }
  }
  return next(specifier, context);
}

export async function load(url, context, next) {
  if (!isTsLike(url)) return next(url, context);
  const source = readFileSync(new URL(url), "utf8");
  const { outputText } = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2022,
      verbatimModuleSyntax: false,
      // .tsx 的 JSX → react/jsx-runtime 的 jsx() 调用（React 19 自动
      // 导入，与源码只 import { memo } 的命名导入形态兼容）
      jsx: ts.JsxEmit.ReactJSX,
    },
  });
  return { format: "module", shortCircuit: true, source: outputText };
}
