# Actions 部署环境（Environments）增强 - 产品需求文档

## Overview

- **Summary**: 为 Gitea Actions 新增 GitHub 风格的「部署环境（Environments）」功能：仓库可定义命名环境（如 `production`、`staging`），workflow 作业通过 `environment:` 键声明目标环境；环境可配置保护规则（部署分支策略、必需审批人）、审批门禁、部署锁（手动锁定 + 自动互斥排队）、冻结窗口（一次性 + 周期性）、环境级变量与密钥，并自动记录部署历史。
- **Purpose**: 当前代码库中**完全不存在** CI/CD 环境概念（已核实：`models/actions/` 无 environment 模型、API 无 environments 路由、workflow 解析器 `modules/actions/jobparser/model.go` 的 `Job` 结构体会丢弃 `environment:` YAML 键、无部署记录、无环境管理 UI）。部署类作业目前没有任何发布管控手段，任何人推送到默认分支都可能直接触达生产。
- **Target Users**: 仓库维护者/管理员（配置环境保护）、部署审批人（审核发布）、所有 Actions 使用者（查看部署历史与门禁状态）。

## Goals

- 仓库级环境的创建、配置、删除（UI + API）。
- workflow 作业支持 `environment: <name>` 与 `environment: {name: ..., url: ...}` 两种写法，`name`/`url` 支持表达式；键在作业重序列化（`SetJob`/`Marshal`）后不丢失。
- **保护规则**：部署分支策略（全部分支 / 仅保护分支 / 选定分支匹配）。
- **审批**：配置必需审批人（用户或团队）后，目标环境作业保持阻塞，直到授权审批人批准；拒绝则作业失败；记录审批审计。
- **部署历史**：每个目标环境作业自动生成部署记录（环境、ref、SHA、触发人、状态、审批人、时间、作业链接），支持按环境/仓库查询。
- **环境级变量**：变量按环境作用域隔离，仅注入目标环境作业，优先级高于仓库/组织/全局变量。
- **环境级密钥**：密钥按环境作用域隔离、加密存储，仅注入目标环境作业；fork PR 作业不可见（沿用现有 fork PR 密钥隔离规则）。
- **冻结窗口**：支持一次性窗口（起止时间）与周期性窗口（每周指定星期 + 时刻 + 时长 + 时区）；窗口内部署挂起，窗口结束后自动恢复。
- **部署锁**：
  - 手动锁：管理员可锁定/解锁环境（带原因），锁定期间所有部署挂起；
  - 自动互斥：开启后同一环境同一时刻只允许一个部署作业运行，其余按 FIFO 排队，前序部署结束后自动放行。
- 待审批通知：部署进入待审批时向授权审批人发送 Gitea 站内通知；审批/拒绝结果通知触发人。

## Non-Goals

- 组织/用户级环境（环境仅仓库级；变量/密钥的组织级作用域维持现状）。
- runner 侧 `environment` 表达式上下文（`${{ environment.name }}`）与向步骤暴露 `environment.url`；服务端门禁与注入为本期范围。
- 部署 webhook 事件、部署状态提交状态（commit status）联动。
- GitHub 的「等待计时器（wait timer）」保护规则。
- Go SDK 客户端（外部模块 `gitea.dev/sdk`）变更；仅提供 REST API 与 Swagger 文档。
- 用户文档（发布在独立的 website 仓库，不在本仓库）。
- 冻结窗口的 cron 表达式自定义（周期性仅支持「每周若干天 + 时刻 + 时长」模型）。

## Background & Context

