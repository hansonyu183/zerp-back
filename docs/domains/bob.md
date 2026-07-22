# BOB 后端业务域

## 1. 文档目的

本文定义 ZERP 后端 **BOB（Business Object Base）** 领域的业务模型、状态机、数据约束、事务边界和 API 契约，作为客户、供应商、员工、产品、服务、资金账户等基础业务对象的统一实现规范。

BOB 使用固定领域标识 `bob`。所有外部业务接口遵循：

```text
POST /bob/{entity}/{action}
```

首批实体标识为：

```text
customer
supplier
employee
product
service
fund-account
```

数据库内部名称可使用 `fund_account`，但 HTTP 路径、权限路径和对外 JSON 中必须始终使用 `fund-account`，不得混用。

## 2. 领域职责与边界

BOB 负责：

- 建立稳定的业务对象身份；
- 保存对象每次新建或编辑产生的独立版本；
- 执行草稿、提交、审核、驳回、生效和失效状态流转；
- 保证只有有效版本能被新的交易业务引用；
- 保存完整版本历史和状态变更审计轨迹；
- 为其他领域提供对象、版本和业务发生时快照所需的数据。

BOB 不负责：

- 销售、采购、库存、资金收付等交易流程；
- 修改已发生业务记录中的历史引用；
- 替交易领域决定需要保存哪些业务快照；
- 物理删除已经创建的对象、版本和审核记录；
- 绕过 APP 领域执行身份认证和 API 权限判断。

## 3. 聚合模型

### 3.1 业务对象聚合

一个 BOB 聚合由稳定对象、多个对象版本和追加式审计事件组成：

```text
BusinessObject (稳定身份)
  ├── Version 1
  ├── Version 2
  ├── ...
  └── AuditEvent 1..n
```

建议共享生命周期表，并为各实体建立类型化版本明细表：

| 模型 | 建议表名 | 用途 |
| --- | --- | --- |
| 业务对象 | `bob_objects` | 保存稳定身份、实体类型和当前指针 |
| 对象版本 | `bob_versions` | 保存版本号、状态和审核审计字段 |
| 状态事件 | `bob_audit_events` | 追加保存每次状态变化和意见 |
| 客户版本明细 | `bob_customer_versions` | 客户类型化业务字段，与版本一对一 |
| 供应商版本明细 | `bob_supplier_versions` | 供应商类型化业务字段，与版本一对一 |
| 员工版本明细 | `bob_employee_versions` | 员工类型化业务字段，与版本一对一 |
| 产品版本明细 | `bob_product_versions` | 产品类型化业务字段，与版本一对一 |
| 服务版本明细 | `bob_service_versions` | 服务类型化业务字段，与版本一对一 |
| 资金账户版本明细 | `bob_fund_account_versions` | 资金账户类型化业务字段，与版本一对一 |

业务字段尚未确定前，不应仅为追求通用性把全部正式字段长期存入无约束 JSONB。类型化明细表可以提供外键、唯一性、精度、长度和查询索引约束；共享表只承载所有实体一致的生命周期信息。

### 3.2 业务对象

`bob_objects` 至少包含：

| 字段 | 约束 | 说明 |
| --- | --- | --- |
| `id` | 主键 | 对象稳定标识，跨版本不变 |
| `entity` | 非空 | 对外实体标识 |
| `current_version_id` | 外键、非空 | 最新创建的版本 |
| `effective_version_id` | 外键、可空 | 当前可供新业务引用的版本 |
| `next_version_no` | 非空 | 下一个版本号，或使用等效安全分配机制 |
| `revision` | 非空 | 对象级乐观并发版本 |
| `created_at`、`created_by` | 非空 | 创建审计信息 |
| `updated_at`、`updated_by` | 非空 | 最后修改审计信息 |

