import { describe, expect, it } from "vitest";
import {
  EVAL_PROMPT,
  PRO_GROUP_NAME,
  RESULT_END,
  RESULT_START,
  extractEvalHtml,
  extractRenderableHtml,
  priorityFromOrder,
} from "./model-eval";

describe("extractEvalHtml", () => {
  it("提取包裹标记之间的 HTML", () => {
    const out = extractEvalHtml(
      `好的\n${RESULT_START}<html><svg>鹈鹕</svg></html>${RESULT_END}\n完成`,
    );
    expect(out.wrapped).toBe(true);
    expect(out.html).toBe("<html><svg>鹈鹕</svg></html>");
  });

  it("trim 标记间的首尾空白", () => {
    const out = extractEvalHtml(
      `${RESULT_START}\n  <div>动画</div>\n  ${RESULT_END}`,
    );
    expect(out.wrapped).toBe(true);
    expect(out.html).toBe("<div>动画</div>");
  });

  it("缺少起始标记 → wrapped=false", () => {
    expect(extractEvalHtml(`<html>没有起始标记${RESULT_END}`).wrapped).toBe(false);
  });

  it("缺少结束标记 → wrapped=false", () => {
    expect(extractEvalHtml(`${RESULT_START}<html>没有结束标记`).wrapped).toBe(false);
  });

  it("标记间为空 → wrapped=false", () => {
    expect(
      extractEvalHtml(`${RESULT_START}   ${RESULT_END}`).wrapped,
    ).toBe(false);
  });

  it("空字符串 → wrapped=false", () => {
    expect(extractEvalHtml("").wrapped).toBe(false);
  });

  it("多次包裹取第一组", () => {
    const out = extractEvalHtml(
      `${RESULT_START}<a/>${RESULT_END}${RESULT_START}<b/>${RESULT_END}`,
    );
    expect(out.wrapped).toBe(true);
    expect(out.html).toBe("<a/>");
  });

  it("标记前后有解释文字仍能提取", () => {
    const out = extractEvalHtml(
      `我来帮你做这个动画。\n\n${RESULT_START}<!DOCTYPE html><html>...</html>${RESULT_END}\n\n希望你喜欢。`,
    );
    expect(out.wrapped).toBe(true);
    expect(out.html).toBe("<!DOCTYPE html><html>...</html>");
  });
});

describe("常量", () => {
  it("EVAL_PROMPT 包含题目与包裹要求", () => {
    expect(EVAL_PROMPT).toContain("鹈鹕");
    expect(EVAL_PROMPT).toContain("不要联网");
    expect(EVAL_PROMPT).toContain(RESULT_START);
    expect(EVAL_PROMPT).toContain(RESULT_END);
  });

  it("PRO_GROUP_NAME 为 pro", () => {
    expect(PRO_GROUP_NAME).toBe("pro");
  });
});

describe("extractRenderableHtml", () => {
  it("优先用包裹标记提取", () => {
    const html = extractRenderableHtml(
      `说明文字\n${RESULT_START}<html><svg>鹈鹕</svg></html>${RESULT_END}`,
    );
    expect(html).toBe("<html><svg>鹈鹕</svg></html>");
  });

  it("无包裹时从 markdown ```html 围栏提取", () => {
    const html = extractRenderableHtml(
      "这是结果：\n```html\n<html><body>动画</body></html>\n```\n完成",
    );
    expect(html).toBe("<html><body>动画</body></html>");
  });

  it("无包裹时从裸 ``` 围栏提取（含 svg）", () => {
    const html = extractRenderableHtml(
      "```\n<svg viewBox=\"0 0 100 100\"><circle r=\"50\"/></svg>\n```",
    );
    expect(html).toContain("<svg");
  });

  it("无围栏但含裸 HTML → 从第一个标签开始提取", () => {
    const html = extractRenderableHtml(
      "好的，这是代码：\n<!DOCTYPE html><html><body>鹈鹕</body></html>",
    );
    expect(html).toBe("<!DOCTYPE html><html><body>鹈鹕</body></html>");
  });

  it("纯文字无 HTML → 返回空串", () => {
    expect(extractRenderableHtml("这是一个鹈鹕骑自行车的动画描述。")).toBe("");
  });

  it("空字符串 → 返回空串", () => {
    expect(extractRenderableHtml("")).toBe("");
  });
});

describe("priorityFromOrder", () => {
  it("索引 0 拿到最高优先级", () => {
    expect(priorityFromOrder(0, 5)).toBe(5);
    expect(priorityFromOrder(4, 5)).toBe(1);
  });

  it("单调递减", () => {
    const total = 3;
    expect(priorityFromOrder(0, total)).toBeGreaterThan(priorityFromOrder(1, total));
    expect(priorityFromOrder(1, total)).toBeGreaterThan(priorityFromOrder(2, total));
  });
});