- 作业生命周期：作业入库为 `StatusBlocked`（有 needs / run 待审批等）或 `StatusWaiting`；`services/actions/job_emitter.go` 的 `jobStatusResolver` 负责把 Blocked 作业在 needs 完成、`if:` 通过、并发组允许后转为 Waiting，runner 再领取。环境门禁统一挂在该解析路径上：**所有声明环境的作业一律以 Blocked 入库**，由 resolver 评估环境门禁后决定放行/继续阻塞/终止。
- run 级审批（fork PR 不可信代码）已存在：`ActionRun.NeedApproval` + `services/actions/approve.go` 的 `ApproveRuns`，环境审批为独立机制，不与之共用状态字段。
- workflow 解析器 `modules/actions/jobparser/model.go` 中的 `Job`/`SingleWorkflow` 是 Gitea 自有结构（actionslib 仅用于辅助方法），新增 YAML 字段即可随 `SetJob`/`Marshal` 往返保留；表达式求值可参照同文件 `EvaluateConcurrency` 的做法。
- 变量：`models/actions/variable.go` 的 `GetVariablesOfRun` 按 全局 < 组织 < 仓库 优先级合并；任务载荷在 `services/actions/task.go` 的 `buildRunnerTask` 组装。密钥：`models/secret/secret.go` 的 `GetSecretsOfTask` 解密合并，且对 fork PR 提前返回。
- 建表：新模型通过 `db.RegisterModel` 注册后由 xorm `SyncAllTables` 自动建表/加列（本 fork 的版本化迁移位于 `modelmigration/`，仅用于数据迁移）。
- API 结构体位于仓库内 `modules/structs/`；路由在 `routers/api/v1/api.go` 的 `addActionsRoutes`（仓库级）；仓库设置 WEB 路由组在 `routers/web/web.go` 约 1257 行（`reqRepoAdmin`），设置页处理器模式见 `routers/web/shared/actions/variables.go`。
- 定时任务注册模式见 `services/cron/tasks_actions.go`（如 `start_schedule_tasks` 为 `@every 1m`）。
- 站内通知：`models/activities/notification.go` 的 `Notification` 目前以 Issue 为中心（IssueID NOT NULL，Source 仅有 issue/PR/commit/repo），需新增部署类 Source 与链接渲染分支。
- 已与用户确认的范围决策：部署锁 = 手动锁 + 自动互斥排队；冻结窗口 = 一次性 + 周期性；环境级变量与密钥都做；待审批附带站内通知。

## Functional Requirements

