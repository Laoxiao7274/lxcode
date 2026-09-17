// frontend 测试的 TS 加载器：node:test 直接跑 .ts/.tsx 源码，无独立构建产物。
// 用已有 TypeScript 做内存转译，擦除 type-only 导入；不改生产模块解析规则。
// 测试运行前仍需 tsc --noEmit 校验类型，transpileModule 本身不做类型检查。
import { readFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";

const isTsLike = (s) => s.endsWith(".ts") || s.endsWith(".tsx");

export async function resolve(specifier, context, next) {
  if (isTsLike(specifier)) {
    const parentPath = context.parentURL ? fileURLToPath(context.parentURL) : process.cwd() + "/";
    const resolved = new URL(specifier, pathToFileURL(parentPath));
    return { shortCircuit: true, url: resolved.href };
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
