/**
 * 全局常量 —— 与 NovaVeil 后端约定对齐
 *  DEFAULT_TEST_MESSAGE：渠道「测试连通」默认 probe 消息。
 *  选题思路（与源项目一致）：考察常识判断与指令遵循。
 *  注意：前端总是显式携带 message（缺省时用本常量），后端自己的空 message
 *  回退值是 "ping"（internal/relay/test.go），与本常量并不相同。
 */
export const DEFAULT_TEST_MESSAGE =
  "我要去洗汽车，洗车店离我家只有50m，走路去还是坐公交车去？回复最多五个字。";