- **FR-1 环境管理**：管理员可通过 UI/API 创建、查看、更新、删除仓库环境；环境名在仓库内唯一；删除环境后其变量/密钥/审批人/分支规则/冻结窗口级联删除，历史部署记录保留（环境名快照）。
- **FR-2 workflow 解析**：解析器识别作业级 `environment`（字符串或 `{name, url}` 映射）；`name`/`url` 中的表达式在作业就绪（needs 完成、matrix 展开后）按与 `concurrency` 相同的上下文求值；求值结果持久化到作业行。
- **FR-3 自动建环境**：作业引用不存在的环境时，系统自动创建该环境（无保护规则）并正常记录部署；自动创建只发生在 run 级审批（fork PR）通过之后的作业解析阶段。
- **FR-4 部署分支策略**：环境可配置分支策略——`all`（默认，不限制）、`protected`（仅保护分支可部署；tag 不视为保护分支）、`selected`（按 glob 模式匹配 ref 名，分支与 tag 均可匹配）。不符策略的部署被**拒绝**，作业以失败终止并给出可读原因。
- **FR-5 审批门禁**：环境配置必需审批人（用户/团队）后，部署作业保持阻塞并生成待审批部署记录；授权用户可批准（作业继续后续门禁）或拒绝（作业失败）；审批决定记录审批人、评论、时间。
- **FR-6 审批权限**：仓库管理员始终可审批；配置的审批用户可审批；配置团队的成员（对仓库有写权限）可审批；**触发该 run 的用户不能审批自己触发的部署**；无权限用户的审批/拒绝请求返回 403。
- **FR-7 冻结窗口**：管理员可为环境配置一次性窗口（起止时间）与周期性窗口（星期位图 + 本地时刻 HH:MM + 时长分钟 + IANA 时区）；窗口生效期间部署作业阻塞并标明窗口名称；窗口结束后作业自动恢复（最迟 1 分钟内）。
- **FR-8 手动部署锁**：管理员可锁定环境（必填/选填原因）并解锁；锁定期间所有部署作业阻塞并显示锁定人与原因；解锁后自动恢复。
- **FR-9 自动互斥**：环境开启互斥后，同一环境至多一个部署作业处于运行/等待领取状态；其余部署阻塞排队，按作业 ID FIFO 放行；前序部署终态后自动唤醒下一个。
- **FR-10 环境级变量**：管理员可按环境增删改查变量；仅声明该环境的作业能在表达式求值与 runner 载荷中获得这些变量；优先级 全局 < 组织/用户 < 仓库 < 环境；同名覆盖。
- **FR-11 环境级密钥**：管理员可按环境增删改查密钥（加密存储、值只写）；仅声明该环境的作业获得；fork PR 作业（除 `pull_request_target`）不可见；reusable workflow 的 `secrets:` 传递规则对环境密钥同样生效。
- **FR-12 部署历史**：每个声明环境的作业（含 matrix 每个组合、rerun 的每次尝试）在到达门禁时创建一条部署记录，含环境名快照、ref、SHA、触发人、`environment.url` 求值结果、审批状态/审批人/评论/时间；部署展示状态由审批状态与作业状态派生（待审批/进行中/成功/失败/取消/已拒绝）；支持按环境与按仓库分页查询（API + UI）。
- **FR-13 通知**：部署进入待审批时，向所有授权审批人（去重、排除触发人）创建站内通知，链接到作业页；批准/拒绝后向 run 触发人创建通知；通知铃铛/列表可正确渲染该类型。
- **FR-14 门禁顺序与原因**：门禁按 手动锁 → 冻结窗口 → 分支策略 → 审批 → 互斥排队 的顺序评估；阻塞中的作业在 UI/API 上可区分阻塞原因（锁定原因/窗口名/待审批/排队中）。
- **FR-15 UI**：仓库设置 → Actions 下新增「Environments」页：环境列表（保护状态徽章、最近部署）与环境详情页（分支策略、审批人选择器、冻结窗口管理、锁定开关、变量与密钥管理、最近部署表）；run 详情页中环境作业显示环境名徽章与门禁状态，授权用户可见批准/拒绝按钮。

## Non-Functional Requirements

- **NFR-1 安全**：环境密钥加密存储（复用 `secret_module.EncryptSecret`）；环境管理接口要求仓库管理员权限；审批接口做服务端授权校验，不依赖前端隐藏。
- **NFR-2 兼容**：不改变现有无环境作业的任何行为；`environment:` 键保留在发给 runner 的 WorkflowPayload 中（runner 不识别即忽略）。
- **NFR-3 性能**：门禁评估在既有 job-emitter 队列事务内完成，不引入跨 run 长事务；周期性冻结窗口求值为纯内存计算；每分钟的唤醒 cron 仅查询存在「环境阻塞作业」的 run。
- **NFR-4 可观测**：门禁阻塞/拒绝原因写入服务端日志；部署记录提供审计线索。
- **NFR-5 工程规范**：遵循 AGENTS.md（新 `.go` 文件加 2026 版权头、仅改 `locale_en-US.json`、TS 用 `!`、`tw-*` 优先、`make fmt`/`lint-*`/`generate-swagger`、测试快速且确定性）。

## Constraints

- **Technical**：Go + xorm 模型自动同步；actionslib 为外部依赖不可改（环境字段加在 Gitea 自有 jobparser 结构上）；SDK 为外部模块不改动；通知系统以 Issue 为中心，需扩展 Source 枚举与渲染。
- **Business**：环境为仓库级；配置管理限仓库管理员。
- **Dependencies**：job-emitter 队列、cron 框架、通知模块、保护分支判定（`models/git/protected_branch.go`）、团队成员查询（`models/organization`）。

## Assumptions

- 环境名遵循 GitHub 约束（不区分大小写存储展示、长度 ≤ 255、不含空白以外的特殊字符做基本校验）。
- 周期性冻结窗口以环境配置的 IANA 时区求值；未配置时用服务器时区。
- 互斥排队仅在同一环境内生效，不与 workflow `concurrency:` 组冲突（两者各自独立评估）。
- 环境被删除时，已在运行/阻塞中的历史作业不受影响（作业上的环境名为求值快照），但不再受新规则约束——具体策略：删除环境后引用该环境的阻塞作业按「环境不存在」处理（自动重建为无保护环境或放行），实现时取与 FR-3 一致的自动重建语义。

