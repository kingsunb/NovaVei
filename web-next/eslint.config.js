// ESLint v9 flat config —— 关注三类问题：
// 1. 类型安全（typescript-eslint）
// 2. React 模式（react-hooks）
// 3. HMR 友好（react-refresh）
import js from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";

export default tseslint.config(
  // 全局忽略
  {
    ignores: [
      "dist/**",
      "node_modules/**",
      "coverage/**",
      "_legacy/**",
      "assets/**",          // P0 静态原型 JS 快照（已被 web-next/ 源码取代）
      "*.config.{js,ts,mjs}",
    ],
  },

  // 基础
  js.configs.recommended,
  ...tseslint.configs.recommended,

  // 项目级规则
  {
    files: ["src/**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      globals: { ...globals.browser, ...globals.es2022 },
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      // === 类型安全 ===
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
      "@typescript-eslint/no-explicit-any": "error",
      "@typescript-eslint/ban-ts-comment": "error",

      // === React Hooks 模式 —— 这次修的 #2 #3 类问题会在这里被捕获 ===
      "react-hooks/rules-of-hooks": "error",
      "react-hooks/exhaustive-deps": "warn",

      // === HMR ===
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true, allowExportNames: ["useTheme", "useAuth", "buttonVariants", "useSidebar"] },
      ],

      // === 一般 ===
      "no-console": ["warn", { allow: ["warn", "error"] }],
      "no-debugger": "error",
      eqeqeq: ["error", "always", { null: "ignore" }],
      "prefer-const": "error",
    },
  },

  // 测试放宽
  {
    files: ["src/**/*.test.{ts,tsx}", "src/test/**"],
    rules: {
      "@typescript-eslint/no-explicit-any": "off",
      "@typescript-eslint/no-unused-vars": "off",
      "react-refresh/only-export-components": "off",
    },
  },

  // public/ 下的浏览器脚本（如 theme-init.js，在 <head> 中内联执行）
  {
    files: ["public/**/*.js"],
    languageOptions: {
      ecmaVersion: 2022,
      globals: { ...globals.browser, ...globals.es2022 },
    },
  },

  // 一次性迁移脚本
  {
    files: ["scripts/**/*.{js,mjs,ts}"],
    languageOptions: {
      globals: { ...globals.node, console: "readonly" },
    },
  },
);