`current_version_id` 和 `effective_version_id` 指向的版本必须属于同一 `object_id` 且实体类型一致。该跨行规则应由事务内校验配合数据库约束实现，不能只依赖客户端。由于对象与首个版本相互引用，创建时应预生成两个 ID，并把相关外键定义为可延迟到事务提交时检查，或采用经过评审的等效无循环结构；不得为绕开插入顺序而在事务提交后留下空的 `current_version_id`。

### 3.3 对象版本

`bob_versions` 至少包含：

| 字段 | 约束 | 说明 |
| --- | --- | --- |
| `id` | 主键 | 版本标识 |
| `object_id` | 外键、非空 | 所属稳定对象 |
| `version_no` | 非空 | 对象内从 1 递增 |
| `status` | 非空 | 生命周期状态 |
| `revision` | 非空 | 版本级乐观并发版本 |
| `created_at`、`created_by` | 非空 | 版本创建信息 |
| `submitted_at`、`submitted_by` | 可空 | 最近一次提交信息 |
| `reviewed_at`、`reviewed_by` | 可空 | 最近一次审核信息 |
| `review_comment` | 可空 | 最近一次审核意见 |

数据库约束至少包括：

- `(object_id, version_no)` 唯一；
- 每个版本恰好存在一条与实体类型匹配的版本明细；
- 每个对象最多一个 `EFFECTIVE` 版本；
- 每个对象最多一个处于 `DRAFT`、`PENDING` 或 `REJECTED` 的候选版本；
- `PENDING` 必须具有提交人和提交时间；
- `EFFECTIVE`、`REJECTED` 必须具有审核人和审核时间；
- 提交人与审核人不得相同。

可用 PostgreSQL 部分唯一索引保证“最多一个有效版本”和“最多一个候选版本”。无法用简单约束表达的跨表规则必须集中在领域服务和数据库集成测试中验证。

### 3.4 审计事件

`bob_audit_events` 是追加式记录，至少包含：

- `id`、`object_id`、`version_id`；
- `entity`、`event_type`；
- `from_status`、`to_status`；
- `actor_id`、`occurred_at`；
- `comment`；
- `request_id`；
- 必要且经过脱敏的变更摘要。

事件类型至少包括 `CREATED`、`EDIT_STARTED`、`SAVED`、`SUBMITTED`、`APPROVED`、`REJECTED`、`INVALIDATED`。业务事务回滚时，对应审计事件也必须回滚，禁止记录未发生的状态变化。

## 4. 生命周期状态机

### 4.1 状态定义

| 状态 | 可修改业务字段 | 可提交 | 可审核 | 可被新业务引用 |
| --- | ---: | ---: | ---: | ---: |
| `DRAFT` | 是 | 是 | 否 | 否 |
| `PENDING` | 否 | 否 | 是 | 否 |
| `REJECTED` | 是 | 是 | 否 | 否 |
| `EFFECTIVE` | 否 | 否 | 否 | 是 |
| `INVALID` | 否 | 否 | 否 | 否 |

允许的状态转换只有：

```text
create:  (none)    → DRAFT
submit: DRAFT      → PENDING
submit: REJECTED   → PENDING
approve: PENDING   → EFFECTIVE
reject:  PENDING   → REJECTED
edit:    EFFECTIVE → INVALID，并创建新的 DRAFT
```

除上述转换外全部拒绝。尤其禁止：

- 直接创建 `EFFECTIVE` 版本；
- 修改 `PENDING`、`EFFECTIVE` 或 `INVALID` 的业务字段；
- 将 `REJECTED` 直接改为 `EFFECTIVE`；
- 驳回新版本后自动恢复旧版本；
- 通过普通保存接口修改状态或审计字段。

### 4.2 编辑即失效

发起编辑必须在同一事务内：

1. 锁定 `bob_objects` 当前行；
2. 校验对象 `revision` 和当前有效版本；
3. 将原 `EFFECTIVE` 版本更新为 `INVALID`；
4. 清空 `effective_version_id`；
5. 创建版本号递增的新 `DRAFT` 版本及明细；
6. 更新 `current_version_id` 和对象 `revision`；
7. 写入原版本失效和新版本创建审计事件。

