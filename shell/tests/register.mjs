// Node ≥20.10 的 module.register 钩子入口：让 node --test 直接加载 TS 源。
import { register } from "node:module";
register(new URL("./ts-loader.mjs", import.meta.url));
