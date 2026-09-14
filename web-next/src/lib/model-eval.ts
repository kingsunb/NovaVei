/**
 * 模型评估页核心逻辑：固定测试题、包裹标记、HTML 提取与格式判定。
 *
 * 评估流程：对每个渠道的每个模型发送 EVAL_PROMPT，模型须把完整 HTML 放在
 * RESULT_START / RESULT_END 之间。根据回复是否合规决定优先级：
 *  - 测试报错 → 从候选列表移除（不进分组）
 *  - 成功但未按格式包裹 → 自动判最低优先级
 *  - 成功且包裹合规 → 正常参与手动排序
 */

/** 包裹起始标记。 */
export const RESULT_START = "<<<RESULT>>>";
/** 包裹结束标记。 */
export const RESULT_END = "<<<END>>>";

/**
 * 固定测试题。要求模型生成一个 SVG 鹈鹕骑自行车 2D 动画的 HTML，
 * 并把完整代码放在 <<<RESULT>>> / <<<END>>> 之间，标记外不输出任何内容。
 * "不要联网" 约束配合 sandbox iframe 渲染，即使模型不听话也无法外联。
 */
export const EVAL_PROMPT =
  "创建一个HTML，内容是SVG绘制一个鹈鹕骑自行车的2D动画，不要联网，直接做出来。" +
  `请将最终完整的 HTML 代码放在 ${RESULT_START} 和 ${RESULT_END} 之间，` +
  "这两个标记之外不要输出任何其他内容。";

/** 分组名称固定为 pro。 */
export const PRO_GROUP_NAME = "pro";

/** 并发测试的 worker 数，与渠道编辑器批量测试保持一致，避免触发上游风控。 */
export const EVAL_CONCURRENCY = 4;

/**
 * extractEvalHtml 从模型回复中提取包裹标记之间的 HTML。
 *
 * @returns 找到包裹时 { wrapped: true, html }；未找到时 { wrapped: false, html: "" }。
 *          多次出现包裹时取第一组；标记间内容做 trim，避免首尾空白撑高 iframe。
 */
export function extractEvalHtml(content: string): {
  wrapped: boolean;
  html: string;
} {
  if (!content) return { wrapped: false, html: "" };
  const startIdx = content.indexOf(RESULT_START);
  if (startIdx === -1) return { wrapped: false, html: "" };
  const afterStart = startIdx + RESULT_START.length;
  const endIdx = content.indexOf(RESULT_END, afterStart);
  if (endIdx === -1) return { wrapped: false, html: "" };
  const html = content.slice(afterStart, endIdx).trim();
  if (html.length === 0) return { wrapped: false, html: "" };
  return { wrapped: true, html };
}

/**
 * EvalOutcome 单个渠道模型的评估终态。
 *  - error      : 测试请求失败（上游报错/超时/拒绝），该候选从列表移除。
 *  - violation  : 成功返回但未按格式包裹，自动最低优先级。
 *  - ok         : 成功且包裹合规，正常参与手动排序。
 */
export type EvalOutcome = "ok" | "violation" | "error";

/** 评估候选：一个渠道上的一个模型。 */
export interface EvalTarget {
  channelId: number;
  channelName: string;
  channelType: string;
  /** ChannelModel.id，创建分组成员时需要。 */
  channelModelId: number;
  modelName: string;
}

/** 评估结果条目。 */
export interface EvalResult {
  target: EvalTarget;
  outcome: EvalOutcome;
  /** 模型原始回复全文。 */
  content: string;
  /** 提取出的 HTML（仅 outcome=ok 时有值）。 */
  html: string;
  latencyMs: number;
  promptTokens: number;
  completionTokens: number;
  /** outcome=error 时的失败原因。 */
  error: string;
}

/**
 * 归一化优先级：把用户拖拽顺序（索引 0 = 最优先）映射为后端 priority 值。
 * 后端 priority 越大越靠前，所以用 (length - index) 让索引 0 拿到最高值。
 */
export function priorityFromOrder(index: number, total: number): number {
  return total - index;
}