## Acceptance Criteria

### AC-1: 环境管理 CRUD（API + UI）
- **Type**: `rule`
- **Given**: 仓库管理员登录
- **When**: 通过 UI 或 API 创建/查看/更新/删除环境（含分支策略、审批人、互斥开关等配置）
- **Then**: 配置持久化并可读回；同名创建返回冲突错误；非管理员访问管理接口返回 403/404；删除环境后其变量/密钥/审批人/分支/窗口级联删除
- **Pass Condition**: API 集成测试覆盖 CRUD + 权限 + 级联删除，全部通过
- **Evidence**: `go test ./tests/integration/ -run 'TestAPIActionsEnvironments'`；UI 手动验证截图

### AC-2: workflow `environment:` 解析与往返保留
- **Type**: `rule`
- **Given**: 一个含 `environment: production` 与另一个含 `environment: {name: ${{ inputs.env }}, url: https://...}` 的 workflow
- **When**: 作业入库并重序列化 payload
- **Then**: 字符串与映射两种写法均可解析；表达式在作业就绪时正确求值；求值后的环境名持久化到作业行；payload 中 `environment:` 键不丢失
- **Pass Condition**: jobparser 单元测试覆盖解析/克隆/往返/表达式求值，全部通过
- **Evidence**: `go test ./modules/actions/jobparser/`

### AC-3: 部署分支策略强制
- **Type**: `rule`
- **Given**: 环境配置为 `protected` 或 `selected`（模式 `release-*`）
- **When**: 分别从非保护分支、保护分支、匹配/不匹配模式的 ref 触发部署作业
- **Then**: 保护分支环境仅放行保护分支部署，其余作业失败并给出分支策略原因；selected 环境按 glob 放行/拒绝；`all` 环境全部放行
- **Pass Condition**: 门禁单元测试 + 集成测试覆盖三种模式与 tag 情形
- **Evidence**: `go test ./services/actions/ -run 'Environment'`；集成测试

### AC-4: 审批门禁全流程
- **Type**: `rule`
- **Given**: 环境配置了必需审批人（含一个团队）
- **When**: 部署作业到达门禁；审批人批准 / 拒绝；触发人本人尝试审批；无权限用户尝试审批
- **Then**: 批准前作业保持 Blocked、部署记录为待审批；批准后作业转为 Waiting 并继续；拒绝后作业失败、记录拒绝人与评论；触发人自审返回 403；无权限用户返回 403；审计字段完整
- **Pass Condition**: 集成测试覆盖批准/拒绝/自审拒绝/越权拒绝
- **Evidence**: `go test ./tests/integration/ -run 'TestActionsEnvironmentApproval'`

### AC-5: 冻结窗口（一次性 + 周期性）
- **Type**: `rule`
- **Given**: 环境配置一个进行中的一次性窗口，以及一个「每周当前星期、当前时刻前后若干分钟」的周期性窗口
- **When**: 窗口内触发部署；窗口结束后 cron 运行
- **Then**: 窗口内部署阻塞且原因为窗口名；窗口结束后 1 分钟内作业自动放行；窗口 CRUD 正常；时区按配置求值
- **Pass Condition**: 窗口求值纯函数单元测试（边界：开始/结束时刻、跨时区、星期位图）+ 集成测试验证阻塞与自动恢复
- **Evidence**: 单元测试 + 集成测试输出

### AC-6: 部署锁（手动 + 自动互斥）
- **Type**: `rule`
- **Given**: 环境被手动锁定；或环境开启互斥且已有一个部署在运行
- **When**: 锁定期间触发部署后解锁；互斥场景下连续触发两个部署并等待第一个结束
- **Then**: 锁定期间作业阻塞并显示锁定人/原因，解锁后自动放行；互斥场景第二个部署在第一个终态前不运行，终态后 FIFO 自动放行
- **Pass Condition**: 集成测试覆盖锁/解锁放行与互斥排队顺序
- **Evidence**: 集成测试输出

