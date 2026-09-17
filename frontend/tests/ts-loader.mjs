// frontend 测试的 TS 加载器：node:test 直接跑 .ts 源码，无独立构建产物。
// 用已有 TypeScript 做内存转译，擦除 type-only 导入；不改生产模块解析规则。
// 测试运行前仍需 tsc --noEmit 校验类型，transpileModule 本身不做类型检查。
import { readFileSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";

export async function resolve(specifier, context, next) {
  if (specifier.endsWith(".ts")) {
    const parentPath = context.parentURL ? fileURLToPath(context.parentURL) : process.cwd() + "/";
    const resolved = new URL(specifier, pathToFileURL(parentPath));
    return { shortCircuit: true, url: resolved.href };
  }
  return next(specifier, context);
}

export async function load(url, context, next) {
  if (!url.endsWith(".ts")) return next(url, context);
  const source = readFileSync(new URL(url), "utf8");
  const { outputText } = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2022,
      verbatimModuleSyntax: false,
    },
  });
  return { format: "module", shortCircuit: true, source: outputText };
}
