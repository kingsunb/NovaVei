# NovaVeil 请求脱敏与还原功能

> **状态：已实现**（2026-09-10，对应需求 REQ-015）。核心代码位于 `internal/relay/mask/`，并已接入 `internal/relay/handler.go` 与 `internal/relay/mask_integration.go`。出厂默认全关，详见第八节。
> 本目录原为开发规划文档集，功能落地后保留作设计参考；技术内容仍有参考价值，行号以实际代码为准。
> 参考项目：[Data Maskit（数据面具）](https://github.com/xiaYuTian11/maskit)（Python + mitmproxy 实现）。
> 本文件为总纲，详细设计见同目录其余文档。

## 文档索引

| 文档 | 内容 |
|---|---|
| `README.md` | 本文件——总纲、能力对照、阶段拆分（已实现） |
| `01-架构与插入点.md` | NovaVeil 请求/响应链路分析与脱敏/还原精确插入点 |
| `02-规则引擎设计.md` | Go 版脱敏规则引擎、占位符规范、多轮会话一致性设计 |
| `03-流式还原设计.md` | SSE 跨 chunk 增量还原（最难模块）的缓冲拼接方案 |
| `04-配置与前端.md` | 规则开关、自定义敏感词、前端管理页与审计可视化 |
| `05-借鉴思路总结.md` | maskit 源码中值得借鉴的设计智慧与踩坑经验（9 大类） |
| `06-关键源码参考.md` | maskit 关键源码片段保留（占位符/脱敏/还原/流式SSE/校验/凭据/规则） |
| `07-日志详情命中明细.md` | 日志详情展示脱敏命中明细（规则/原文/占位符）的设计（待实施） |

---

## 一、背景与目标

### 1.1 问题
使用 Cursor、Claude Code、Codex 等 AI 编程助手时，代码中的高危敏感信息（API Key、数据库连接串、内网 IP、手机号/身份证等）会随请求发往外部模型服务商。

### 1.2 目标
在 NovaVeil 网关层实现：**请求出网前在本地自动打码成结构化占位符，模型回答时流式无感还原成原文**，开发体验完全不受影响。

### 1.3 为什么在 NovaVeil 做而不是另起 maskit
NovaVeil 本身就是 LLM API 网关，客户端的 `base_url` 已经指向它，**天然处于请求必经之路**：
- **无需 mitmproxy 中间人代理**——maskit 的核心复杂度之一
- **无需向操作系统安装自签名 CA 根证书**
- **无需多端口反代与 Passthrough 兜底**——NovaVeil 已有故障转移机制
- **Go 性能更高**——脱敏在每请求热路径，Go 正则比 Python 快
- **已有会话（`X-Session-Id`）、审计、日志 redact 基建**可直接复用

---

## 二、参考 maskit 的能力对照

| maskit 能力 | maskit 源位置 | NovaVeil 移植方式 | 工作量 |
|---|---|---|---|
| 19 类正则规则库 | `engine/transparent.py:52` RULES | Go `regexp` 重写，放 `internal/relay/mask/` 新包 | 中（直接译） |
| 占位符 `{{LABEL_6位辅音}}` | `transparent.py` 生成逻辑 | Go `crypto/rand` 生成 6 位辅音随机串 | 小 |
| 请求体脱敏 | mitmproxy addon `request` 钩子 | `handler.go:88` 拿到请求体后、`sendPassthrough` 前插入 | 小 |
| 非流式响应还原 | mitmproxy `response` 钩子 | `handler.go:552` `c.Writer.Write` 前还原 | 小 |
| **流式 SSE 跨 chunk 还原** | `transparent.py` 缓冲拼接 | `handler.go:713/792` 写每个 event 前增量还原 | **大（最难）** |
| 多轮会话占位符复用 | 滑动窗口映射 | 挂到 NovaVeil 已有的 `X-Session-Id` 会话 | 中 |
| Fail-Closed 熔断 | 异常→503 不放明文 | 契合 NovaVeil 现有 `rejectRequest` 错误路径 | 小 |
| 凭据只存哈希 | `event_store.py` 摘要 | NovaVeil 已有 `op/error_log.go:350` 日志 redact，强化即可 | 小 |
| 安全审计信号 | `audit_signals.py` 纯函数 | Go 重写为审计模块（可选增强） | 中 |
| 规则开关/自定义词 | `config.json` | 放 `model/setting.go` 系统设置 + 前端管理页 | 中 |

---

## 三、核心原理

```
你的输入: 排查数据库 mysql://root:Pass123@192.168.1.50:3306  联系人李四 13800138000
  ↓ NovaVeil 本地脱敏（handler.go:88 插入点）
上游收到: 排查数据库 {{CONNSTR_zkpmqx}}  联系人{{TERM_fnqtsw}} {{PHONE_bcdfgh}}
  ↓ 模型推理
上游回答: 建议 {{TERM_fnqtsw}} 核对 {{CONNSTR_zkpmqx}} 的连通性
  ↓ NovaVeil 本地还原（handler.go:552 / 713 插入点）
你看到:  建议 李四 核对 mysql://root:Pass123@192.168.1.50:3306 的连通性
```

---

## 四、实施阶段与工作量

| 阶段 | 内容 | 量级 | 依赖 |
|---|---|---|---|
| **P1** | Go 版规则引擎 + 占位符生成（译 19 类正则） | 中 | 无 |
| **P2** | 请求脱敏注入 + 非流式还原（2 处插入点） | 小 | P1 |
| **P3** | 流式 SSE 增量还原（跨 chunk 缓冲拼接） | **大** | P1 |
| **P4** | 会话级映射表 + 多轮一致性 | 中 | P1 |
| **P5** | 配置持久化 + 前端管理 UI + 审计可视化 | 中 | P1-P4 |
| **P6** | 单测 + 流式冒烟 + 回归（对齐 NovaVeil 测试规范） | 中 | P1-P5 |

**总体：中等偏上，约 3-5 人日。** 难点集中在 P3（流式 SSE 跨 chunk 还原）。

> **实现状态**：以上 P1-P6 阶段均已实现（2026-09-10，REQ-015）。规则引擎、占位符、流式还原、会话映射、配置与前端管理页均已落地，代码见 `internal/relay/mask/` 与前端 `web-next/src/pages/Mask.tsx`。

---

## 五、关键工程约束（移植自 maskit，须单测守死）

1. **100% 本地运算零遥测**：脱敏与还原全部在本地进程内，严禁任何用户追踪/云端日志。
2. **Fail-Closed 严格熔断**：脱敏管线未捕获异常或请求体超限时，立即回退阻断，**绝不放行未脱敏明文出网**。
3. **占位符规范 `{{LABEL_6位辅音}}`**：后缀 6 位纯辅音随机串，消除大模型对十六进制做变异算术的诱因；跨请求滑动窗口复用保证多轮一致。
4. **流式中途无事件返回空**：严禁返回空字节导致 chunked 语法提前断连。
5. **凭据安全红线**：API_KEY/TOKEN/SECRET/JWT 等在事件库中恒只存 preview + sha256 摘要，导出剔除原文。
6. **安全拷贝兼容**：脱敏生成新切片，不原地改写 `raw.Body`，契合 `handler.go:88` 现有 `string(raw.Body)` 安全拷贝模式（原 `unsafe.String` 零拷贝已在安全审计 C-1 中改为安全拷贝）。

---

## 六、目录规划

```
internal/relay/mask/          # 脱敏核心包（已实现）
  ├── rules.go                # 19 类正则规则定义（译自 maskit transparent.py:52）
  ├── placeholder.go          # 占位符生成（6 位辅音串）+ 映射表
  ├── engine.go               # 脱敏引擎：扫描请求体 → 替换占位符
  ├── restore.go              # 还原引擎：占位符 → 原文（非流式）
  ├── stream_restore.go       # 流式增量还原：跨 chunk 缓冲拼接（最难）
  ├── session.go              # 会话级映射表 + 滑动窗口复用
  └── *_test.go               # 单测
internal/model/mask_setting.go  # 脱敏配置模型（规则开关/自定义词）— 已实现
internal/op/mask.go             # 脱敏配置读写 op — 已实现
internal/server/handlers/mask.go # 脱敏配置管理 API — 已实现
web-next/src/pages/Mask.tsx     # 前端脱敏管理页 — 已实现
```

---

## 七、风险与待决策

| 项 | 说明 | 倾向 |
|---|---|---|
| 规则默认开关 | maskit 默认开 7 类核心、关 12 类高误报。NovaVeil 面向网关多用户，误报影响更大 | **已锁定：默认全关**（见第八节保证），管理员按需开 |
| 脱敏作用范围 | 全局开关 vs 按渠道/分组开关 | 按分组开关，粒度可控 |
| 流式协议差异 | OpenAI/Anthropic SSE 事件结构不同，还原需按协议适配 | 复用 NovaVeil 现有 transformer 协议判断 |
| 性能影响 | 每请求正则扫描开销 | Go 正则 + 仅扫请求体非流式字段，可接受；提供开关一键关闭 |
| 与现有日志 redact 关系 | `op/error_log.go:350` 已有日志字段脱敏 | 互补：日志 redact 管记录，请求脱敏管出网 |

---

## 八、默认关闭保证（硬约束）

本功能出厂状态恒为**全关**，且此为不可退化的硬约束，非可配置倾向：

1. **全局开关**：`MaskConfig.Enabled` 默认 `false`。复用 `SettingGetBool`，**setting 行不存在即视为关，无需初始化写入任何默认行**（与 `SettingKeyConversationLog` 完全一致）。
2. **分组开关**：`GroupRelayConfig.MaskEnabled` 为 `bool` 零值 `false`。旧分组持久化 JSON 反序列化自动得到关，**无需数据迁移**。
3. **单规则开关**：`BuiltinRuleSwitch` 出厂恒为全 `false`，不提供任何默认开启项（区别于 maskit 默认开 7 类）。
4. **短路语义**：三层开关任一为 `false` 即整条脱敏/还原链路跳过。关闭时热路径**仅多一次 bool 判断**，不触发任何正则扫描或缓冲分配，零开销零风险。
5. **热生效**：开关走 `settingCache`，管理台改写即刷新，**无需重启**。
6. **部署期熔断（可选）**：若需保证某些部署根本不允许开启脱敏，可加一个 env/配置文件级 kill switch，优先级高于管理台设置。（待评估）

---

详细设计见同目录 `01` ~ `04` 文档。