任一步失败必须全部回滚。事务提交后到新版本再次审核通过前，该对象没有可供新业务引用的有效版本。

### 4.3 驳回后重提

驳回不创建新版本。用户可在原 `REJECTED` 版本上保存修正；保存增加版本 `revision`，但 `version_no` 不变。再次提交时覆盖该版本的“最近一次提交”字段，同时所有历史提交、审核过程保留在 `bob_audit_events` 中。

## 5. 领域动作与 API

所有首批实体提供相同动作：

| 动作 | 路径 | 说明 |
| --- | --- | --- |
| 查询 | `/bob/{entity}/query` | 分页查询对象及当前版本摘要 |
| 查看 | `/bob/{entity}/get` | 查看对象和指定版本详情 |
| 新建 | `/bob/{entity}/create` | 创建对象、首个草稿和明细 |
| 发起编辑 | `/bob/{entity}/edit` | 使有效版本失效并复制为新草稿 |
| 保存草稿 | `/bob/{entity}/save` | 保存 `DRAFT` 或 `REJECTED` 版本 |
| 提交审核 | `/bob/{entity}/submit` | 转为 `PENDING` |
| 审核通过 | `/bob/{entity}/approve` | 转为 `EFFECTIVE` |
| 审核驳回 | `/bob/{entity}/reject` | 转为 `REJECTED` |
| 查看版本 | `/bob/{entity}/versions` | 查询对象全部历史版本 |
| 审核记录 | `/bob/{entity}/audit-history` | 查询状态与审核事件 |

每条路径都是独立 APP 权限。后端通过路由元数据绑定权限标识，禁止 Handler 自行用字符串前缀或角色名称判断权限。

## 6. 请求与响应契约

### 6.1 查询

请求示例：

```json
{
  "page": 1,
  "pageSize": 20,
  "filters": {
    "keyword": "示例",
    "status": ["DRAFT", "PENDING", "EFFECTIVE"]
  },
  "sort": [
    { "field": "updatedAt", "order": "desc" }
  ]
}
```

成功响应 `data`：

```json
{
  "items": [
    {
      "objectId": "01J...",
      "entity": "customer",
      "objectRevision": 3,
      "currentVersion": {
        "versionId": "01J...",
        "version": 2,
        "status": "PENDING",
        "revision": 4,
        "summary": {}
      },
      "effectiveVersionId": null,
      "updatedAt": "2026-07-22T10:00:00Z"
    }
  ],
  "total": 1,
  "page": 1,
  "pageSize": 20
}
```

各实体必须定义允许过滤、排序和关键字匹配的字段白名单。客户端字段名和排序方向不得拼接进 SQL；`pageSize` 必须设上限。

### 6.2 查看

请求可以指定某一历史版本；未指定时读取 `current_version_id`：

```json
{
  "objectId": "01J...",
  "versionId": "01J..."
}
```

后端必须校验 `versionId` 属于 `objectId` 且实体与路径一致。响应返回对象元数据、版本元数据和当前实体的类型化 `data`，不得仅凭版本 ID 跨实体读取数据。

### 6.3 新建

请求示例：

```json
{
  "data": {}
}
```

成功后创建稳定对象、版本号 1 的 `DRAFT` 版本和类型化明细，返回：

```json
{
  "objectId": "01J...",
  "objectRevision": 1,
  "versionId": "01J...",
  "version": 1,
  "status": "DRAFT",
  "revision": 1
}
```

对象、版本、明细和 `CREATED` 审计事件必须在单个事务中写入。

### 6.4 发起编辑

请求：

```json
{
  "objectId": "01J...",
  "objectRevision": 2
}
```

只有存在当前 `EFFECTIVE` 版本且不存在候选版本时允许执行。新草稿默认复制有效版本的业务字段，返回新 `versionId`、递增后的版本号、对象 `revision` 和版本 `revision`。

### 6.5 保存草稿

请求：

```json
{
  "objectId": "01J...",
  "versionId": "01J...",
  "revision": 2,
  "data": {}
}
```