### AC-7: 环境级变量作用域与优先级
- **Type**: `rule`
- **Given**: 同名变量分别定义在全局、仓库、环境层；另一作业不引用该环境
- **When**: 两个作业被 runner 领取
- **Then**: 环境作业获得环境层值（最高优先级）且包含环境独有变量；非环境作业看不到任何环境变量；表达式求值同样可见环境变量
- **Pass Condition**: 变量合并单元测试 + 集成测试校验 runner 载荷
- **Evidence**: `go test ./models/actions/ ./services/actions/`

### AC-8: 环境级密钥作用域与加密
- **Type**: `rule`
- **Given**: 环境配置密钥；同一仓库存在 fork PR run
- **When**: 环境作业与 fork PR 作业分别领取任务
- **Then**: 环境作业载荷含解密后的环境密钥；fork PR（非 pull_request_target）载荷不含；数据库中密文存储；非环境作业不可见
- **Pass Condition**: 集成测试校验载荷内容与 fork PR 隔离；存储断言为密文
- **Evidence**: 集成测试输出

### AC-9: 部署历史记录与查询
- **Type**: `rule`
- **Given**: 多个作业（含 matrix、rerun）部署到同一环境
- **When**: 查询环境部署列表与仓库部署列表
- **Then**: 每个作业/组合/尝试各有一条记录，字段完整（ref、SHA、触发人、状态派生正确、审批信息、作业链接）；分页正常；删除环境后历史记录仍可查
- **Pass Condition**: API 集成测试覆盖列表/分页/字段/rerun
- **Evidence**: API 测试输出

### AC-10: 审批通知
- **Type**: `rule`
- **Given**: 环境配置了审批人
- **When**: 部署进入待审批；审批人做出决定
- **Then**: 每个授权审批人（排除触发人、去重）收到一条站内通知并链接到作业页；触发人收到结果通知；通知列表正确渲染该类型
- **Pass Condition**: 集成测试断言通知行与链接
- **Evidence**: 集成测试输出

### AC-11: run 详情页门禁状态与操作
- **Type**: `rule`
- **Given**: 因审批/冻结/锁定/互斥而阻塞的环境作业
- **When**: 用户打开 run 详情页
- **Then**: 作业显示环境徽章与对应阻塞原因；授权用户看到批准/拒绝按钮且操作生效；无权限用户看不到操作按钮
- **Pass Condition**: e2e 或集成测试断言页面元素与操作结果
- **Evidence**: `GITEA_TEST_E2E_FLAGS` 测试或模板集成测试 + 截图

### AC-12: 设置页环境管理体验
- **Type**: `rubric`
- **Dimension**: 环境管理 UI 与现有 Actions 设置页（variables/secrets/runners）的视觉与交互一致性
- **Scale**: 1-5
- **Anchors**: 1 = 页面风格脱节、表单不可用；3 = 功能可用但样式/交互与现有页面明显不一致；5 = 导航、表单、徽章、弹窗与现有设置页完全一致，遵循 tw-* 规范
- **Pass Threshold**: >= 4
- **Evidence**: 环境列表页与详情页截图，对照 variables/secrets 页

### AC-13: 工程质量门禁
- **Type**: `rule`
- **Given**: 全部实现完成
- **When**: 运行 fmt、lint、swagger 生成与相关测试
- **Then**: `make fmt` 无变更；`make lint-go`（及涉及前端的 `lint-js`/`lint-css`/`lint-templates`）通过；`make generate-swagger` 无遗漏；新增/修改测试全部通过；仅修改 `locale_en-US.json`
- **Pass Condition**: 上述命令全部退出码 0
- **Evidence**: 命令输出记录于任务 Completion Evidence

## Open Questions

- 无（范围决策已通过用户确认：手动锁+自动互斥、一次性+周期性冻结、变量+密钥、站内通知）。
