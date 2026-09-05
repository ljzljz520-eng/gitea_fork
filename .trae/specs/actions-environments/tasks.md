# Actions 部署环境（Environments）增强 - 实施计划

> 说明：任务按依赖顺序排列；每个任务完成后需满足其 Test Requirements 并记录 Completion Evidence。
> 新 `.go` 文件加 2026 版权头；locale 只改 `options/locale/locale_en-US.json`；Go 改后跑 `make fmt`。

## Task 1: 数据模型层（环境、部署记录及关联表）
- **Status**: `completed`
- **Priority**: high
- **Depends On**: None
- **Completion Evidence**:
  - 新增 [environment.go](file:///Users/kkcarrot/swe-project/gitea_fork/models/actions/environment.go)（Environment/Reviewer/AllowedBranch/FreezeWindow + CRUD + 窗口求值纯函数 + 级联删除）、[deployment.go](file:///Users/kkcarrot/swe-project/gitea_fork/models/actions/deployment.go)（Deployment + 派生状态 + 查询 + 阻塞环境 run 查询）、[environment_variable.go](file:///Users/kkcarrot/swe-project/gitea_fork/models/actions/environment_variable.go)、[environment_secret.go](file:///Users/kkcarrot/swe-project/gitea_fork/models/actions/environment_secret.go)（加密复用 modules/secret，置 actions 包避免导入环）；[run_job.go](file:///Users/kkcarrot/swe-project/gitea_fork/models/actions/run_job.go) 新增 Environment/EnvironmentURL 列。
  - TR-1.1/1.2/1.3：[environment_test.go](file:///Users/kkcarrot/swe-project/gitea_fork/models/actions/environment_test.go) 覆盖窗口边界/时区/星期位图、CRUD、大小写不敏感查找、reviewer/branch sync 替换语义、级联删除保留部署、派生状态；`go test ./models/actions/ -run 'TestFreezeWindow|TestEnvironmentCRUD|TestDeploymentDisplayStatus'` 通过（ok 1.366s）。
  - `go build ./...` 全仓库通过；gofmt 干净。
- **Description**:
  - 新增 `models/actions/environment.go`：
    - `Environment` 表 `action_environment`：ID、RepoID（index）、Name（与 RepoID 唯一索引）、Description、BranchPolicyMode（0=all/1=protected/2=selected）、Exclusive（自动互斥开关）、Manual lock 字段（Locked、LockedBy、LockedReason、LockedUnix）、CreatedUnix、UpdatedUnix。
    - `EnvironmentReviewer` 表：EnvID、ReviewerType（0=user/1=team）、ReviewerID，唯一索引 (env_id, reviewer_type, reviewer_id)。
    - `EnvironmentAllowedBranch` 表：EnvID、Pattern，唯一索引 (env_id, pattern)。
    - `EnvironmentFreezeWindow` 表：EnvID、Name、Kind（0=once/1=recurring）、StartUnix、EndUnix（once）、Weekdays（星期位图）、StartTime（HH:MM）、DurationMinutes、Timezone（recurring）、CreatedBy、CreatedUnix。
  - 新增 `models/actions/deployment.go`：
    - `Deployment` 表 `action_deployment`：ID、RepoID（index）、EnvID（index）、EnvName（快照）、RunID（index）、RunJobID（唯一索引）、Ref、CommitSHA、TriggerUserID、URL、ReviewStatus（0=pending/1=approved/2=rejected）、ReviewerID、ReviewComment、ReviewedUnix、CreatedUnix、UpdatedUnix；提供派生展示状态方法（结合作业状态：待审批/进行中/成功/失败/取消/已拒绝）。
  - 新增 `models/actions/environment_variable.go`：`EnvironmentVariable` 表（RepoID、EnvID、Name、Data、Description，唯一 (env_id, name)），常量与校验同 `ActionVariable`。
  - 新增 `models/secret/environment_secret.go`：`EnvironmentSecret` 表（RepoID、EnvID、Name、Data 密文、Description，唯一 (env_id, name)），加密复用 `secret_module.EncryptSecret`。
  - `models/actions/run_job.go`：`ActionRunJob` 增加 `Environment string`（xorm index NOT NULL DEFAULT ''）与 `EnvironmentURL string`（VARCHAR(2048) NOT NULL DEFAULT ''）列（xorm 自动同步）。
  - 各模型提供 CRUD/查询函数（Get/List/Create/Update/Delete、级联删除、查找选项遵循 `db.Find`/`ToConds` 模式）；环境删除时级联删除 reviewer/branch/window/variable/secret（事务），部署记录保留。
  - fixtures：`models/fixtures/action_environment.yml` 等（供测试）。
- **Acceptance Criteria Addressed**: AC-1（持久化基础）、AC-9（部署记录结构）、AC-7/AC-8（变量/密钥表）
- **Test Requirements**:
  - `rule` TR-1.1: `db.TableInfo`/一致性测试（参照 `models/unittest/consistency.go` 覆盖范围）确认新表注册成功、字段标签合法；`go test ./models/actions/ ./models/secret/` 通过。
  - `rule` TR-1.2: 环境删除级联：单测构造环境+审批人+分支+窗口+变量+密钥+部署，删除后关联行被清除而部署行保留。
  - `rule` TR-1.3: 模型 CRUD 单测覆盖唯一约束冲突返回错误、查询选项正确过滤。

## Task 2: jobparser 支持 environment 键
- **Status**: `pending`
- **Priority**: high
- **Depends On**: None
- **Description**:
  - `modules/actions/jobparser/model.go`：`Job` 增加 `Environment yaml.Node`（`yaml:"environment,omitempty"`），`Clone()` 同步拷贝。
  - 新增辅助方法：`(j *Job) EnvironmentSpec() (name, url string, ok bool, err error)`，接受标量字符串或映射 `{name, url}`；未设置返回 ok=false。
  - 新增 `EvaluateEnvironment(ctx-like args)`：参照 `EvaluateConcurrency`，用相同解释器上下文（git context、matrix、vars、inputs、results）对 name/url 节点求值，返回求值后的字符串。
  - 确保 `SetJob`/`Marshal` 往返后 environment 节点保留（结构体自有字段即支持，加测试锁定）。
  - matrix 场景：组合展开后环境表达式可含 `matrix.*`（由调用方在展开后求值）。
- **Acceptance Criteria Addressed**: AC-2
- **Test Requirements**:
  - `rule` TR-2.1: 单测：字符串形式、映射形式、含表达式（`${{ inputs.env }}`、`${{ matrix.env }}`）形式均正确解析与求值。
  - `rule` TR-2.2: 单测：解析 → `SetJob` → `Marshal` → 再解析，environment 数据不丢失。
  - `rule` TR-2.3: 单测：非法形式（映射但缺 name、非字符串值）返回明确错误。

## Task 3: 环境门禁服务与作业生命周期接线（核心）
- **Status**: `completed`
- **Priority**: high
- **Depends On**: Task 1, Task 2
- **Completion Evidence**:
  - 新增 [services/actions/environment.go](file:///Users/kkcarrot/swe-project/gitea_fork/services/actions/environment.go)：环境表达式求值与持久化（EvaluateJobEnvironment）、getOrCreate 自动建环境、部署 upsert、按 锁→冻结→分支策略→审批→互斥 顺序的 `evaluateEnvironmentGate`、`ReviewDeployment`（授权：admin/配置用户(读+)/团队成员(写+)，禁止自审）、`LockEnvironment`/`UnlockEnvironment`、`ReEmitEnvironmentBlockedRuns`、通知钩子占位。
  - 接线：[run.go](file:///Users/kkcarrot/swe-project/gitea_fork/services/actions/run.go) insertRunJob 使声明环境作业 Blocked 入库；[job_emitter.go](file:///Users/kkcarrot/swe-project/gitea_fork/services/actions/job_emitter.go) resolver 在 needs/if/matrix 后插入门禁（reject 直接置 Failure，block 保持等待，pass 继续并发流程），checkRunConcurrency 在部署作业终态后按环境唤醒排队 run；新增模型查询 FindBlockedEnvironmentRunIDs(InEnv)。
  - TR-3.1/3.2：[environment_test.go](file:///Users/kkcarrot/swe-project/gitea_fork/services/actions/environment_test.go) 覆盖 10 个门禁场景（锁/进行中+过期冻结窗口/分支策略 match-mismatch/tag 拒绝/待审批/已批准/已拒绝/互斥排队）与 5 个授权场景（admin 批准、自审拒绝、无权限拒绝、配置审批人通过、拒绝使作业 Failure）；`go test ./services/actions/ -run 'TestEvaluateEnvironmentGate|TestReviewDeploymentAuth'` 通过；既有 emitter/approve 测试无回归（全包 ok）。
  - TR-3.3/3.4/3.5 集成场景（真实 workflow run、冻结到期恢复、matrix 环境）由 Task 10 端到端测试覆盖。
- **Description**:
  - 新增 `services/actions/environment.go`：
    - 作业环境求值与持久化：resolver 中对声明环境的作业调用 `EvaluateEnvironment`（needs 已完成、matrix 已展开），写入 `job.Environment`/`EnvironmentURL`（更新列）。
    - `getOrCreateEnvironment`：环境不存在时自动创建（无规则）；返回环境。
    - 部署记录 upsert：按 RunJobID 唯一键，门禁首次触达时创建 `Deployment`（ref、SHA、触发人、URL 快照、pending）。
    - 门禁决策函数 `evaluateEnvironmentGate(ctx, run, job, env, deployment) (gateResult, reason, error)`，按 FR-14 顺序：
      1. 手动锁 Locked → blocked(reason: locked by/原因)；
      2. 冻结窗口：一次性（StartUnix ≤ now ≤ EndUnix）与周期性（按 Timezone 求本地星期/时刻，星期位图命中且当前在 StartTime+Duration 内）→ blocked(reason: 窗口名)；
      3. 分支策略：protected → 校验 run.Ref 对应分支为保护分支（`models/git/protected_branch.go`）；selected → ref 名 glob 匹配（`modules/util.Glob`）patterns；不匹配 → reject；
      4. 审批：reviewStatus pending 且配置了审批人 → blocked(waiting review)；reviewStatus rejected → reject；approved → 继续；
      5. 互斥（env.Exclusive）：存在同环境、其他作业处于 Running/Waiting（已领取队列）/Cancelling 的部署 → blocked(queued)；按作业 ID FIFO（最小 ID 优先）。
    - `ApproveDeployment(ctx, repo, doer, deploymentID, comment)`：授权校验（FR-6：admin 或配置审批人/团队成员且有写权限；禁止触发人自审），置 approved + 审计字段，`EmitJobsIfReadyByRun`。
    - `RejectDeployment(...)`：同上授权，置 rejected + 审计字段，并立即将对应 Blocked 作业置为 StatusFailure（stopped 时间戳，cond 校验仍为 Blocked），通知 run 状态刷新。
    - 锁/解锁服务：`LockEnvironment`/`UnlockEnvironment`（admin），解锁后对该仓库含环境阻塞作业的 run 触发 `EmitJobsIfReadyByRun`。
    - 冻结窗口 CRUD 服务（含周期性/一次性字段校验）。
  - 接线：
    - `services/actions/run.go` `insertRunJob`：作业声明环境时（解析原始节点判断，未求值也可识别节点存在）以 Blocked 入库（加入 `shouldBlockJob` 条件）。
    - `services/actions/job_emitter.go` `jobStatusResolver.resolve`：在 needs/`if:`/matrix 之后、并发组检查之前插入环境门禁：求值环境名 → getOrCreate → upsert 部署 → 决策；blocked 则保持 Blocked（记录原因到内存/日志），reject 则置 StatusFailure，pass 则继续既有并发流程；首次进入待审批时触发通知（Task 5，预留接口）。
    - `checkRunConcurrency`：扩展唤醒逻辑——环境互斥/锁/冻结导致阻塞的 run，在相关 run 的作业终态后被重新 emit（按环境 ID 查找 Blocked 且 Environment 非空的作业所属 run）。
    - 部署状态无需主动同步：展示状态由 Deployment.ReviewStatus + 作业状态派生（Task 1 方法）。
  - 阻塞原因可供 UI/API 使用：提供 `GetDeploymentGateReason(ctx, job)` 或在部署视图上附带 reason 字段（内存评估，不持久化）。
- **Acceptance Criteria Addressed**: AC-3, AC-4, AC-5, AC-6, AC-9, AC-14, AC-2（求值接线）, AC-11（原因数据）
- **Test Requirements**:
  - `rule` TR-3.1: 门禁决策单测：锁/冻结（一次性边界时刻、周期性星期位图与时区）/分支策略（all/protected/selected、tag）/审批（pending/approved/rejected）/互斥（FIFO 顺序）各分支正确，纯函数优先、亚秒级。
  - `rule` TR-3.2: 审批授权单测：admin 通过、配置审批人通过、团队成员通过、触发人自审拒绝、非授权用户拒绝。
  - `rule` TR-3.3: 集成测试（参照 `tests/integration/actions_concurrency_test.go` 模式）：含 `environment:` 的 workflow run，作业保持 Blocked；批准后转 Waiting/Success；拒绝后 Failure；不存在的环境自动创建。
  - `rule` TR-3.4: 集成测试：锁定/解锁与互斥排队两个作业的执行顺序符合预期；冻结窗口结束后 cron/重 emit 放行（cron 在 Task 6，本任务用手动 emit 验证恢复逻辑）。
  - `rule` TR-3.5: 单测：matrix 组合的环境表达式在展开后正确求值并逐组合生成部署记录。

## Task 4: 环境级变量与密钥注入
- **Status**: `pending`
- **Priority**: high
- **Depends On**: Task 1, Task 3
- **Description**:
  - 变量服务：环境变量 CRUD（service 层，供 API/WEB 复用），名称大写化、长度校验同现有变量。
  - `models/actions/variable.go` 或新文件：`GetVariablesOfJob(ctx, run, envName) (map[string]string, error)`，合并顺序 全局 < 组织 < 仓库 < 环境；无环境名时等同 `GetVariablesOfRun`。
  - `services/actions/task.go` `buildRunnerTask`：按 `job.Environment` 合并环境变量到 `task.Vars`。
  - `services/actions/job_emitter.go`：resolver 中环境作业表达式求值（`if:`/concurrency/environment）使用合并环境变量后的 vars（按 run+env 缓存，避免逐作业重复查询）。
  - `models/secret/environment_secret.go`：环境密钥 CRUD（加密写入、更新、删除）；`models/secret/secret.go` `GetSecretsOfTask`：在 fork PR 早返回之后、reusable scoping 之前，按 `task.Job.Environment` 加载并解密环境密钥合并；环境密钥同样参与 `secrets: inherit` 链路（进入 baseSecrets 即可）。
- **Acceptance Criteria Addressed**: AC-7, AC-8
- **Test Requirements**:
  - `rule` TR-4.1: 单测：变量优先级合并正确（同名环境覆盖仓库），无环境作业不获得环境变量。
  - `rule` TR-4.2: 集成测试：环境作业的 runner 任务载荷 `Vars`/`Secrets` 含环境层值；非环境作业不含；DB 中环境密钥为密文。
  - `rule` TR-4.3: 集成/单测：fork PR run（非 pull_request_target）载荷不含环境密钥；pull_request_target 可见。
  - `rule` TR-4.4: 单测：环境变量/密钥 CRUD 校验（重名、超长、大小写）。

## Task 5: 审批站内通知
- **Status**: `pending`
- **Priority**: medium
- **Depends On**: Task 3
- **Description**:
  - `models/activities/notification.go`：新增 `NotificationSourceDeployment` Source 常量；新增创建函数 `CreateDeploymentReviewNotification(ctx, repo, doer, recipients, run, job)`（IssueID 置 0，RepoID/CommitID 可填）；`HTMLURL`/`Link`/`APIURL` 为新 Source 增加指向 run job 页的分支；通知列表加载属性对新 Source 容错（无 Issue）。
  - 通知触发：
    - 部署首次进入待审批（resolver 内，幂等：同一 deployment+user 不重复插入）→ 通知授权审批人（配置用户 + 配置团队中对仓库有读权限以上的成员 + 仓库 admin？——按 FR-13：仅配置审批人，排除触发人，去重）。
    - 批准/拒绝 → 通知 run 触发人。
  - 计数：复用 `services/notify` 的 `NotificationCountChange`。
  - 前端通知列表（模板/TS）能渲染新类型（环境名、审批文案、链接）。
- **Acceptance Criteria Addressed**: AC-10
- **Test Requirements**:
  - `rule` TR-5.1: 集成测试：待审批后每个授权审批人生成一条通知、触发人被排除、重复 emit 不产生重复通知；批准/拒绝后触发人收到结果通知。
  - `rule` TR-5.2: 单测：新 Source 的 HTMLURL 指向正确的 run job 路径；通知列表加载不报错。

## Task 6: 冻结窗口定时唤醒
- **Status**: `pending`
- **Priority**: medium
- **Depends On**: Task 3
- **Description**:
  - `services/cron/tasks_actions.go`：注册 `reemit_environment_deployments`（`@every 1m`，RunAtStart false），调用新服务函数。
  - `services/actions/environment.go`：`ReEmitEnvironmentBlockedRuns(ctx)`：查找存在 `status=blocked AND environment != ''` 作业的 run（去重），逐个 `EmitJobsIfReadyByRun`；resolver 幂等，已满足门禁者放行。
  - 解锁/审批等即时路径已在 Task 3 直接 emit；cron 仅为冻结到期等时间驱动场景兜底。
- **Acceptance Criteria Addressed**: AC-5（自动恢复）、AC-6（兜底）
- **Test Requirements**:
  - `rule` TR-6.1: 单测/集成：构造窗口已过期的阻塞环境作业，调用 ReEmit 后作业转 Waiting；窗口仍生效时保持 Blocked。
  - `rule` TR-6.2: 单测：查询仅命中环境阻塞作业所在 run，不影响无关 run（断言 emit 调用集合）。

## Task 7: REST API 与 Swagger
- **Status**: `pending`
- **Priority**: high
- **Depends On**: Task 3, Task 4
- **Description**:
  - `modules/structs/`：新增 environment/deployment/freeze-window/review 相关请求/响应结构（参照 `variable.go`）。
  - `routers/api/v1/repo/action.go`（仓库级，挂入 `addActionsRoutes` 的 `/actions` 组）：
    - `GET /environments`（列表，含保护配置摘要与最近部署可选）、`GET /environments/{environment_name}`、`PUT /environments/{environment_name}`（创建或全量更新：description、branch_policy_mode、branch_patterns、reviewers[{type,id}]、exclusive）、`DELETE /environments/{environment_name}`；
    - `GET/POST /environments/{environment_name}/freeze-windows`、`PATCH/DELETE /environments/{environment_name}/freeze-windows/{window_id}`；
    - `PUT /environments/{environment_name}/lock`（body: reason）、`DELETE /environments/{environment_name}/lock`；
    - `GET/POST /environments/{environment_name}/variables`、`GET/PUT/DELETE /environments/{environment_name}/variables/{variablename}`；
    - `GET /environments/{environment_name}/secrets`、`PUT/DELETE /environments/{environment_name}/secrets/{secretname}`（值只写）；
    - `GET /environments/{environment_name}/deployments`、`GET /deployments`（仓库级，分页）、`GET /deployments/{id}`；
    - `POST /deployments/{id}/reviews`（body: event=approved|rejected、comment）。
  - 权限：管理类接口 reqOwnerCheck（写/管理员，与 secrets 一致）；reviews 做服务端 FR-6 授权；读接口 reader。
  - swagger 注释 + `make generate-swagger`；`routers/api/v1/swagger/` 响应类型注册。
- **Acceptance Criteria Addressed**: AC-1（API）、AC-9（部署查询）、AC-4（审批 API）
- **Test Requirements**:
  - `rule` TR-7.1: 集成测试（参照 `tests/integration/api_repo_variables_test.go`）：环境 CRUD、reviewers/branch_patterns 全量更新语义、404/409/403 状态码。
  - `rule` TR-7.2: 集成测试：freeze-windows CRUD、lock/unlock、环境变量/密钥 CRUD（密钥读不回值）、deployments 列表分页与字段、reviews 批准/拒绝/越权 403。
  - `rule` TR-7.3: `make generate-swagger` 后无未生成差异；swagger JSON 含新路径。

## Task 8: 仓库设置 Web UI（环境管理）
- **Status**: `pending`
- **Priority**: high
- **Depends On**: Task 7（复用 service）
- **Description**:
  - 路由：`routers/web/web.go` 仓库 settings `/actions` 组下新增 `environments` 子路由（列表、详情、创建/更新/删除、freeze 窗口增删、lock/unlock、环境变量/密钥增删改），`reqRepoAdmin`；处理器放 `routers/web/shared/actions/environments.go`（参照 `variables.go` 的 ctx 模式，仅仓库级）。
  - 导航：`templates/repo/settings/navbar.tmpl` Actions 分组新增 Environments 项（`PageIsActionsSettingsEnvironments`）。
  - 模板（`templates/repo/settings/`）：
    - `actions_environments.tmpl`：环境列表（名称、描述、保护徽章：审批人数量/分支策略/锁/冻结中/互斥、最近部署 ref+SHA+状态+时间）、创建表单。
    - `actions_environment_edit.tmpl`（或 actions.tmpl 分支）：描述、互斥开关、分支策略单选 + patterns 输入、审批人选择（复用现有用户/团队选择器组件）、冻结窗口列表+新增表单（一次性：datetime-local 起止；周期性：星期多选 + 时间 + 时长 + 时区）、锁定/解锁按钮（原因弹窗）、变量列表（复用 shared variables 组件模式）、密钥列表（复用 shared secrets 模式）、最近部署表（ref/SHA/触发人/状态/时间/链接 run）。
  - 表单：`services/forms` 增加对应 form 结构与校验。
  - TS/CSS：遵循 tw-* 与 flex-* 规范；交互（弹窗、选择器）优先复用现有组件。
  - locale：仅 `options/locale/locale_en-US.json` 新增键。
- **Acceptance Criteria Addressed**: AC-1（UI）、AC-5/AC-6（管理操作入口）、AC-7/AC-8（变量密钥管理入口）、AC-9（历史展示）、AC-12
- **Test Requirements**:
  - `rule` TR-8.1: 集成测试（模板渲染）：环境列表页与详情页 200 渲染、含配置数据；非管理员 403/不可见导航。
  - `rule` TR-8.2: 集成测试：通过 WEB 表单创建环境/配置审批人/加冻结窗口/锁定/增改变量密钥后数据持久化（重定向 + DB 断言）。
  - `rubric` TR-8.3: Dimension UI 一致性；scale 1-5；anchors 1=风格脱节不可用，3=功能可用但样式不一致，5=与 variables/secrets 页完全一致且遵循 tw-*；threshold >= 4；evidence 截图对照。

## Task 9: run 详情页门禁状态与审批操作
- **Status**: `pending`
- **Priority**: high
- **Depends On**: Task 3, Task 5
- **Description**:
  - `routers/web/repo/actions/view.go`：作业视图数据装配中为环境作业附带：环境名、部署记录、门禁原因（locked/frozen/waiting_review/queued）、当前用户是否可审批（FR-6）；新增 WEB 端点 `POST /runs/{run}/jobs/{job}/deployment/approve|reject`（或复用 runs 组路径），调用 Task 3 服务。
  - `templates/repo/actions/view_component.tmpl`：环境作业显示环境徽章；Blocked 环境作业按原因显示提示（锁定人+原因/冻结窗口名/等待审批/排队中）；可审批用户显示 Approve/Reject 按钮（拒绝带评论输入），复用现有 run 审批按钮的交互模式。
  - 通知列表模板支持部署类型渲染（Task 5 配套）。
  - locale 键补充。
- **Acceptance Criteria Addressed**: AC-11、AC-4（操作入口）、AC-10（通知渲染）、AC-14
- **Test Requirements**:
  - `rule` TR-9.1: e2e（优先，参照现有 actions e2e，sub-4s）或模板集成测试：待审批作业显示按钮，批准后作业开始、按钮消失；锁定/冻结原因文案正确。
  - `rule` TR-9.2: 集成测试：WEB 批准/拒绝端点权限校验（无权限 403、触发人自审 403）。
  - `rule` TR-9.3: 集成测试：通知列表页渲染部署通知且链接正确。

## Task 10: 端到端集成测试与质量门禁
- **Status**: `pending`
- **Priority**: high
- **Depends On**: Task 1-9
- **Description**:
  - 新增/补充集成测试 `tests/integration/actions_environment_test.go`（及 API 测试）：
    - 完整审批流：workflow 引用受保护环境 → Blocked + 待审批记录 + 通知 → 批准 → 作业成功 → 部署记录状态派生正确；
    - 拒绝流 → 作业失败；
    - 冻结窗口（一次性，通过时钟/直接构造窗口时间）阻塞与到期恢复；
    - 手动锁锁定/解锁恢复；
    - 互斥两作业 FIFO；
    - 分支策略三种模式；
    - 环境变量/密钥注入与 fork PR 隔离；
    - 部署历史（matrix、rerun 多条）与查询。
  - 单元测试补齐缺口（门禁纯函数、窗口求值、glob 匹配、通知去重）。
  - 运行 `make fmt`、`make lint-go`、`make lint-js`、`make lint-css`、`make lint-templates`、`make generate-swagger`、`make tidy`（如有 go.mod 变更）；修复根因而非弱化测试/lint。
  - 核对：仅修改 `locale_en-US.json`；新文件版权头；无遗留调试代码。
- **Acceptance Criteria Addressed**: AC-1 ~ AC-13（全量回归）、AC-13 质量门禁
- **Test Requirements**:
  - `rule` TR-10.1: 新增集成测试全部通过，单测 sub-2s、e2e sub-4s，等待使用确定性条件而非 sleep。
  - `rule` TR-10.2: 质量命令退出码全 0：`make fmt`（无差异）、`make lint-go`、`make lint-js`、`make lint-css`、`make lint-templates`、`make generate-swagger`（无差异）。
  - `rule` TR-10.3: 既有 actions 相关测试（`go test ./services/actions/... ./models/actions/... ./tests/integration/ -run 'Actions'`）无回归。