只允许保存 `DRAFT` 或 `REJECTED`。更新必须匹配 `versionId`、`objectId`、实体、允许状态和 `revision`，成功后 `revision + 1`。客户端不能修改 `version`、`status` 和任何审计字段。

### 6.6 提交审核

请求：

```json
{
  "objectId": "01J...",
  "versionId": "01J...",
  "revision": 3
}
```

提交前重新执行该实体的完整业务字段、唯一性和关联有效性校验。只有 `DRAFT` 或 `REJECTED` 可提交。事务内更新状态、提交人/时间、版本 `revision` 并写入 `SUBMITTED` 事件；从 `REJECTED` 重提时清空版本行上的“最近一次审核”字段，上一轮审核事实继续保留在 `bob_audit_events` 中。

### 6.7 审核通过

请求：

```json
{
  "objectId": "01J...",
  "versionId": "01J...",
  "revision": 4,
  "comment": "审核通过"
}
```

事务内必须：

1. 锁定对象和目标版本；
2. 校验目标是对象当前版本、状态为 `PENDING` 且 `revision` 匹配；
3. 校验当前用户不是该次提交人；
4. 重新校验关键唯一性和关联约束；
5. 将版本改为 `EFFECTIVE`；
6. 设置 `effective_version_id` 并增加对象 `revision`；
7. 写入审核字段和 `APPROVED` 事件。

### 6.8 审核驳回

请求与审核通过相同，但 `comment` 必填且必须满足长度限制。只有当前 `PENDING` 版本可驳回，提交人不能审核自己的提交。事务内改为 `REJECTED` 并写入 `REJECTED` 事件；对象的 `effective_version_id` 保持为空。

### 6.9 版本与审核历史

`versions` 请求：

```json
{
  "objectId": "01J...",
  "page": 1,
  "pageSize": 20
}
```

按 `version_no desc` 返回版本元数据和展示摘要。`audit-history` 使用相同分页结构，按事件发生时间和事件 ID 稳定倒序。读取历史详情仍需对应 `get`、`versions` 或 `audit-history` 权限策略明确授权，不能因知道 ID 绕过实体权限。

## 7. 并发与事务规则

### 7.1 乐观并发

- 对象级动作使用 `objectRevision`；
- 版本内容及状态动作使用版本 `revision`；
- 更新 SQL 必须把预期 revision 放入 `WHERE` 条件；
- revision 不匹配返回稳定“数据冲突”业务码，并返回最少必要的当前版本信息供前端刷新；
- 重复提交、重复审核、保存已经变为待审核的数据都属于冲突，不得按成功处理。

### 7.2 数据库锁

创建新版本、编辑、提交、审核通过和审核驳回必须在事务内按固定顺序锁定对象行，再锁定版本行，避免死锁和交错状态流转。部分唯一索引作为最后一致性防线；约束冲突应转换为领域冲突，不能把 PostgreSQL 错误文本返回客户端。

### 7.3 幂等边界

读接口天然幂等。状态写操作默认采用 revision 防止重复执行，不应把第二次审核伪装为首次成功。若客户端重试需求引入幂等键，应统一使用请求幂等表并限定作用域、有效期和响应重放规则，不能由各实体自行实现不同语义。

## 8. 有效引用规则

交易领域创建新记录时，必须同时保存：

- `object_id`：稳定对象标识；
- `version_id`：业务发生时引用的版本标识；
- 交易领域要求的名称、编码、税务信息等业务快照。

BOB 提供内部领域能力 `ResolveEffectiveReference(entity, objectId, versionId)`。交易写入必须在自身数据库事务中调用该能力，并确认：

1. 对象和版本存在且实体匹配；
2. `bob_objects.effective_version_id = versionId`；
3. 版本状态为 `EFFECTIVE`；
4. 当前操作者满足必要的数据范围规则。

仅在前端下拉框加载时有效不构成写入保证。为避免“校验后、交易写入前”发生编辑失效，交易事务应对对象行取得与 BOB 编辑更新互斥的共享锁，或采用经验证的等效数据库约束/串行化方案。

