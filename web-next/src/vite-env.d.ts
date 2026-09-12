/// <reference types="vite/client" />

// 构建期由 vite.config.ts 的 define 注入（来源：scripts/build.sh 传入的
// VITE_APP_VERSION / VITE_APP_COMMIT 环境变量）。dev 模式未设置时为空串，
// 版本看门狗按「不可判定」处理，不告警。
declare const __APP_VERSION__: string;
declare const __APP_COMMIT__: string;
