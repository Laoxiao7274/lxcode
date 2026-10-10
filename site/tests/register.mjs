// 测试入口的注册钩子（package.json 的 test 脚本经 --import 先加载它）。
//
// 为什么测试直接 import src/ 下的 .ts 源码而不是构建产物：站点源码就是契约本身
// （release.ts 的字段口径、mock 数据的自洽性），测试要钉的是源码，不是某次构建结果。
//
// 这里做两件事：
// 1) 类型剥离：Node 从 23.6 起默认剥离 TS 类型（process.features.typescript === "strip"）。
//    不支持时早失败并给人话——否则测试只会抛 "Unknown file extension .ts"。
// 2) 补全扩展名：站点源码用打包器风格的省略扩展名导入（`from "../release"`，见
//    tsconfig 的 moduleResolution: bundler），而 Node 的 ESM 解析要求写全扩展名。
//    解析钩子把「无扩展名的相对导入」补成 .ts，两端因此可以共用同一份源码。

import { existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { fileURLToPath } from "node:url";

if (process.features.typescript === false) {
  throw new Error("当前 Node 不支持直接执行 TypeScript（需要 >= 23.6）：升级 Node 后重跑 npm test");
}

registerHooks({
  resolve(specifier, context, nextResolve) {
    // 只碰「相对的、且没写扩展名」的导入，其余一律交回默认解析（不劫持 node: / 包名）。
    if (specifier.startsWith(".") && !/\.[a-z]+$/i.test(specifier)) {
      const url = new URL(`${specifier}.ts`, context.parentURL);
      if (existsSync(fileURLToPath(url))) {
        return { url: url.href, shortCircuit: true };
      }
    }
    return nextResolve(specifier, context);
  },
});