已经保存的历史业务引用不因版本后续变为 `INVALID` 而失效、级联更新或删除。BOB 表禁止配置会破坏历史引用的级联删除。

## 9. 校验与唯一性

校验分为三层：

1. **传输校验**：JSON 类型、必填字段、长度、枚举和 ID 格式；
2. **实体校验**：各实体字段组合、精度、编码规则和条件必填；
3. **领域校验**：状态、提交人与审核人分离、唯一性、关联对象有效性和并发版本。

唯一性必须明确是“全历史唯一”“同实体对象唯一”还是“仅有效版本唯一”。在规则确定前不能只在应用层执行先查后写；最终规则应通过数据库唯一索引或排他约束兜底，并把约束冲突映射成字段级或领域冲突结果。

## 10. 权限与审计

- 所有接口先由 APP 中间件校验会话、CSRF 和完整 API 路径权限；
- `query` 是前端实体菜单准入权限，但不自动授予 `get`、`versions` 或其他动作；
- `approve` 与 `reject` 只授予审核角色，且仍需执行提交人与审核人分离校验；
- 后端从会话取得操作者，拒绝客户端传入 `createdBy`、`submittedBy` 或 `reviewedBy`；
- 保存动作的变更摘要与每次状态流转写入审计事件；
- 日志记录 `requestId`、实体、对象 ID、版本 ID、动作和结果类别，不记录完整敏感业务字段；
- 若未来引入数据范围权限，必须在列表和单对象读取中同时实施，防止通过 ID 绕过。

## 11. 错误分类

领域错误映射到项目统一稳定业务类别：

| 场景 | 类别 |
| --- | --- |
| 会话不存在或失效 | 未登录 |
| 缺少精确动作权限 | 无权限 |
| JSON、字段或状态参数非法 | 参数校验失败 |
| revision 过期、状态已变化、候选版本已存在 | 数据冲突 |
| 对象或版本不存在，或不属于路径实体 | 未找到或参数失败，按统一 API 契约固定 |
| 数据库或未知异常 | 内部异常 |

错误消息不能包含 SQL、约束名、堆栈、内部路径或敏感字段。对于调用者无权知道是否存在的对象，应统一表现为无权限或不可见，避免 ID 枚举。

## 12. 测试验收

每一种 BOB 实体复用同一组生命周期契约测试，并补充实体字段测试。至少覆盖：

1. 新建对象只产生 `DRAFT`，不能被有效引用；
2. 草稿提交、审核通过及提交人不能自审；
3. 审核驳回、修改和重新提交保留完整事件历史；
4. 编辑有效对象在同一事务内使旧版本失效并创建新草稿；
5. 编辑事务失败时旧有效版本保持有效；
6. 新版本待审或被驳回时旧版本不自动恢复；
7. 同一对象并发编辑只能有一个成功；
8. 过期 revision 的保存、提交和审核返回数据冲突；
9. 每个对象最多一个有效版本和一个候选版本；
10. 交易写入不能引用无效、待审、被驳回或已被编辑失效的版本；
11. 历史引用和快照不受后续版本状态变化影响；
12. 无权限用户不能通过查询、详情或猜测 ID 读取数据；
13. 数据库约束错误被转换为稳定业务错误且事务完整回滚。

## 13. 待决事项

- 六类实体的字段、编码、唯一性、必填和查询规则；
- 客户与供应商是否共享“业务伙伴”主体；
- 员工与 APP 用户、组织及岗位的关系；
- 产品与服务的分类、单位、价格、税率和多币种属性；
- 资金账户类型、币种、敏感字段加密和数据可见范围；
- 各实体审核角色、是否需要多级审核及委托审核；
- 是否允许主动停用有效对象；若允许，需要新增独立状态动作及业务规则；
- 对象编号是在草稿创建、提交还是审核通过时生成；
- 历史版本和审计记录的保留、归档和脱敏策略。
