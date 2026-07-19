# graph-memory 设计文档

日期：2026-07-19
状态：已确认

## 概述

基于 FalkorDB 的记忆系统，提供 `gmem-cli` 命令行工具。图 schema 对齐 [graphiti](https://github.com/getzep/graphiti) 的 FalkorDB 实现。**推理（实体抽取、失效判断、社区总结、结果相关性判断）由调用方 agent 完成；gmem-cli 负责确定性的持久化、检索、向量生成与图算法。**

- 语言：Go
- 架构：库优先 —— `pkg/gmem` 为可导入核心库（后续 MCP server 直接复用），`cmd/gmem-cli` 为 cobra 薄壳
- 交付物包含一份 skill 文档（`skills/gmem/SKILL.md`），教 agent 如何使用 gmem-cli

## 依赖

**运行时外部依赖仅两个：**

| 依赖 | 用途 | 配置 |
|---|---|---|
| FalkorDB | 图存储 + 向量索引 + 全文索引 | `FALKORDB_ADDR` |
| OpenAI 兼容 embedding API | 生成 name_embedding / fact_embedding | `EMBEDDING_API_BASE/KEY/MODEL` |

**无 LLM（chat 模型）依赖** —— 与 graphiti 最大区别：抽取、判失效、总结全部由 agent 完成。可完全内网/离线运行（embedding 指向本地 vLLM/Ollama 等 OpenAI 兼容端点）。

**Go 库依赖（最小集）：**

| 库 | 用途 |
|---|---|
| `falkordb-go` | FalkorDB 客户端 |
| `cobra` | CLI 框架 |
| `yaml` | 配置文件解析 |
| `resty/v3` | HTTP 客户端（embedding API 调用） |
| `log/slog` | 结构化日志（标准库，统一打到 stderr） |

## 第一层：图设计

### 节点（4 种基础 label）

| label | 属性 | 说明 |
|---|---|---|
| `Episodic` | uuid, name, group_id, content, source(message/text/json), source_description, episode_metadata{}, entity_edges[], created_at, valid_at | 原始事件片段；分类靠 source/metadata 属性，固定单 label |
| `Entity` | uuid, name, group_id, summary, name_embedding(vecf32), created_at, attributes | 实体；多 label 存储：`:Entity:Person:Project` |
| `Community` | uuid, name, group_id, summary, name_embedding, created_at | label propagation 聚类结果 |
| `Saga` | uuid, name, group_id, summary, first_episode_uuid, last_episode_uuid, last_summarized_at, last_summarized_episode_valid_at, created_at | 增量总结水位线 |

### 边（3 种）

| 边 | 方向 | 属性 |
|---|---|---|
| `MENTIONS` | Episodic → Entity | uuid, group_id, created_at |
| `RELATES_TO` | Entity → Entity | uuid, group_id, name, fact, fact_embedding(vecf32), episodes[], created_at, valid_at, invalid_at, expired_at, attributes |
| `HAS_MEMBER` | Community → Entity | uuid, group_id, created_at |

### 类型与 label 的对应

- 每个实体节点必然有 `Entity` 基础 label（检索、索引基于它）
- 配置中 `entity_types.Person` → 写入时 `SET n:Person`，节点为 `:Entity:Person`
- 支持多个类型 label（`:Entity:Person:Manager`）；**第一个在配置中有定义的类型 label 决定 attribute 校验 schema**（与 graphiti "第一个非 Entity label 决定语义" 的约定一致）
- 多 label 随时间演化：同一实体不同时期被抽成不同类型，merge/dedup 时 labels 取并集（对齐 graphiti dedup_helpers 行为）
- `MATCH (n:Entity:Person)` = 所有 Person；`MATCH (n:Entity)` = 所有实体
- `edge_types.WORKS_ON.source: [Person]` 校验源节点是否带 `:Person` label
- Episodic/Community/Saga 为固定单 label，分类靠属性（与 graphiti 一致）

### 时序语义

- fact 不可变：事实变化 = 旧边标记 `invalid_at` + 新建边；历史保留可查
- 时点查询 `search --as-of T`：返回 `valid_at ≤ T` 且（`invalid_at` 为空 或 `invalid_at > T`）的边
- "失效"（曾经是事实，现在不是）与"删除"（从来不该存在）语义区分

### 索引

- 向量：`Entity.name_embedding`、`Community.name_embedding`、`RELATES_TO.fact_embedding`（vecf32）
- 全文（RedisSearch）：`Entity(name, summary)`、`RELATES_TO(name, fact)`、`Episodic(content)`
- 范围：所有节点/边的 `uuid`、`group_id`

### 类型配置（gmem.yaml，可选）

```yaml
entity_types:
  Person:
    description: "真实人物"
    attributes:
      role: { type: string, required: true }
      team: { type: string }
  Project:
    attributes:
      status: { type: "enum:active|paused|done", required: true }
      repo: { type: string }
edge_types:
  WORKS_ON:
    source: [Person]
    target: [Project]
```

校验规则（upsert/add 时执行）：

1. 必填属性缺失 → 报错（exit 1，stderr 给出缺失字段）
2. 属性类型不匹配（如 enum 值越界）→ 报错
3. 未定义的属性 → 默认报错；`--lenient` 跳过校验直接写入
4. edge 端点类型不符 → 报错
5. 不配置 entity_types/edge_types = 无校验，完全自由

## 第二层：命令设计

### 组合命令（agent 日常使用）

**`gmem-cli add --content --source [--entities JSON] [--edges JSON] [--metadata JSON] [--valid-at] [--group-id]`**

一次完成 graphiti `add_episode` 的持久化部分：

1. 建 Episodic 节点
2. upsert 每个 Entity（按 name+group_id 去重，生成 name_embedding，校验 attributes，建多 label）
3. 建 MENTIONS 边
4. 建 RELATES_TO 边（生成 fact_embedding，追加 episodes 列表）
5. 回写 episode.entity_edges

`--entities` / `--edges` 的 JSON 结构（edges 用实体 name 引用，由 CLI 解析为 uuid）：

```json
--entities '[{"name":"Alice","labels":["Person"],"summary":"后端工程师","attributes":{"role":"backend"}}]'
--edges    '[{"source":"Alice","target":"graph-memory","name":"WORKS_ON","fact":"Alice 参与 graph-memory 开发","valid_at":"2026-07-19T00:00:00Z"}]'
```

返回全部 uuid 的 JSON。

**`gmem-cli add-triplet --source --name --fact --target [--group-id] [--valid-at]`**

单条事实快速写入（对齐 graphiti `add_triplet`）：按 name 去重 source/target 实体（不存在则创建），建 RELATES_TO 边并生成 fact_embedding。写入的三档粒度：`add-triplet`（单条）→ `add`（结构化批量）→ `edge upsert`（底层精细控制）。

**`gmem-cli search --query [--limit] [--as-of] [--group-id] [--include-invalid]`**

混合检索一次返回 entities + facts + episodes：

- 多路召回：query 生成向量做余弦相似度检索 ∪ RedisSearch 全文匹配
- 排序：**RRF**（Reciprocal Rank Fusion，纯算法无模型依赖）；每条结果带 score 字段
- 时序过滤：默认不返回已失效边；`--as-of T` 时点查询；`--include-invalid` 含历史
- 结果相关性最终由 agent 阅读判断（agent 即 reranker）
- 扩展点（非 v1）：可选 reranker 模型（OpenAI 兼容 rerank 接口），配置驱动，不改命令接口

### 原子命令（精细操作/维护）

```
gmem-cli init                                   # 建索引
gmem-cli status                                 # FalkorDB 连通性、索引状态、embedding API 可用性
gmem-cli schema show                            # 输出类型定义（agent 抽取前先读）
gmem-cli episode get --uuid | episode list [--group-id] [--limit]
gmem-cli entity get --uuid | entity search --query [--limit]
gmem-cli entity update --uuid [--name] [--summary] [--attributes JSON] [--replace]
                                                # attributes 默认合并；--replace 整体替换；改 name 自动重算 embedding
gmem-cli entity merge --from <uuid> --to <uuid> # 边重接 + attributes 合并 + labels 取并集 + 删 from
gmem-cli edge upsert --source-uuid --target-uuid --name --fact [--episode-uuid] [--valid-at]
gmem-cli edge invalidate --uuid --invalid-at
gmem-cli edge search --query [--limit] [--include-invalid]
gmem-cli node delete --uuid                     # 级联删相关边
gmem-cli edge delete --uuid
gmem-cli saga create|get|update                 # 增量总结水位线管理
gmem-cli community build [--group-id]           # label propagation 聚类，输出 cluster 列表
gmem-cli community upsert --name --summary --member-uuids   # agent 总结后写回
```

### 修改的三种场景

1. **事实变了** → `edge invalidate` + `add`/`add-triplet` 新建边（不改边）
2. **认识加深** → `entity update`（attributes 默认合并）
3. **记错了** → `node delete` / `edge delete`（物理删除）
4. **发现重复实体** → `entity merge --from A --to B`

### 约定

- 全部 stdout 输出 JSON；日志（slog）与错误 → stderr；错误 exit 1
- 配置：环境变量（`FALKORDB_ADDR`、`FALKORDB_GRAPH`、`EMBEDDING_API_BASE`、`EMBEDDING_API_KEY`、`EMBEDDING_MODEL`）+ `--config gmem.yaml`

## 与 graphiti 命令对照

| graphiti (MCP/core) | gmem-cli | 差异 |
|---|---|---|
| `add_memory` | `add` | graphiti 内部 LLM 抽取；我们接收 agent 预抽取结果 |
| `add_triplet` | `add-triplet` | 对齐 |
| `search_nodes` + `search_memory_facts` | `search`（合体）+ `entity search` / `edge search`（单类型） | 增加 `--as-of` 时点查询 |
| `get_episodes` / `get_episode_entities` | `episode get` / `episode list` | episode get 返回内含实体 |
| `get_entity_edge` | （`edge search`/`node delete` 等按 uuid 操作） | — |
| `delete_entity_edge` / `delete_episode` | `edge delete` / `node delete` | node delete 扩展到任意节点并级联 |
| `build_communities` | `community build` + `community upsert` | graphiti 内部 LLM 总结；我们分两步 |
| `summarize_saga` | `saga create/get/update` | 总结推理交给 agent |
| 无（内部 LLM dedup） | `entity merge` | agent 发现重复后调用 |
| 无（内部 LLM 判失效） | `edge invalidate` | 暴露给 agent |
| 无（类型定义在代码里） | `schema show` | 类型定义在配置文件 |
| `get_status` | `status` | 对齐 |
| `clear_graph` | 不实现 | 危险操作，直接操作 FalkorDB |

## 架构

```
graph-memory/
├── pkg/gmem/              # 可导入核心库（后续 MCP server 复用）
│   ├── client.go          # FalkorDB 连接、索引初始化、status
│   ├── episode.go         # Episode CRUD
│   ├── entity.go          # Entity CRUD/upsert/merge
│   ├── edge.go            # RELATES_TO CRUD + 时序失效
│   ├── search.go          # 向量/全文召回 + RRF 排序
│   ├── community.go       # label propagation
│   ├── saga.go            # Saga 水位线
│   ├── schema.go          # 类型配置加载与校验
│   └── embed.go           # OpenAI 兼容 embedding 客户端（resty/v3）
├── cmd/gmem-cli/main.go   # cobra 薄壳
└── skills/gmem/SKILL.md   # 教 agent 使用 gmem-cli
```

## 测试

- pkg 层集成测试：testcontainer（或 docker compose）起 FalkorDB；embedding 客户端 mock（resty 层 httptest）
- 核心验证：add/search 闭环、时序过滤（as-of、include-invalid）、schema 校验、entity merge 边重接与 labels 并集、add-triplet 实体去重

## Skill 文档要点（skills/gmem/SKILL.md）

教 agent：

- 开始前 `status` 确认环境；抽取前先 `schema show` 了解已有类型；一个实体一个主类型放最前
- 何时 `add`（对话/事件发生后，抽取 entities + edges 一次写入）；单条事实用 `add-triplet`
- 事实变化用 `edge invalidate` + `add`/`add-triplet`，不要 update 边
- 定期 `community build` 聚类 → agent 总结 → `community upsert` 写回
- 发现重复实体用 `entity merge`
- 用 `search --as-of` 回答历史时点问题
