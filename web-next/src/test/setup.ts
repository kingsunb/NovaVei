import "@testing-library/jest-dom/vitest";
import { afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/react";

// 固定时区：时间相关断言（formatDatetimeLocal 等）不能依赖 CI/本机的 TZ，
// GitHub Actions runner 默认 UTC，这里显式钉死，测试写死 UTC 期望值（审计 1.11）。
process.env.TZ = "UTC";

/**
 * 每个用例跑完自动 unmount，避免 DOM 残留
 *  —— RTL 16 推荐做法
 */
afterEach(() => {
  cleanup();
});

// jsdom 缺 matchMedia；ThemeProvider 在 system 模式下会调用
if (!window.matchMedia) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

// jsdom 缺 DataTransfer；userEvent.upload 依赖它设置 <input type="file"> 的 files 属性
if (typeof DataTransfer === "undefined") {
  // 注意：不能在此守卫内写 `typeof DataTransfer`——TS 会把 DataTransfer 收窄为
  // `never`，使目标类型变成 `never` 而报 TS2322。用 `new () => unknown` 既避开收窄，
  // 又能容纳这个部分 polyfill（jsdom 只需要 items/files），运行时行为不变。
  (globalThis as unknown as { DataTransfer: new () => unknown }).DataTransfer =
    class DataTransferPolyfill {
      items: DataTransferItemList;
      files: FileList;
      constructor() {
        const fileArray: File[] = [];
        const itemsObj = {
          length: 0,
          add(file: File) {
            fileArray.push(file);
            (this as { length: number }).length = fileArray.length;
          },
          clear() {
            fileArray.length = 0;
            (this as { length: number }).length = 0;
          },
          remove(_index: number) {},
        };
        this.items = itemsObj as unknown as DataTransferItemList;
        this.files = fileArray as unknown as FileList;
      }
    };
}

// jsdom 缺 Blob.text() / File.text()；BackupSection 导入流程依赖 File.text() 读取文件内容
if (typeof Blob.prototype.text !== "function") {
  Blob.prototype.text = function (this: Blob): Promise<string> {
    return new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(reader.result as string);
      reader.onerror = () => reject(reader.error);
      reader.readAsText(this);
    });
  };
}
