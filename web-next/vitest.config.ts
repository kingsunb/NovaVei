/// <reference types="vitest" />
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "node:path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    // 排除 E2E（Playwright 单独跑）
    exclude: [
      "**/node_modules/**",
      "**/dist/**",
      "**/.{idea,git,cache,output,temp}/**",
      "e2e/**",
    ],
    coverage: {
      provider: "v8",
      reporter: ["text", "html", "json-summary"],
      include: [
        "src/lib/**",
        "src/components/ui/**",
        "src/components/charts/**",
      ],
      exclude: ["**/*.test.{ts,tsx}", "**/*.d.ts"],
      // P4.1 内部验收目标：≥90%
      //  - 下降超过阈值会 CI 失败
      thresholds: {
        lines: 88,        // 给 2% buffer（新代码可能略低）
        functions: 80,
        branches: 85,
        statements: 88,
      },
    },
  },
});
