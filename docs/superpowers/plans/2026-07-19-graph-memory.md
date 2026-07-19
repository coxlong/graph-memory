# graph-memory 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 gmem-cli —— 基于 FalkorDB 的 agent 记忆系统 CLI（图 schema 对齐 graphiti），核心逻辑在可复用的 `pkg/gmem` 库中。

**Architecture:** `pkg/gmem`（库）+ `cmd/gmem-cli`（cobra 薄壳）。推理由调用方 agent 完成，CLI 只做持久化、检索、向量生成与图算法。所有命令 stdout 输出 JSON，slog 日志走 stderr。

**Tech Stack:** Go 1.22+、`github.com/FalkorDB/falkordb-go/v2`、`github.com/spf13/cobra`、`gopkg.in/yaml.v3`、`github.com/go-resty/resty/v3`、`github.com/google/uuid`、`log/slog`。

**Spec:** `docs/superpowers/specs/2026-07-19-graph-memory-design.md`

## Global Constraints

- 所有命令：结果 JSON → stdout，日志/错误 → stderr，出错 exit 1
- 无 LLM（chat 模型）依赖；外部依赖仅 FalkorDB + OpenAI 兼容 embedding API
- 时间一律存储为 UTC RFC3339 字符串（`time.Now().UTC().Format(time.RFC3339)`），时序过滤依赖字符串字典序比较
- 时间戳参数解析：`time.Parse(time.RFC3339, s)` 后 `.UTC().Format(time.RFC3339)` 归一化
- label 名校验正则：`^[A-Za-z_][A-Za-z0-9_]*$`（labels 需字符串插值进 Cypher，防注入）
- **对 spec 的一处偏离（与 graphiti 现行实现对齐，已确认）**：不建向量索引；相似度用 Cypher 内 `(2 - vec.cosineDistance(v, vecf32($vec)))/2` 计算，免维度配置。spec 中"向量索引"一条按此实现
- `attributes` / `episode_metadata` 以 **JSON 字符串**存储在属性中（FalkorDB 属性不支持嵌套 map），Go 侧 marshal/unmarshal
- 集成测试依赖环境变量 `FALKORDB_TEST_ADDR`（默认 `localhost:6379`），不可用时 `t.Skip`；测试用独立图名 `gmem_test_<纳秒时间戳>`，结束 `GRAPH.DELETE`
- 集成测试的 embedding 用 httptest 假服务器（返回固定向量），不打真实 API

## 测试基础设施（所有 pkg 测试共用）

每个集成测试文件复用这两个 helper（在 Task 2 的 `client_test.go` 中定义，后续任务直接用）：

```go
// newFakeEmbedServer 返回一个 httptest server，对 POST /embeddings 返回固定 8 维向量
func newFakeEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8]}]}`)
	}))
}

// newTestClient 连接 FALKORDB_TEST_ADDR，使用独立测试图，注册清理
func newTestClient(t *testing.T, embedURL string) *Client {
	t.Helper()
	addr := os.Getenv("FALKORDB_TEST_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	cfg := &Config{
		FalkorAddr: addr,
		Graph:      fmt.Sprintf("gmem_test_%d", time.Now().UnixNano()),
		EmbedBase:  embedURL,
		EmbedKey:   "test",
		EmbedModel: "test-model",
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Skipf("FalkorDB unavailable at %s: %v", addr, err)
	}
	ctx := context.Background()
	if err := c.db.Conn.Ping(ctx).Err(); err != nil {
		t.Skipf("FalkorDB unavailable at %s: %v", addr, err)
	}
	if err := c.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		c.db.Conn.Do(ctx, "GRAPH.DELETE", cfg.Graph)
	})
	return c
}
```

运行测试：`go test ./... `（需要本地 FalkorDB：`docker run -p 6379:6379 falkordb/falkordb:latest`）

---

### Task 1: 项目脚手架 + 配置 + Embedder

**Files:**
- Create: `go.mod`
- Create: `pkg/gmem/config.go`
- Create: `pkg/gmem/config_test.go`
- Create: `pkg/gmem/embed.go`
- Create: `pkg/gmem/embed_test.go`
- Create: `cmd/gmem-cli/main.go`

**Interfaces:**
- Produces:
  - `gmem.Config{FalkorAddr, Graph, EmbedBase, EmbedKey, EmbedModel, SchemaPath string}`
  - `gmem.LoadConfig(configPath string) (*Config, error)` — 先读 gmem.yaml（可选），环境变量覆盖
  - `gmem.NewEmbedder(base, key, model string) *Embedder`
  - `(*Embedder).Embed(text string) ([]float32, error)`
  - main 包 helper：`loadClient() (*gmem.Client, error)`、`printJSON(v any)`、`fatal(err error)`（后续所有 cmd 文件使用）
- 环境变量（verbatim）：`FALKORDB_ADDR`（默认 `localhost:6379`）、`FALKORDB_GRAPH`（默认 `gmem`）、`EMBEDDING_API_BASE`（默认 `https://api.openai.com/v1`）、`EMBEDDING_API_KEY`、`EMBEDDING_MODEL`（默认 `text-embedding-3-small`）、`GMEM_SCHEMA`（schema 文件路径）

- [ ] **Step 1: 初始化模块并写失败测试**

```bash
cd /home/work/workspace/graph-memory
go mod init github.com/coxlong/graph-memory
```

`pkg/gmem/config_test.go`:

```go
package gmem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FalkorAddr != "localhost:6379" || cfg.Graph != "gmem" || cfg.EmbedModel != "text-embedding-3-small" {
		t.Fatalf("bad defaults: %+v", cfg)
	}
}

func TestLoadConfigFileAndEnvOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gmem.yaml")
	os.WriteFile(p, []byte("falkordb_addr: file:1234\nembedding_model: file-model\n"), 0o644)
	t.Setenv("FALKORDB_ADDR", "env:6379")
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FalkorAddr != "env:6379" {
		t.Fatalf("env should override file: %s", cfg.FalkorAddr)
	}
	if cfg.EmbedModel != "file-model" {
		t.Fatalf("file value lost: %s", cfg.EmbedModel)
	}
}
```

`pkg/gmem/embed_test.go`:

```go
package gmem

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbed(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	e := NewEmbedder(srv.URL, "k", "m")
	vec, err := e.Embed("hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 8 || vec[0] != 0.1 {
		t.Fatalf("bad vector: %v", vec)
	}
}

func TestEmbedHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
	defer srv.Close()
	e := NewEmbedder(srv.URL, "k", "m")
	if _, err := e.Embed("x"); err == nil {
		t.Fatal("expected error on 500")
	}
}
```

注：`newFakeEmbedServer` 定义在 Task 2 的 `client_test.go` 中；为让本任务测试独立通过，先临时在 `embed_test.go` 顶部定义它，Task 2 时删除并移到 `client_test.go`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -v`
Expected: FAIL — `LoadConfig`/`NewEmbedder` undefined（编译错误）

- [ ] **Step 3: 实现 config.go / embed.go / main.go**

`pkg/gmem/config.go`:

```go
package gmem

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	FalkorAddr string `yaml:"falkordb_addr"`
	Graph      string `yaml:"falkordb_graph"`
	EmbedBase  string `yaml:"embedding_api_base"`
	EmbedKey   string `yaml:"embedding_api_key"`
	EmbedModel string `yaml:"embedding_model"`
	SchemaPath string `yaml:"schema_path"`
}

func LoadConfig(configPath string) (*Config, error) {
	cfg := &Config{
		FalkorAddr: "localhost:6379",
		Graph:      "gmem",
		EmbedBase:  "https://api.openai.com/v1",
		EmbedModel: "text-embedding-3-small",
	}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}
	env := map[string]*string{
		"FALKORDB_ADDR":      &cfg.FalkorAddr,
		"FALKORDB_GRAPH":     &cfg.Graph,
		"EMBEDDING_API_BASE": &cfg.EmbedBase,
		"EMBEDDING_API_KEY":  &cfg.EmbedKey,
		"EMBEDDING_MODEL":    &cfg.EmbedModel,
		"GMEM_SCHEMA":        &cfg.SchemaPath,
	}
	for k, ptr := range env {
		if v := os.Getenv(k); v != "" {
			*ptr = v
		}
	}
	return cfg, nil
}
```

`pkg/gmem/embed.go`:

```go
package gmem

import (
	"fmt"

	"github.com/go-resty/resty/v3"
)

type Embedder struct {
	client *resty.Client
	model  string
}

func NewEmbedder(base, key, model string) *Embedder {
	return &Embedder{
		client: resty.New().
			SetBaseURL(base).
			SetHeader("Authorization", "Bearer "+key),
		model: model,
	}
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed 调用 OpenAI 兼容的 POST {base}/embeddings
func (e *Embedder) Embed(text string) ([]float32, error) {
	var out embedResponse
	resp, err := e.client.R().
		SetBody(embedRequest{Model: e.model, Input: text}).
		SetResult(&out).
		Post("/embeddings")
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}
	if resp.IsError() {
		return nil, fmt.Errorf("embedding API %d: %s", resp.StatusCode(), resp.String())
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embedding API returned no data")
	}
	return out.Data[0].Embedding, nil
}
```

`cmd/gmem-cli/main.go`:

```go
package main

import (
	"encoding/json"
	"log/slog"
	"os"

	"github.com/coxlong/graph-memory/pkg/gmem"
	"github.com/spf13/cobra"
)

var configPath string

var rootCmd = &cobra.Command{
	Use:   "gmem-cli",
	Short: "Graph memory CLI for agents (FalkorDB + graphiti schema)",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "path to gmem.yaml")
}

func loadClient() (*gmem.Client, error) {
	cfg, err := gmem.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return gmem.NewClient(cfg)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	slog.Error("command failed", "err", err)
	os.Exit(1)
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

- [ ] **Step 4: 拉依赖并运行测试**

Run: `go mod tidy && go test ./pkg/gmem/ -v && go build ./cmd/gmem-cli && ./gmem-cli --help`
Expected: 测试 PASS；help 输出 root 命令

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum pkg/gmem cmd/gmem-cli
git commit -m "feat: scaffold project with config and embedder"
```

### Task 2: FalkorDB Client + init + status

**Files:**
- Create: `pkg/gmem/client.go`
- Create: `pkg/gmem/client_test.go`
- Create: `cmd/gmem-cli/cmd_admin.go`
- Delete: `pkg/gmem/embed_test.go` 中临时的 `newFakeEmbedServer`（移到 `client_test.go`）

**Interfaces:**
- Consumes: `Config`、`NewEmbedder`（Task 1）
- Produces:
  - `gmem.Client{Embed *Embedder, Schema *Schema}`（`db`、`graph` 为私有字段，供同包使用）
  - `gmem.NewClient(cfg *Config) (*Client, error)`
  - `(*Client).Init() error` — 建索引（幂等，重复执行不报错）
  - `(*Client).Status() *Status`，`Status{FalkorDB, Graph, Embedding string; IndexesOK bool}`
  - `(*Client).GroupID(g string) string` — 空串返回 `"default"`
  - `nowUTC() string`、`newUUID() string`（包内工具函数，后续任务使用）
  - CLI：`gmem-cli init`、`gmem-cli status`

- [ ] **Step 1: 写失败测试**

`pkg/gmem/client_test.go`（含"测试基础设施"节的两个 helper）:

```go
package gmem

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func newFakeEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8]}]}`)
	}))
}

func newTestClient(t *testing.T, embedURL string) *Client {
	t.Helper()
	addr := os.Getenv("FALKORDB_TEST_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	cfg := &Config{
		FalkorAddr: addr,
		Graph:      fmt.Sprintf("gmem_test_%d", time.Now().UnixNano()),
		EmbedBase:  embedURL,
		EmbedKey:   "test",
		EmbedModel: "test-model",
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Skipf("FalkorDB unavailable at %s: %v", addr, err)
	}
	ctx := context.Background()
	if err := c.db.Conn.Ping(ctx).Err(); err != nil {
		t.Skipf("FalkorDB unavailable at %s: %v", addr, err)
	}
	if err := c.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		c.db.Conn.Do(ctx, "GRAPH.DELETE", cfg.Graph)
	})
	return c
}

func TestInitIdempotent(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	// newTestClient 已 Init 一次；再调一次不应报错
	if err := c.Init(); err != nil {
		t.Fatalf("second init: %v", err)
	}
}

func TestStatus(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	st := c.Status()
	if st.FalkorDB != "ok" || st.Embedding != "ok" || !st.IndexesOK {
		t.Fatalf("bad status: %+v", st)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run TestInit -v`
Expected: FAIL — `NewClient` undefined（编译错误）

- [ ] **Step 3: 实现 client.go**

`pkg/gmem/client.go`:

```go
package gmem

import (
	"context"
	"fmt"
	"strings"
	"time"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
	"github.com/google/uuid"
)

type Client struct {
	cfg    *Config
	db     *falkordb.FalkorDB
	graph  *falkordb.Graph
	Embed  *Embedder
	Schema *Schema
}

func NewClient(cfg *Config) (*Client, error) {
	db, err := falkordb.FalkorDBNew(&falkordb.ConnectionOption{Addr: cfg.FalkorAddr})
	if err != nil {
		return nil, fmt.Errorf("connect falkordb: %w", err)
	}
	c := &Client{
		cfg:   cfg,
		db:    db,
		Embed: NewEmbedder(cfg.EmbedBase, cfg.EmbedKey, cfg.EmbedModel),
	}
	c.graph = db.SelectGraph(cfg.Graph)
	if cfg.SchemaPath != "" {
		s, err := LoadSchema(cfg.SchemaPath)
		if err != nil {
			return nil, err
		}
		c.Schema = s
	} else {
		c.Schema = &Schema{}
	}
	return c, nil
}

// GroupID 返回有效 group_id；空串归为 "default"
func (c *Client) GroupID(g string) string {
	if g == "" {
		return "default"
	}
	return g
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func newUUID() string { return uuid.NewString() }

// normalizeTime 解析 RFC3339 并归一化为 UTC 字符串；空串原样返回
func normalizeTime(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return "", fmt.Errorf("invalid RFC3339 time %q: %w", s, err)
	}
	return t.UTC().Format(time.RFC3339), nil
}

// indexQueries 对齐 graphiti 的 FalkorDB 索引（范围 + 全文）
var indexQueries = []string{
	"CREATE INDEX FOR (n:Entity) ON (n.uuid, n.group_id, n.name, n.created_at)",
	"CREATE INDEX FOR (n:Episodic) ON (n.uuid, n.group_id, n.created_at, n.valid_at)",
	"CREATE INDEX FOR (n:Community) ON (n.uuid)",
	"CREATE INDEX FOR (n:Saga) ON (n.uuid, n.group_id, n.name)",
	"CREATE INDEX FOR ()-[e:RELATES_TO]-() ON (e.uuid, e.group_id, e.name, e.created_at, e.valid_at, e.invalid_at)",
	"CREATE INDEX FOR ()-[e:MENTIONS]-() ON (e.uuid, e.group_id)",
	"CREATE INDEX FOR ()-[e:HAS_MEMBER]-() ON (e.uuid)",
	"CALL db.idx.fulltext.createNodeIndex('Entity', 'name', 'summary', 'group_id')",
	"CALL db.idx.fulltext.createNodeIndex('Episodic', 'content', 'source', 'source_description', 'group_id')",
	"CALL db.idx.fulltext.createNodeIndex('Community', 'name')",
	"CALL db.idx.fulltext.createRelationshipIndex('RELATES_TO', 'name', 'fact', 'group_id')",
}

// Init 建索引，幂等：已存在的索引跳过
func (c *Client) Init() error {
	for _, q := range indexQueries {
		if _, err := c.graph.Query(q, nil, nil); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already") {
				continue
			}
			return fmt.Errorf("init %q: %w", q, err)
		}
	}
	return nil
}

type Status struct {
	FalkorDB  string `json:"falkordb"`
	Graph     string `json:"graph"`
	IndexesOK bool   `json:"indexes_ok"`
	Embedding string `json:"embedding"`
}

func (c *Client) Status() *Status {
	st := &Status{Graph: c.cfg.Graph}
	if _, err := c.db.Conn.Ping(context.Background()).Result(); err != nil {
		st.FalkorDB = "error: " + err.Error()
	} else {
		st.FalkorDB = "ok"
	}
	if _, err := c.graph.Query("CALL db.indexes()", nil, nil); err == nil {
		st.IndexesOK = true
	}
	if _, err := c.Embed.Embed("ping"); err != nil {
		st.Embedding = "error: " + err.Error()
	} else {
		st.Embedding = "ok"
	}
	return st
}
```

- [ ] **Step 4: 写 CLI 命令 cmd_admin.go**

`cmd/gmem-cli/cmd_admin.go`:

```go
package main

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(initCmd, statusCmd)
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create graph indexes (idempotent)",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		if err := c.Init(); err != nil {
			fatal(err)
		}
		printJSON(map[string]string{"status": "ok"})
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check FalkorDB, indexes and embedding API",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		printJSON(c.Status())
	},
}
```

- [ ] **Step 5: 运行测试并验证 CLI**

Run: `docker run -d --name gmem-dev-falkor -p 6379:6379 falkordb/falkordb:latest`（若未运行）
Run: `go mod tidy && go test ./pkg/gmem/ -v`
Expected: TestInitIdempotent、TestStatus PASS（Task 1 测试也保持 PASS）

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: falkordb client with init and status"
```

---

### Task 3: Schema 类型配置与校验 + `schema show`

**Files:**
- Create: `pkg/gmem/schema.go`
- Create: `pkg/gmem/schema_test.go`
- Create: `cmd/gmem-cli/cmd_schema.go`

**Interfaces:**
- Consumes: 无（独立模块）
- Produces:
  - `gmem.Schema{EntityTypes map[string]EntityTypeDef, EdgeTypes map[string]EdgeTypeDef}`
  - `gmem.LoadSchema(path string) (*Schema, error)`
  - `(*Schema).PrimaryType(labels []string) string` — 第一个在配置中有定义的非 Entity label
  - `(*Schema).ValidateEntity(labels []string, attrs map[string]any, lenient bool) error`
  - `(*Schema).ValidateEdge(name string, sourceLabels, targetLabels []string, lenient bool) error`
  - `gmem.ValidateLabels(labels []string) error` — Cypher 安全校验
  - CLI：`gmem-cli schema show`（输出 Schema 的 JSON；无配置时输出 `{}`）

- [ ] **Step 1: 写失败测试**

`pkg/gmem/schema_test.go`（纯单元测试，不需要 FalkorDB）:

```go
package gmem

import (
	"os"
	"path/filepath"
	"testing"
)

const testSchemaYAML = `
entity_types:
  Person:
    description: "真实人物"
    attributes:
      role: { type: string, required: true }
      team: { type: string }
  Project:
    attributes:
      status: { type: "enum:active|paused|done", required: true }
edge_types:
  WORKS_ON:
    source: [Person]
    target: [Project]
`

func loadTestSchema(t *testing.T) *Schema {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gmem.yaml")
	if err := os.WriteFile(p, []byte(testSchemaYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSchema(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidateEntityOK(t *testing.T) {
	s := loadTestSchema(t)
	err := s.ValidateEntity([]string{"Person"}, map[string]any{"role": "backend"}, false)
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateEntityMissingRequired(t *testing.T) {
	s := loadTestSchema(t)
	err := s.ValidateEntity([]string{"Person"}, map[string]any{"team": "B"}, false)
	if err == nil {
		t.Fatal("expected missing required error")
	}
}

func TestValidateEntityEnum(t *testing.T) {
	s := loadTestSchema(t)
	if err := s.ValidateEntity([]string{"Project"}, map[string]any{"status": "bogus"}, false); err == nil {
		t.Fatal("expected enum error")
	}
	if err := s.ValidateEntity([]string{"Project"}, map[string]any{"status": "active"}, false); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEntityUndefinedAttr(t *testing.T) {
	s := loadTestSchema(t)
	if err := s.ValidateEntity([]string{"Person"}, map[string]any{"role": "x", "zzz": 1}, false); err == nil {
		t.Fatal("expected undefined attribute error")
	}
	if err := s.ValidateEntity([]string{"Person"}, map[string]any{"role": "x", "zzz": 1}, true); err != nil {
		t.Fatal("lenient should pass")
	}
}

func TestValidateEdgeEndpoints(t *testing.T) {
	s := loadTestSchema(t)
	if err := s.ValidateEdge("WORKS_ON", []string{"Entity", "Person"}, []string{"Entity", "Project"}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateEdge("WORKS_ON", []string{"Entity", "Project"}, []string{"Entity", "Person"}, false); err == nil {
		t.Fatal("expected endpoint type error")
	}
	if err := s.ValidateEdge("UNKNOWN_EDGE", []string{"Person"}, []string{"Project"}, false); err == nil {
		t.Fatal("expected undefined edge type error")
	}
}

func TestValidateLabelsInjection(t *testing.T) {
	if err := ValidateLabels([]string{"Person"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLabels([]string{"Person} DETACH DELETE n //"}); err == nil {
		t.Fatal("expected injection rejection")
	}
}

func TestPrimaryType(t *testing.T) {
	s := loadTestSchema(t)
	if got := s.PrimaryType([]string{"Entity", "Person", "Manager"}); got != "Person" {
		t.Fatalf("primary type: %q", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run 'TestValidate|TestPrimary' -v`
Expected: FAIL — `Schema` undefined（编译错误）

- [ ] **Step 3: 实现 schema.go**

`pkg/gmem/schema.go`:

```go
package gmem

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type AttributeDef struct {
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required" json:"required,omitempty"`
}

type EntityTypeDef struct {
	Description string                  `yaml:"description" json:"description,omitempty"`
	Attributes  map[string]AttributeDef `yaml:"attributes" json:"attributes,omitempty"`
}

type EdgeTypeDef struct {
	Description string   `yaml:"description" json:"description,omitempty"`
	Source      []string `yaml:"source" json:"source,omitempty"`
	Target      []string `yaml:"target" json:"target,omitempty"`
}

type Schema struct {
	EntityTypes map[string]EntityTypeDef `yaml:"entity_types" json:"entity_types,omitempty"`
	EdgeTypes   map[string]EdgeTypeDef   `yaml:"edge_types" json:"edge_types,omitempty"`
}

func LoadSchema(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Schema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}
	return &s, nil
}

var labelRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateLabels 校验 label 可安全插值进 Cypher
func ValidateLabels(labels []string) error {
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return fmt.Errorf("invalid label %q", l)
		}
	}
	return nil
}

// PrimaryType 返回第一个在配置中有定义的非 Entity label
func (s *Schema) PrimaryType(labels []string) string {
	for _, l := range labels {
		if l == "Entity" {
			continue
		}
		if _, ok := s.EntityTypes[l]; ok {
			return l
		}
	}
	return ""
}

func (s *Schema) ValidateEntity(labels []string, attrs map[string]any, lenient bool) error {
	if lenient || len(s.EntityTypes) == 0 {
		return nil
	}
	t := s.PrimaryType(labels)
	if t == "" {
		return fmt.Errorf("no configured entity type in labels %v", labels)
	}
	def := s.EntityTypes[t]
	for name, ad := range def.Attributes {
		v, ok := attrs[name]
		if !ok || v == nil {
			if ad.Required {
				return fmt.Errorf("missing required attribute %q for type %s", name, t)
			}
			continue
		}
		if err := checkAttrType(name, ad.Type, v); err != nil {
			return err
		}
	}
	for name := range attrs {
		if _, ok := def.Attributes[name]; !ok {
			return fmt.Errorf("undefined attribute %q for type %s (use --lenient to skip)", name, t)
		}
	}
	return nil
}

func checkAttrType(name, typ string, v any) error {
	switch {
	case typ == "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("attribute %q must be string", name)
		}
	case strings.HasPrefix(typ, "enum:"):
		sv, ok := v.(string)
		if !ok {
			return fmt.Errorf("attribute %q must be string (enum)", name)
		}
		allowed := strings.Split(strings.TrimPrefix(typ, "enum:"), "|")
		if !slices.Contains(allowed, sv) {
			return fmt.Errorf("attribute %q: %q not in enum [%s]", name, sv, strings.Join(allowed, " "))
		}
	case typ == "number":
		switch v.(type) {
		case int, int64, float64:
		default:
			return fmt.Errorf("attribute %q must be number", name)
		}
	}
	return nil
}

func (s *Schema) ValidateEdge(name string, sourceLabels, targetLabels []string, lenient bool) error {
	if lenient || len(s.EdgeTypes) == 0 {
		return nil
	}
	def, ok := s.EdgeTypes[name]
	if !ok {
		return fmt.Errorf("undefined edge type %q (use --lenient to skip)", name)
	}
	if !hasAny(sourceLabels, def.Source) {
		return fmt.Errorf("edge %s: source labels %v not in %v", name, sourceLabels, def.Source)
	}
	if !hasAny(targetLabels, def.Target) {
		return fmt.Errorf("edge %s: target labels %v not in %v", name, targetLabels, def.Target)
	}
	return nil
}

func hasAny(labels, allowed []string) bool {
	for _, l := range labels {
		if l == "Entity" {
			continue
		}
		if slices.Contains(allowed, l) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: CLI cmd_schema.go**

`cmd/gmem-cli/cmd_schema.go`:

```go
package main

import (
	"github.com/spf13/cobra"
)

func init() {
	schemaCmd.AddCommand(schemaShowCmd)
	rootCmd.AddCommand(schemaCmd)
}

var schemaCmd = &cobra.Command{Use: "schema", Short: "Type schema operations"}

var schemaShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print configured entity/edge types as JSON",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		printJSON(c.Schema)
	},
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./pkg/gmem/ -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: type schema config and validation"
```

### Task 4: Episode CRUD

**Files:**
- Create: `pkg/gmem/episode.go`
- Create: `pkg/gmem/episode_test.go`
- Create: `pkg/gmem/types.go`
- Create: `cmd/gmem-cli/cmd_episode.go`

**Interfaces:**
- Consumes: `Client`、`nowUTC`、`newUUID`、`normalizeTime`（Task 2）
- Produces:
  - `gmem.Episode{UUID, Name, GroupID, Content, Source, SourceDescription string; Metadata map[string]any; EntityEdges []string; CreatedAt, ValidAt string}`
  - `(*Client).CreateEpisode(ep *Episode) (*Episode, error)` — 空 UUID/CreatedAt 自动填充；`Source` 限 `message|text|json`
  - `(*Client).GetEpisode(uuid string) (*Episode, error)`
  - `(*Client).ListEpisodes(groupID string, limit int) ([]*Episode, error)` — 按 created_at 倒序
  - `types.go` 中的 `mapToJSON(m map[string]any) (string, error)` 和 `jsonToMap(s string) map[string]any`（attributes/metadata 序列化，后续 entity/edge 复用）
  - CLI：`gmem-cli episode get --uuid`、`gmem-cli episode list [--group-id] [--limit]`

- [ ] **Step 1: 写 types.go 和失败测试**

`pkg/gmem/types.go`:

```go
package gmem

import "encoding/json"

// mapToJSON 将 map 序列化为 JSON 字符串（FalkorDB 属性不支持嵌套 map）；nil 存 "{}"
func mapToJSON(m map[string]any) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	return string(b), err
}

// jsonToMap 反序列化 JSON 字符串为 map；空串返回空 map
func jsonToMap(s string) map[string]any {
	m := map[string]any{}
	if s != "" {
		_ = json.Unmarshal([]byte(s), &m)
	}
	return m
}

// strSlice 将 FalkorDB 返回的 []any 转为 []string
func strSlice(v any) []string {
	out := []string{}
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
```

`pkg/gmem/episode_test.go`:

```go
package gmem

import "testing"

func TestCreateAndGetEpisode(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	ep, err := c.CreateEpisode(&Episode{
		Name: "chat-1", Content: "user: hello", Source: "message",
		Metadata: map[string]any{"channel": "cli"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ep.UUID == "" || ep.CreatedAt == "" || ep.GroupID != "default" {
		t.Fatalf("bad episode: %+v", ep)
	}

	got, err := c.GetEpisode(ep.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "user: hello" || got.Metadata["channel"] != "cli" {
		t.Fatalf("bad get: %+v", got)
	}
}

func TestCreateEpisodeBadSource(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	_, err := c.CreateEpisode(&Episode{Content: "x", Source: "bogus"})
	if err == nil {
		t.Fatal("expected source validation error")
	}
}

func TestListEpisodes(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	for _, name := range []string{"e1", "e2"} {
		if _, err := c.CreateEpisode(&Episode{Name: name, Content: name, Source: "text"}); err != nil {
			t.Fatal(err)
		}
	}
	eps, err := c.ListEpisodes("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 {
		t.Fatalf("want 2, got %d", len(eps))
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run 'TestCreate|TestList' -v`
Expected: FAIL — `Episode` undefined（编译错误）

- [ ] **Step 3: 实现 episode.go**

`pkg/gmem/episode.go`:

```go
package gmem

import (
	"fmt"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

type Episode struct {
	UUID              string         `json:"uuid"`
	Name              string         `json:"name"`
	GroupID           string         `json:"group_id"`
	Content           string         `json:"content"`
	Source            string         `json:"source"`
	SourceDescription string         `json:"source_description,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	EntityEdges       []string       `json:"entity_edges,omitempty"`
	CreatedAt         string         `json:"created_at"`
	ValidAt           string         `json:"valid_at,omitempty"`
}

var validSources = map[string]bool{"message": true, "text": true, "json": true}

func (c *Client) CreateEpisode(ep *Episode) (*Episode, error) {
	if !validSources[ep.Source] {
		return nil, fmt.Errorf("invalid source %q (message|text|json)", ep.Source)
	}
	if ep.UUID == "" {
		ep.UUID = newUUID()
	}
	ep.GroupID = c.GroupID(ep.GroupID)
	if ep.CreatedAt == "" {
		ep.CreatedAt = nowUTC()
	}
	var err error
	if ep.ValidAt, err = normalizeTime(ep.ValidAt); err != nil {
		return nil, err
	}
	meta, err := mapToJSON(ep.Metadata)
	if err != nil {
		return nil, err
	}
	_, err = c.graph.Query(`CREATE (n:Episodic {
		uuid: $uuid, name: $name, group_id: $group_id, content: $content,
		source: $source, source_description: $sd, episode_metadata: $meta,
		entity_edges: $ee, created_at: $created_at, valid_at: $valid_at
	})`, map[string]any{
		"uuid": ep.UUID, "name": ep.Name, "group_id": ep.GroupID,
		"content": ep.Content, "source": ep.Source, "sd": ep.SourceDescription,
		"meta": meta, "ee": ep.EntityEdges, "created_at": ep.CreatedAt, "valid_at": ep.ValidAt,
	}, nil)
	if err != nil {
		return nil, err
	}
	return ep, nil
}

func episodeFromNode(n *falkordb.Node) *Episode {
	p := n.Properties
	ep := &Episode{
		UUID:              fmt.Sprint(p["uuid"]),
		Name:              fmt.Sprint(p["name"]),
		GroupID:           fmt.Sprint(p["group_id"]),
		Content:           fmt.Sprint(p["content"]),
		Source:            fmt.Sprint(p["source"]),
		SourceDescription: fmt.Sprint(p["source_description"]),
		Metadata:          jsonToMap(fmt.Sprint(p["episode_metadata"])),
		EntityEdges:       strSlice(p["entity_edges"]),
		CreatedAt:         fmt.Sprint(p["created_at"]),
		ValidAt:           fmt.Sprint(p["valid_at"]),
	}
	return ep
}

func (c *Client) GetEpisode(uuid string) (*Episode, error) {
	res, err := c.graph.ROQuery(`MATCH (n:Episodic {uuid: $uuid}) RETURN n`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return nil, err
	}
	if !res.Next() {
		return nil, fmt.Errorf("episode %s not found", uuid)
	}
	n, ok := res.Record().GetByIndex(0).(*falkordb.Node)
	if !ok {
		return nil, fmt.Errorf("unexpected record type")
	}
	return episodeFromNode(n), nil
}

func (c *Client) ListEpisodes(groupID string, limit int) ([]*Episode, error) {
	res, err := c.graph.ROQuery(`MATCH (n:Episodic {group_id: $gid})
		RETURN n ORDER BY n.created_at DESC LIMIT $limit`,
		map[string]any{"gid": c.GroupID(groupID), "limit": limit}, nil)
	if err != nil {
		return nil, err
	}
	out := []*Episode{}
	for res.Next() {
		if n, ok := res.Record().GetByIndex(0).(*falkordb.Node); ok {
			out = append(out, episodeFromNode(n))
		}
	}
	return out, nil
}
```

- [ ] **Step 4: CLI cmd_episode.go**

`cmd/gmem-cli/cmd_episode.go`:

```go
package main

import (
	"github.com/spf13/cobra"
)

var episodeUUID, episodeGroup string
var episodeLimit int

func init() {
	episodeGetCmd.Flags().StringVar(&episodeUUID, "uuid", "", "episode uuid")
	_ = episodeGetCmd.MarkFlagRequired("uuid")
	episodeListCmd.Flags().StringVar(&episodeGroup, "group-id", "", "group id")
	episodeListCmd.Flags().IntVar(&episodeLimit, "limit", 20, "max episodes")
	episodeCmd.AddCommand(episodeGetCmd, episodeListCmd)
	rootCmd.AddCommand(episodeCmd)
}

var episodeCmd = &cobra.Command{Use: "episode", Short: "Episode operations"}

var episodeGetCmd = &cobra.Command{
	Use: "get",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		ep, err := c.GetEpisode(episodeUUID)
		if err != nil {
			fatal(err)
		}
		printJSON(ep)
	},
}

var episodeListCmd = &cobra.Command{
	Use: "list",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		eps, err := c.ListEpisodes(episodeGroup, episodeLimit)
		if err != nil {
			fatal(err)
		}
		printJSON(eps)
	},
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./pkg/gmem/ -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: episode crud"
```

---

### Task 5: Entity CRUD + upsert + update + merge + `node delete`

**Files:**
- Create: `pkg/gmem/entity.go`
- Create: `pkg/gmem/entity_test.go`
- Create: `cmd/gmem-cli/cmd_entity.go`
- Create: `cmd/gmem-cli/cmd_node.go`

**Interfaces:**
- Consumes: `Schema.ValidateEntity`、`ValidateLabels`、`mapToJSON`、`Embedder.Embed`（Task 1/3/4）
- Produces:
  - `gmem.Entity{UUID, Name, GroupID, Summary, CreatedAt string; Labels []string; Attributes map[string]any}`
  - `(*Client).UpsertEntity(e *Entity, lenient bool) (*Entity, bool, error)` — 按 (name, group_id) MERGE 去重；bool 返回是否新建；自动生成 name_embedding 和多 label
  - `(*Client).GetEntity(uuid string) (*Entity, error)`
  - `(*Client).UpdateEntity(uuid string, name, summary string, attrs map[string]any, replace bool) (*Entity, error)` — 空 name/summary 不改；attrs 默认合并，replace=true 整体替换；改 name 重算 embedding
  - `(*Client).MergeEntities(fromUUID, toUUID string) (*Entity, error)` — 边重接 + attributes 合并 + labels 并集 + 删 from
  - `(*Client).DeleteNode(uuid string) error` — 任意标签节点，DETACH DELETE
  - CLI：`entity get --uuid`、`entity update`、`entity merge --from --to`、`node delete --uuid`

- [ ] **Step 1: 写失败测试**

`pkg/gmem/entity_test.go`:

```go
package gmem

import "testing"

func TestUpsertEntityDedup(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	e1, created1, err := c.UpsertEntity(&Entity{Name: "Alice", Labels: []string{"Person"}}, false)
	if err != nil || !created1 {
		t.Fatalf("first upsert: %v created=%v", err, created1)
	}
	e2, created2, err := c.UpsertEntity(&Entity{Name: "Alice"}, false)
	if err != nil || created2 {
		t.Fatalf("second upsert should dedup: %v created=%v", err, created2)
	}
	if e1.UUID != e2.UUID {
		t.Fatalf("dedup uuid mismatch: %s vs %s", e1.UUID, e2.UUID)
	}
}

func TestUpsertEntityLabelsAndEmbedding(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	e, _, err := c.UpsertEntity(&Entity{Name: "Bob", Labels: []string{"Person"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	// 直接查库验证 label 和 embedding 已写入
	res, err := c.graph.ROQuery(`MATCH (n {uuid: $uuid}) RETURN labels(n), n.name_embedding`,
		map[string]any{"uuid": e.UUID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Next()
	rec := res.Record()
	labels := strSlice(mustGet(t, rec, 0))
	if !contains(labels, "Entity") || !contains(labels, "Person") {
		t.Fatalf("labels: %v", labels)
	}
	if mustGet(t, rec, 1) == nil {
		t.Fatal("name_embedding not set")
	}
}

func TestUpdateEntityMergeAttrs(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	e, _, _ := c.UpsertEntity(&Entity{Name: "Carol", Attributes: map[string]any{"a": 1}}, false)
	got, err := c.UpdateEntity(e.UUID, "", "new summary", map[string]any{"b": 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "new summary" || got.Attributes["a"] != float64(1) && got.Attributes["a"] != 1 {
		t.Fatalf("merge failed: %+v", got)
	}
	if _, ok := got.Attributes["b"]; !ok {
		t.Fatal("attr b missing")
	}
}

func TestMergeEntities(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "Alice", Labels: []string{"Person"}, Attributes: map[string]any{"role": "be"}}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "Alice Wang", Labels: []string{"User"}}, false)
	x, _, _ := c.UpsertEntity(&Entity{Name: "ProjectX"}, false)
	if _, err := c.graph.Query(`MATCH (s:Entity {uuid:$s}), (t:Entity {uuid:$t})
		CREATE (s)-[:RELATES_TO {uuid:$e, group_id:'default'}]->(t)`,
		map[string]any{"s": a.UUID, "t": x.UUID, "e": newUUID()}, nil); err != nil {
		t.Fatal(err)
	}
	merged, err := c.MergeEntities(a.UUID, b.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(merged.Labels, "Person") || !contains(merged.Labels, "User") {
		t.Fatalf("labels union: %v", merged.Labels)
	}
	if merged.Attributes["role"] != "be" {
		t.Fatalf("attrs merged: %v", merged.Attributes)
	}
	// 边已重接到 b
	res, _ := c.graph.ROQuery(`MATCH (n:Entity {uuid:$u})-[r:RELATES_TO]->() RETURN count(r)`,
		map[string]any{"u": b.UUID}, nil)
	res.Next()
	if cnt, _ := res.Record().GetByIndex(0).(int64); cnt != 1 {
		t.Fatalf("rewired edges: %d", cnt)
	}
	// a 已删除
	if _, err := c.GetEntity(a.UUID); err == nil {
		t.Fatal("from entity should be deleted")
	}
}

func TestDeleteNode(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	e, _, _ := c.UpsertEntity(&Entity{Name: "Temp"}, false)
	if err := c.DeleteNode(e.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetEntity(e.UUID); err == nil {
		t.Fatal("node should be gone")
	}
}

// helpers
func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func mustGet(t *testing.T, rec interface{ GetByIndex(int) (any, error) }, i int) any {
	t.Helper()
	v, err := rec.GetByIndex(i)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run 'TestUpsert|TestUpdate|TestMerge|TestDelete' -v`
Expected: FAIL — `Entity` undefined（编译错误）

- [ ] **Step 3: 实现 entity.go**

`pkg/gmem/entity.go`:

```go
package gmem

import (
	"fmt"
	"strings"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

type Entity struct {
	UUID       string         `json:"uuid"`
	Name       string         `json:"name"`
	GroupID    string         `json:"group_id"`
	Labels     []string       `json:"labels,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	CreatedAt  string         `json:"created_at"`
}

// labelSet 返回去重且确保含 Entity 的 label 串（用于 SET n:Label1:Label2）
func labelSet(labels []string) string {
	seen := map[string]bool{"Entity": true}
	out := []string{"Entity"}
	for _, l := range labels {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return strings.Join(out, ":")
}

// UpsertEntity 按 (name, group_id) MERGE；返回实体和是否新建
func (c *Client) UpsertEntity(e *Entity, lenient bool) (*Entity, bool, error) {
	if err := ValidateLabels(e.Labels); err != nil {
		return nil, false, err
	}
	if err := c.Schema.ValidateEntity(e.Labels, e.Attributes, lenient); err != nil {
		return nil, false, err
	}
	gid := c.GroupID(e.GroupID)
	emb, err := c.Embed.Embed(e.Name)
	if err != nil {
		return nil, false, err
	}
	if e.UUID == "" {
		e.UUID = newUUID()
	}
	attrs, err := mapToJSON(e.Attributes)
	if err != nil {
		return nil, false, err
	}
	// 先尝试找已有实体
	res, err := c.graph.ROQuery(`MATCH (n:Entity {name: $name, group_id: $gid}) RETURN n LIMIT 1`,
		map[string]any{"name": e.Name, "gid": gid}, nil)
	if err != nil {
		return nil, false, err
	}
	if res.Next() {
		n, _ := res.Record().GetByIndex(0).(*falkordb.Node)
		return entityFromNode(n), false, nil
	}
	// 新建
	_, err = c.graph.Query(`CREATE (n:Entity {
		uuid: $uuid, name: $name, group_id: $gid, summary: $summary,
		attributes: $attrs, created_at: $created_at
	}) SET n:`+labelSet(e.Labels)+` SET n.name_embedding = vecf32($emb)`,
		map[string]any{
			"uuid": e.UUID, "name": e.Name, "gid": gid, "summary": e.Summary,
			"attrs": attrs, "created_at": nowUTC(), "emb": emb,
		}, nil)
	if err != nil {
		return nil, false, err
	}
	e.GroupID = gid
	e.CreatedAt = nowUTC()
	return e, true, nil
}

func entityFromNode(n *falkordb.Node) *Entity {
	p := n.Properties
	return &Entity{
		UUID:       fmt.Sprint(p["uuid"]),
		Name:       fmt.Sprint(p["name"]),
		GroupID:    fmt.Sprint(p["group_id"]),
		Labels:     n.Labels,
		Summary:    fmt.Sprint(p["summary"]),
		Attributes: jsonToMap(fmt.Sprint(p["attributes"])),
		CreatedAt:  fmt.Sprint(p["created_at"]),
	}
}

func (c *Client) GetEntity(uuid string) (*Entity, error) {
	res, err := c.graph.ROQuery(`MATCH (n:Entity {uuid: $uuid}) RETURN n`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return nil, err
	}
	if !res.Next() {
		return nil, fmt.Errorf("entity %s not found", uuid)
	}
	n, ok := res.Record().GetByIndex(0).(*falkordb.Node)
	if !ok {
		return nil, fmt.Errorf("unexpected record type")
	}
	return entityFromNode(n), nil
}

// UpdateEntity 更新实体；name/summary 空串不改；attrs 默认合并，replace 整体替换
func (c *Client) UpdateEntity(uuid, name, summary string, attrs map[string]any, replace bool) (*Entity, error) {
	e, err := c.GetEntity(uuid)
	if err != nil {
		return nil, err
	}
	sets := []string{}
	params := map[string]any{"uuid": uuid}
	if name != "" && name != e.Name {
		emb, err := c.Embed.Embed(name)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "n.name = $name", "n.name_embedding = vecf32($emb)")
		params["name"] = name
		params["emb"] = emb
	}
	if summary != "" {
		sets = append(sets, "n.summary = $summary")
		params["summary"] = summary
	}
	if attrs != nil {
		merged := attrs
		if !replace {
			merged = e.Attributes
			for k, v := range attrs {
				merged[k] = v
			}
		}
		s, err := mapToJSON(merged)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "n.attributes = $attrs")
		params["attrs"] = s
	}
	if len(sets) > 0 {
		if _, err := c.graph.Query(`MATCH (n:Entity {uuid: $uuid}) SET `+strings.Join(sets, ", "),
			params, nil); err != nil {
			return nil, err
		}
	}
	return c.GetEntity(uuid)
}

// MergeEntities 把 from 的边重接到 to，合并 attributes 与 labels，删除 from
func (c *Client) MergeEntities(fromUUID, toUUID string) (*Entity, error) {
	from, err := c.GetEntity(fromUUID)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	to, err := c.GetEntity(toUUID)
	if err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	// 出边重接（复制属性）
	if _, err := c.graph.Query(`MATCH (a:Entity {uuid: $from})-[r:RELATES_TO]->(m)
		MATCH (b:Entity {uuid: $to})
		CREATE (b)-[nr:RELATES_TO]->(m) SET nr = properties(r)`,
		map[string]any{"from": fromUUID, "to": toUUID}, nil); err != nil {
		return nil, err
	}
	// 入边重接
	if _, err := c.graph.Query(`MATCH (m)-[r:RELATES_TO]->(a:Entity {uuid: $from})
		MATCH (b:Entity {uuid: $to})
		CREATE (m)-[nr:RELATES_TO]->(b) SET nr = properties(r)`,
		map[string]any{"from": fromUUID, "to": toUUID}, nil); err != nil {
		return nil, err
	}
	// MENTIONS 重接
	if _, err := c.graph.Query(`MATCH (ep:Episodic)-[r:MENTIONS]->(a:Entity {uuid: $from})
		MATCH (b:Entity {uuid: $to})
		MERGE (ep)-[:MENTIONS]->(b)`,
		map[string]any{"from": fromUUID, "to": toUUID}, nil); err != nil {
		return nil, err
	}
	// 合并 attributes（to 优先）与 labels（并集）
	mergedAttrs := from.Attributes
	for k, v := range to.Attributes {
		mergedAttrs[k] = v
	}
	labelUnion := to.Labels
	for _, l := range from.Labels {
		if !containsStr(labelUnion, l) {
			labelUnion = append(labelUnion, l)
		}
	}
	if err := ValidateLabels(labelUnion); err != nil {
		return nil, err
	}
	attrsJSON, err := mapToJSON(mergedAttrs)
	if err != nil {
		return nil, err
	}
	if _, err := c.graph.Query(`MATCH (b:Entity {uuid: $to})
		SET b.attributes = $attrs SET b:`+strings.Join(labelUnion, ":"),
		map[string]any{"to": toUUID, "attrs": attrsJSON}, nil); err != nil {
		return nil, err
	}
	// 删除 from（连带其旧边）
	if _, err := c.graph.Query(`MATCH (a:Entity {uuid: $from}) DETACH DELETE a`,
		map[string]any{"from": fromUUID}, nil); err != nil {
		return nil, err
	}
	return c.GetEntity(toUUID)
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// DeleteNode 删除任意标签节点并级联删边
func (c *Client) DeleteNode(uuid string) error {
	res, err := c.graph.Query(`MATCH (n {uuid: $uuid}) DETACH DELETE n RETURN count(n) AS cnt`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return err
	}
	if res.Next() {
		if cnt, _ := res.Record().GetByIndex(0).(int64); cnt == 0 {
			return fmt.Errorf("node %s not found", uuid)
		}
	}
	return nil
}
```

- [ ] **Step 4: CLI cmd_entity.go + cmd_node.go**

`cmd/gmem-cli/cmd_entity.go`:

```go
package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

var (
	entityUUID, entityName, entitySummary, entityAttrs, entityFrom, entityTo string
	entityReplace bool
)

func init() {
	entityGetCmd.Flags().StringVar(&entityUUID, "uuid", "", "entity uuid")
	_ = entityGetCmd.MarkFlagRequired("uuid")

	entityUpdateCmd.Flags().StringVar(&entityUUID, "uuid", "", "entity uuid")
	entityUpdateCmd.Flags().StringVar(&entityName, "name", "", "new name")
	entityUpdateCmd.Flags().StringVar(&entitySummary, "summary", "", "new summary")
	entityUpdateCmd.Flags().StringVar(&entityAttrs, "attributes", "", "attributes JSON")
	entityUpdateCmd.Flags().BoolVar(&entityReplace, "replace", false, "replace attributes instead of merge")
	_ = entityUpdateCmd.MarkFlagRequired("uuid")

	entityMergeCmd.Flags().StringVar(&entityFrom, "from", "", "source entity uuid (deleted)")
	entityMergeCmd.Flags().StringVar(&entityTo, "to", "", "target entity uuid (kept)")
	_ = entityMergeCmd.MarkFlagRequired("from")
	_ = entityMergeCmd.MarkFlagRequired("to")

	entityCmd.AddCommand(entityGetCmd, entityUpdateCmd, entityMergeCmd)
	rootCmd.AddCommand(entityCmd)
}

var entityCmd = &cobra.Command{Use: "entity", Short: "Entity operations"}

var entityGetCmd = &cobra.Command{
	Use: "get",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		e, err := c.GetEntity(entityUUID)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}

var entityUpdateCmd = &cobra.Command{
	Use: "update",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		var attrs map[string]any
		if entityAttrs != "" {
			if err := json.Unmarshal([]byte(entityAttrs), &attrs); err != nil {
				fatal(err)
			}
		}
		e, err := c.UpdateEntity(entityUUID, entityName, entitySummary, attrs, entityReplace)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}

var entityMergeCmd = &cobra.Command{
	Use: "merge",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		e, err := c.MergeEntities(entityFrom, entityTo)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}
```

`cmd/gmem-cli/cmd_node.go`:

```go
package main

import (
	"github.com/spf13/cobra"
)

var nodeUUID string

func init() {
	nodeDeleteCmd.Flags().StringVar(&nodeUUID, "uuid", "", "node uuid")
	_ = nodeDeleteCmd.MarkFlagRequired("uuid")
	nodeCmd.AddCommand(nodeDeleteCmd)
	rootCmd.AddCommand(nodeCmd)
}

var nodeCmd = &cobra.Command{Use: "node", Short: "Node operations"}

var nodeDeleteCmd = &cobra.Command{
	Use: "delete",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		if err := c.DeleteNode(nodeUUID); err != nil {
			fatal(err)
		}
		printJSON(map[string]string{"status": "ok"})
	},
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./pkg/gmem/ -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: entity crud, upsert, update, merge, node delete"
```

### Task 6: Edge CRUD + invalidate

**Files:**
- Create: `pkg/gmem/edge.go`
- Create: `pkg/gmem/edge_test.go`
- Create: `cmd/gmem-cli/cmd_edge.go`

**Interfaces:**
- Consumes: `GetEntity`、`Schema.ValidateEdge`、`mapToJSON`（Task 3/4/5）
- Produces:
  - `gmem.Edge{UUID, Name, GroupID, Fact, CreatedAt, ValidAt, InvalidAt, ExpiredAt string; Episodes []string; Attributes map[string]any; SourceUUID, TargetUUID string}`
  - `(*Client).UpsertEdge(e *Edge, lenient bool) (*Edge, error)` — 按 uuid MERGE；生成 fact_embedding；校验端点类型
  - `(*Client).GetEdge(uuid string) (*Edge, error)`
  - `(*Client).InvalidateEdge(uuid, invalidAt string) (*Edge, error)` — invalidAt 为空用当前时间
  - `(*Client).DeleteEdge(uuid string) error`
  - CLI：`edge upsert`、`edge invalidate`、`edge delete`

- [ ] **Step 1: 写失败测试**

`pkg/gmem/edge_test.go`:

```go
package gmem

import "testing"

func TestUpsertAndGetEdge(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "Alice"}, false)
	p, _, _ := c.UpsertEntity(&Entity{Name: "ProjX"}, false)

	e, err := c.UpsertEdge(&Edge{
		Name: "WORKS_ON", Fact: "Alice works on ProjX",
		SourceUUID: a.UUID, TargetUUID: p.UUID,
		ValidAt: "2026-07-19T00:00:00Z",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetEdge(e.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fact != "Alice works on ProjX" || got.SourceUUID != a.UUID || got.TargetUUID != p.UUID {
		t.Fatalf("bad edge: %+v", got)
	}
}

func TestInvalidateEdge(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "A"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "B"}, false)
	e, _ := c.UpsertEdge(&Edge{Name: "KNOWS", Fact: "A knows B", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)

	got, err := c.InvalidateEdge(e.UUID, "2026-07-19T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if got.InvalidAt != "2026-07-19T12:00:00Z" {
		t.Fatalf("invalid_at: %q", got.InvalidAt)
	}
}

func TestInvalidateEdgeDefaultTime(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "A2"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "B2"}, false)
	e, _ := c.UpsertEdge(&Edge{Name: "KNOWS", Fact: "x", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)
	got, err := c.InvalidateEdge(e.UUID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.InvalidAt == "" {
		t.Fatal("invalid_at should default to now")
	}
}

func TestDeleteEdge(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "A3"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "B3"}, false)
	e, _ := c.UpsertEdge(&Edge{Name: "KNOWS", Fact: "x", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)
	if err := c.DeleteEdge(e.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetEdge(e.UUID); err == nil {
		t.Fatal("edge should be gone")
	}
}

func TestUpsertEdgeMissingEndpoint(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	b, _, _ := c.UpsertEntity(&Entity{Name: "B4"}, false)
	_, err := c.UpsertEdge(&Edge{Name: "X", Fact: "x", SourceUUID: newUUID(), TargetUUID: b.UUID}, false)
	if err == nil {
		t.Fatal("expected missing endpoint error")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run 'TestUpsertEdge|TestUpsertAndGetEdge|TestInvalidate|TestDeleteEdge' -v`
Expected: FAIL — `Edge` undefined（编译错误）

- [ ] **Step 3: 实现 edge.go**

`pkg/gmem/edge.go`:

```go
package gmem

import (
	"fmt"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

type Edge struct {
	UUID       string         `json:"uuid"`
	Name       string         `json:"name"`
	GroupID    string         `json:"group_id"`
	Fact       string         `json:"fact"`
	Episodes   []string       `json:"episodes,omitempty"`
	CreatedAt  string         `json:"created_at"`
	ValidAt    string         `json:"valid_at,omitempty"`
	InvalidAt  string         `json:"invalid_at,omitempty"`
	ExpiredAt  string         `json:"expired_at,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	SourceUUID string         `json:"source_uuid"`
	TargetUUID string         `json:"target_uuid"`
}

// UpsertEdge 按 uuid MERGE RELATES_TO 边；生成 fact_embedding；校验端点类型
func (c *Client) UpsertEdge(e *Edge, lenient bool) (*Edge, error) {
	src, err := c.GetEntity(e.SourceUUID)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	tgt, err := c.GetEntity(e.TargetUUID)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	if err := c.Schema.ValidateEdge(e.Name, src.Labels, tgt.Labels, lenient); err != nil {
		return nil, err
	}
	emb, err := c.Embed.Embed(e.Fact)
	if err != nil {
		return nil, err
	}
	if e.UUID == "" {
		e.UUID = newUUID()
	}
	e.GroupID = c.GroupID(e.GroupID)
	if e.CreatedAt == "" {
		e.CreatedAt = nowUTC()
	}
	if e.ValidAt, err = normalizeTime(e.ValidAt); err != nil {
		return nil, err
	}
	if e.InvalidAt, err = normalizeTime(e.InvalidAt); err != nil {
		return nil, err
	}
	attrs, err := mapToJSON(e.Attributes)
	if err != nil {
		return nil, err
	}
	_, err = c.graph.Query(`MATCH (s:Entity {uuid: $s}), (t:Entity {uuid: $t})
		MERGE (s)-[r:RELATES_TO {uuid: $uuid}]->(t)
		SET r.name = $name, r.group_id = $gid, r.fact = $fact,
			r.episodes = $episodes, r.created_at = $created_at,
			r.valid_at = $valid_at, r.invalid_at = $invalid_at, r.attributes = $attrs,
			r.fact_embedding = vecf32($emb)`,
		map[string]any{
			"s": e.SourceUUID, "t": e.TargetUUID, "uuid": e.UUID, "name": e.Name,
			"gid": e.GroupID, "fact": e.Fact, "episodes": e.Episodes,
			"created_at": e.CreatedAt, "valid_at": e.ValidAt, "invalid_at": e.InvalidAt,
			"attrs": attrs, "emb": emb,
		}, nil)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func edgeFromRel(r *falkordb.Edge) *Edge {
	p := r.Properties
	e := &Edge{
		UUID:       fmt.Sprint(p["uuid"]),
		Name:       fmt.Sprint(p["name"]),
		GroupID:    fmt.Sprint(p["group_id"]),
		Fact:       fmt.Sprint(p["fact"]),
		Episodes:   strSlice(p["episodes"]),
		CreatedAt:  fmt.Sprint(p["created_at"]),
		ValidAt:    fmt.Sprint(p["valid_at"]),
		InvalidAt:  fmt.Sprint(p["invalid_at"]),
		ExpiredAt:  fmt.Sprint(p["expired_at"]),
		Attributes: jsonToMap(fmt.Sprint(p["attributes"])),
	}
	if r.Source != nil {
		e.SourceUUID = fmt.Sprint(r.Source.Properties["uuid"])
	}
	if r.Destination != nil {
		e.TargetUUID = fmt.Sprint(r.Destination.Properties["uuid"])
	}
	return e
}

func (c *Client) GetEdge(uuid string) (*Edge, error) {
	res, err := c.graph.ROQuery(`MATCH (s)-[r:RELATES_TO {uuid: $uuid}]->(t) RETURN r`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return nil, err
	}
	if !res.Next() {
		return nil, fmt.Errorf("edge %s not found", uuid)
	}
	r, ok := res.Record().GetByIndex(0).(*falkordb.Edge)
	if !ok {
		return nil, fmt.Errorf("unexpected record type")
	}
	return edgeFromRel(r), nil
}

// InvalidateEdge 标记失效；invalidAt 为空用当前时间
func (c *Client) InvalidateEdge(uuid, invalidAt string) (*Edge, error) {
	var err error
	if invalidAt == "" {
		invalidAt = nowUTC()
	}
	if invalidAt, err = normalizeTime(invalidAt); err != nil {
		return nil, err
	}
	if _, err := c.GetEdge(uuid); err != nil {
		return nil, err
	}
	if _, err := c.graph.Query(`MATCH ()-[r:RELATES_TO {uuid: $uuid}]->() SET r.invalid_at = $t`,
		map[string]any{"uuid": uuid, "t": invalidAt}, nil); err != nil {
		return nil, err
	}
	return c.GetEdge(uuid)
}

func (c *Client) DeleteEdge(uuid string) error {
	res, err := c.graph.Query(`MATCH ()-[r:RELATES_TO {uuid: $uuid}]->() DELETE r RETURN count(r) AS cnt`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return err
	}
	if res.Next() {
		if cnt, _ := res.Record().GetByIndex(0).(int64); cnt == 0 {
			return fmt.Errorf("edge %s not found", uuid)
		}
	}
	return nil
}
```

- [ ] **Step 4: CLI cmd_edge.go**

`cmd/gmem-cli/cmd_edge.go`:

```go
package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

var (
	edgeUUID, edgeSrc, edgeTgt, edgeName, edgeFact, edgeValidAt, edgeInvalidAt, edgeEpUUID, edgeAttrs string
	edgeLenient bool
)

func init() {
	edgeUpsertCmd.Flags().StringVar(&edgeSrc, "source-uuid", "", "source entity uuid")
	edgeUpsertCmd.Flags().StringVar(&edgeTgt, "target-uuid", "", "target entity uuid")
	edgeUpsertCmd.Flags().StringVar(&edgeName, "name", "", "relation name (e.g. WORKS_ON)")
	edgeUpsertCmd.Flags().StringVar(&edgeFact, "fact", "", "natural language fact")
	edgeUpsertCmd.Flags().StringVar(&edgeValidAt, "valid-at", "", "RFC3339 time")
	edgeUpsertCmd.Flags().StringVar(&edgeEpUUID, "episode-uuid", "", "source episode uuid")
	edgeUpsertCmd.Flags().StringVar(&edgeAttrs, "attributes", "", "attributes JSON")
	edgeUpsertCmd.Flags().BoolVar(&edgeLenient, "lenient", false, "skip schema validation")
	_ = edgeUpsertCmd.MarkFlagRequired("source-uuid")
	_ = edgeUpsertCmd.MarkFlagRequired("target-uuid")
	_ = edgeUpsertCmd.MarkFlagRequired("name")
	_ = edgeUpsertCmd.MarkFlagRequired("fact")

	edgeInvalidateCmd.Flags().StringVar(&edgeUUID, "uuid", "", "edge uuid")
	edgeInvalidateCmd.Flags().StringVar(&edgeInvalidAt, "invalid-at", "", "RFC3339 time (default now)")
	_ = edgeInvalidateCmd.MarkFlagRequired("uuid")

	edgeDeleteCmd.Flags().StringVar(&edgeUUID, "uuid", "", "edge uuid")
	_ = edgeDeleteCmd.MarkFlagRequired("uuid")

	edgeCmd.AddCommand(edgeUpsertCmd, edgeInvalidateCmd, edgeDeleteCmd)
	rootCmd.AddCommand(edgeCmd)
}

var edgeCmd = &cobra.Command{Use: "edge", Short: "Edge operations"}

var edgeUpsertCmd = &cobra.Command{
	Use: "upsert",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		var attrs map[string]any
		if edgeAttrs != "" {
			if err := json.Unmarshal([]byte(edgeAttrs), &attrs); err != nil {
				fatal(err)
			}
		}
		var episodes []string
		if edgeEpUUID != "" {
			episodes = []string{edgeEpUUID}
		}
		e, err := c.UpsertEdge(&gmem.Edge{
			Name: edgeName, Fact: edgeFact, SourceUUID: edgeSrc, TargetUUID: edgeTgt,
			ValidAt: edgeValidAt, Episodes: episodes, Attributes: attrs,
		}, edgeLenient)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}

var edgeInvalidateCmd = &cobra.Command{
	Use: "invalidate",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		e, err := c.InvalidateEdge(edgeUUID, edgeInvalidAt)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}

var edgeDeleteCmd = &cobra.Command{
	Use: "delete",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		if err := c.DeleteEdge(edgeUUID); err != nil {
			fatal(err)
		}
		printJSON(map[string]string{"status": "ok"})
	},
}
```

注意：文件顶部需要 `import "github.com/coxlong/graph-memory/pkg/gmem"`。

- [ ] **Step 5: 运行测试**

Run: `go test ./pkg/gmem/ -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: edge crud and invalidate"
```

### Task 7: Search —— 向量 ∪ 全文召回 + RRF 排序

**Files:**
- Create: `pkg/gmem/search.go`
- Create: `pkg/gmem/search_test.go`
- Create: `cmd/gmem-cli/cmd_search.go`

**Interfaces:**
- Consumes: `entityFromNode`、`edgeFromRel`、`episodeFromNode`、`Embedder.Embed`（Task 4/5/6）
- Produces:
  - `gmem.SearchOpts{GroupID, AsOf string; Limit int; IncludeInvalid bool}`
  - `gmem.SearchResult{Entities []EntityWithScore, Edges []EdgeWithScore, Episodes []EpisodeWithScore}`
  - `EntityWithScore{Entity; Score float64}` / `EdgeWithScore{Edge; Score float64}` / `EpisodeWithScore{Episode; Score float64}`
  - `(*Client).Search(query string, opts SearchOpts) (*SearchResult, error)`
  - `(*Client).SearchEntities(query string, limit int) ([]EntityWithScore, error)`
  - `(*Client).SearchEdges(query string, limit int, includeInvalid bool) ([]EdgeWithScore, error)`
  - `rrfFuse(rankLists [][]string, scores map[string]float64, k float64) []string` — 纯函数，可单测
  - CLI：`gmem-cli search --query [--limit] [--as-of] [--include-invalid] [--group-id]`、`entity search --query`、`edge search --query [--include-invalid]`

**实现要点（与 graphiti FalkorDB 做法对齐）：**
- 向量相似度：`(2 - vec.cosineDistance(n.name_embedding, vecf32($vec)))/2 AS score`，`WHERE score > 0`（或 min_score 0.3）
- 全文：`CALL db.idx.fulltext.queryNodes('Entity', $query)` / `queryRelationships('RELATES_TO', $query)`
- 全文 query 转义：把用户 query 中的特殊字符（`@{}()"|~*:-`）替换为空格，避免 RedisSearch 语法错误
- RRF：`score(d) = Σ 1/(k + rank_i(d))`，k=60，融合向量路和全文路
- `--as-of T` 过滤：`r.valid_at <= T AND (r.invalid_at = '' OR r.invalid_at > T)`
- 默认（无 as-of、无 include-invalid）过滤：`r.invalid_at = ''`

- [ ] **Step 1: 写失败测试**

`pkg/gmem/search_test.go`:

```go
package gmem

import "testing"

func TestRRFFuse(t *testing.T) {
	// 两路召回：向量路 [a b c]，全文路 [b a d]
	ranked := rrfFuse([][]string{{"a", "b", "c"}, {"b", "a", "d"}}, 60)
	if len(ranked) != 4 {
		t.Fatalf("want 4 docs, got %d", len(ranked))
	}
	// a: 1/61+1/62 ≈ 0.0325; b: 1/62+1/61 ≈ 0.0325 —— 并列前二；c、d 在后
	top2 := map[string]bool{ranked[0]: true, ranked[1]: true}
	if !top2["a"] || !top2["b"] {
		t.Fatalf("top2: %v", ranked)
	}
	if ranked[2] == ranked[3] || (ranked[2] != "c" && ranked[2] != "d") {
		t.Fatalf("tail: %v", ranked)
	}
}

func TestSearchEntities(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	if _, _, err := c.UpsertEntity(&Entity{Name: "Alice Wonderland"}, false); err != nil {
		t.Fatal(err)
	}
	res, err := c.SearchEntities("Alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 || res[0].Name != "Alice Wonderland" {
		t.Fatalf("search: %+v", res)
	}
}

func TestSearchEdgesTemporalFilter(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "TAlice"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "TeamA"}, false)
	e, _ := c.UpsertEdge(&Edge{
		Name: "MEMBER_OF", Fact: "TAlice member of TeamA",
		SourceUUID: a.UUID, TargetUUID: b.UUID,
		ValidAt: "2026-01-01T00:00:00Z",
	}, false)

	// 默认（有效边）：应找到
	res, err := c.SearchEdges("member of TeamA", 10, false)
	if err != nil || len(res) == 0 {
		t.Fatalf("valid edge search: %v %v", res, err)
	}
	// 失效后默认查不到
	if _, err := c.InvalidateEdge(e.UUID, "2026-06-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	res, _ = c.SearchEdges("member of TeamA", 10, false)
	if len(res) != 0 {
		t.Fatalf("invalidated edge should be hidden: %v", res)
	}
	// includeInvalid 能查到
	res, _ = c.SearchEdges("member of TeamA", 10, true)
	if len(res) != 1 {
		t.Fatalf("includeInvalid: %v", res)
	}
}

func TestSearchAsOf(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "AsOfAlice"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "TeamB"}, false)
	e, _ := c.UpsertEdge(&Edge{
		Name: "MEMBER_OF", Fact: "AsOfAlice member of TeamB",
		SourceUUID: a.UUID, TargetUUID: b.UUID,
		ValidAt: "2026-01-01T00:00:00Z",
	}, false)
	if _, err := c.InvalidateEdge(e.UUID, "2026-06-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// 3月时点：边仍有效
	res, err := c.Search("member of TeamB", SearchOpts{AsOf: "2026-03-01T00:00:00Z", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 {
		t.Fatalf("as-of should see the edge: %+v", res.Edges)
	}
	// 现在：默认看不到
	res, _ = c.Search("member of TeamB", SearchOpts{Limit: 10})
	if len(res.Edges) != 0 {
		t.Fatalf("now should not see invalidated edge: %+v", res.Edges)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run 'TestRRF|TestSearch' -v`
Expected: FAIL — `rrfFuse`/`Search` undefined（编译错误）

- [ ] **Step 3: 实现 search.go**

`pkg/gmem/search.go`:

```go
package gmem

import (
	"fmt"
	"sort"
	"strings"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

type SearchOpts struct {
	GroupID        string
	AsOf           string
	Limit          int
	IncludeInvalid bool
}

type EntityWithScore struct {
	Entity
	Score float64 `json:"score"`
}

type EdgeWithScore struct {
	Edge
	Score float64 `json:"score"`
}

type EpisodeWithScore struct {
	Episode
	Score float64 `json:"score"`
}

type SearchResult struct {
	Entities []EntityWithScore  `json:"entities"`
	Edges    []EdgeWithScore    `json:"edges"`
	Episodes []EpisodeWithScore `json:"episodes"`
}

// escapeFTQuery 清理 RedisSearch 特殊字符
func escapeFTQuery(q string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`@{}()"|~*:-><[]`, r) {
			return ' '
		}
		return r
	}, q)
}

// rrfFuse 融合多路召回的排名列表，返回按 RRF 分数降序的文档 id 列表
func rrfFuse(rankLists [][]string, k float64) []string {
	scores := map[string]float64{}
	for _, list := range rankLists {
		for rank, id := range list {
			scores[id] += 1.0 / (k + float64(rank+1))
		}
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] == scores[ids[j]] {
			return ids[i] < ids[j]
		}
		return scores[ids[i]] > scores[ids[j]]
	})
	return ids
}

// SearchEntities 向量 ∪ 全文检索实体
func (c *Client) SearchEntities(query string, limit int) ([]EntityWithScore, error) {
	vec, err := c.Embed.Embed(query)
	if err != nil {
		return nil, err
	}
	byID := map[string]EntityWithScore{}
	ranks := [][]string{}

	// 向量路
	res, err := c.graph.ROQuery(`MATCH (n:Entity)
		WHERE n.name_embedding IS NOT NULL
		WITH n, (2 - vec.cosineDistance(n.name_embedding, vecf32($vec)))/2 AS score
		WHERE score > 0.3
		RETURN n, score ORDER BY score DESC LIMIT $limit`,
		map[string]any{"vec": vec, "limit": limit}, nil)
	if err != nil {
		return nil, err
	}
	vr := []string{}
	for res.Next() {
		rec := res.Record()
		if n, ok := rec.GetByIndex(0).(*falkordb.Node); ok {
			e := entityFromNode(n)
			s, _ := rec.GetByIndex(1).(float64)
			byID[e.UUID] = EntityWithScore{Entity: *e, Score: s}
			vr = append(vr, e.UUID)
		}
	}
	ranks = append(ranks, vr)

	// 全文路
	res, err = c.graph.ROQuery(`CALL db.idx.fulltext.queryNodes('Entity', $q) YIELD node, score
		RETURN node, score LIMIT $limit`,
		map[string]any{"q": escapeFTQuery(query), "limit": limit}, nil)
	if err != nil {
		return nil, err
	}
	fr := []string{}
	for res.Next() {
		rec := res.Record()
		if n, ok := rec.GetByIndex(0).(*falkordb.Node); ok {
			e := entityFromNode(n)
			if _, seen := byID[e.UUID]; !seen {
				s, _ := rec.GetByIndex(1).(float64)
				byID[e.UUID] = EntityWithScore{Entity: *e, Score: s}
			}
			fr = append(fr, e.UUID)
		}
	}
	ranks = append(ranks, fr)

	order := rrfFuse(ranks, 60)
	out := []EntityWithScore{}
	for i, id := range order {
		if i >= limit {
			break
		}
		out = append(out, byID[id])
	}
	return out, nil
}

// edgeTemporalFilter 返回时序过滤 WHERE 片段和参数
func edgeTemporalFilter(asOf string, includeInvalid bool) (string, map[string]any) {
	if asOf != "" {
		return "r.valid_at <= $asOf AND (r.invalid_at = '' OR r.invalid_at > $asOf)",
			map[string]any{"asOf": asOf}
	}
	if !includeInvalid {
		return "r.invalid_at = ''", map[string]any{}
	}
	return "true", map[string]any{}
}

// SearchEdges 向量 ∪ 全文检索 RELATES_TO 边，带时序过滤
func (c *Client) SearchEdges(query string, limit int, includeInvalid bool) ([]EdgeWithScore, error) {
	return c.searchEdgesFiltered(query, limit, "", includeInvalid)
}

func (c *Client) searchEdgesFiltered(query string, limit int, asOf string, includeInvalid bool) ([]EdgeWithScore, error) {
	vec, err := c.Embed.Embed(query)
	if err != nil {
		return nil, err
	}
	filter, fparams := edgeTemporalFilter(asOf, includeInvalid)
	byID := map[string]EdgeWithScore{}
	ranks := [][]string{}

	// 向量路
	params := map[string]any{"vec": vec, "limit": limit}
	for k, v := range fparams {
		params[k] = v
	}
	res, err := c.graph.ROQuery(`MATCH ()-[r:RELATES_TO]->()
		WHERE r.fact_embedding IS NOT NULL AND `+filter+`
		WITH r, (2 - vec.cosineDistance(r.fact_embedding, vecf32($vec)))/2 AS score
		WHERE score > 0.3
		RETURN r, score ORDER BY score DESC LIMIT $limit`, params, nil)
	if err != nil {
		return nil, err
	}
	vr := []string{}
	for res.Next() {
		rec := res.Record()
		if r, ok := rec.GetByIndex(0).(*falkordb.Edge); ok {
			e := edgeFromRel(r)
			s, _ := rec.GetByIndex(1).(float64)
			byID[e.UUID] = EdgeWithScore{Edge: *e, Score: s}
			vr = append(vr, e.UUID)
		}
	}
	ranks = append(ranks, vr)

	// 全文路（时序过滤在召回后应用）
	res, err = c.graph.ROQuery(`CALL db.idx.fulltext.queryRelationships('RELATES_TO', $q) YIELD relationship, score
		WITH relationship AS r, score WHERE `+filter+`
		RETURN r, score LIMIT $limit`,
		map[string]any{"q": escapeFTQuery(query), "limit": limit, "asOf": asOf}, nil)
	if err != nil {
		return nil, err
	}
	fr := []string{}
	for res.Next() {
		rec := res.Record()
		if r, ok := rec.GetByIndex(0).(*falkordb.Edge); ok {
			e := edgeFromRel(r)
			if _, seen := byID[e.UUID]; !seen {
				s, _ := rec.GetByIndex(1).(float64)
				byID[e.UUID] = EdgeWithScore{Edge: *e, Score: s}
			}
			fr = append(fr, e.UUID)
		}
	}
	ranks = append(ranks, fr)

	order := rrfFuse(ranks, 60)
	out := []EdgeWithScore{}
	for i, id := range order {
		if i >= limit {
			break
		}
		out = append(out, byID[id])
	}
	return out, nil
}

// searchEpisodes 全文检索 episode（episode 无向量，仅全文路）
func (c *Client) searchEpisodes(query string, limit int) ([]EpisodeWithScore, error) {
	res, err := c.graph.ROQuery(`CALL db.idx.fulltext.queryNodes('Episodic', $q) YIELD node, score
		RETURN node, score LIMIT $limit`,
		map[string]any{"q": escapeFTQuery(query), "limit": limit}, nil)
	if err != nil {
		return nil, err
	}
	out := []EpisodeWithScore{}
	for res.Next() {
		rec := res.Record()
		if n, ok := rec.GetByIndex(0).(*falkordb.Node); ok {
			s, _ := rec.GetByIndex(1).(float64)
			out = append(out, EpisodeWithScore{Episode: *episodeFromNode(n), Score: s})
		}
	}
	return out, nil
}

// Search 混合检索：entities + edges + episodes
func (c *Client) Search(query string, opts SearchOpts) (*SearchResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.AsOf != "" {
		var err error
		if opts.AsOf, err = normalizeTime(opts.AsOf); err != nil {
			return nil, err
		}
	}
	entities, err := c.SearchEntities(query, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("entities: %w", err)
	}
	edges, err := c.searchEdgesFiltered(query, opts.Limit, opts.AsOf, opts.IncludeInvalid)
	if err != nil {
		return nil, fmt.Errorf("edges: %w", err)
	}
	episodes, err := c.searchEpisodes(query, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("episodes: %w", err)
	}
	return &SearchResult{Entities: entities, Edges: edges, Episodes: episodes}, nil
}
```

- [ ] **Step 4: CLI cmd_search.go + entity search + edge search 子命令**

`cmd/gmem-cli/cmd_search.go`:

```go
package main

import (
	"github.com/spf13/cobra"
)

var (
	searchQuery, searchAsOf, searchGroup string
	searchLimit                          int
	searchIncludeInvalid                 bool
)

func init() {
	searchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results per category")
	searchCmd.Flags().StringVar(&searchAsOf, "as-of", "", "RFC3339 point-in-time filter")
	searchCmd.Flags().StringVar(&searchGroup, "group-id", "", "group id")
	searchCmd.Flags().BoolVar(&searchIncludeInvalid, "include-invalid", false, "include invalidated facts")
	_ = searchCmd.MarkFlagRequired("query")

	entitySearchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	entitySearchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results")
	_ = entitySearchCmd.MarkFlagRequired("query")

	edgeSearchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	edgeSearchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results")
	edgeSearchCmd.Flags().BoolVar(&searchIncludeInvalid, "include-invalid", false, "include invalidated facts")
	_ = edgeSearchCmd.MarkFlagRequired("query")

	entityCmd.AddCommand(entitySearchCmd)
	edgeCmd.AddCommand(edgeSearchCmd)
	rootCmd.AddCommand(searchCmd)
}

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Hybrid search across entities, facts and episodes",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.Search(searchQuery, gmem.SearchOpts{
			GroupID: searchGroup, AsOf: searchAsOf, Limit: searchLimit,
			IncludeInvalid: searchIncludeInvalid,
		})
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}

var entitySearchCmd = &cobra.Command{
	Use: "search",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.SearchEntities(searchQuery, searchLimit)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}

var edgeSearchCmd = &cobra.Command{
	Use: "search",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.SearchEdges(searchQuery, searchLimit, searchIncludeInvalid)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}
```

注意 import `github.com/coxlong/graph-memory/pkg/gmem`。

- [ ] **Step 5: 运行测试**

Run: `go test ./pkg/gmem/ -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: hybrid search with RRF fusion and temporal filtering"
```

### Task 8: `add` 组合命令

**Files:**
- Create: `pkg/gmem/add.go`
- Create: `pkg/gmem/add_test.go`
- Create: `cmd/gmem-cli/cmd_add.go`

**Interfaces:**
- Consumes: `CreateEpisode`、`UpsertEntity`、`UpsertEdge`（Task 4/5/6）
- Produces:
  - `gmem.AddEntityInput{Name string; Labels []string; Summary string; Attributes map[string]any}`
  - `gmem.AddEdgeInput{Source, Target, Name, Fact, ValidAt string}`（Source/Target 为实体 **name**，CLI 解析为 uuid）
  - `gmem.AddInput{Episode *Episode; Entities []AddEntityInput; Edges []AddEdgeInput; Lenient bool}`
  - `gmem.AddResult{EpisodeUUID string; Entities map[string]string; EdgeUUIDs []string}`（Entities: name → uuid）
  - `(*Client).Add(in *AddInput) (*AddResult, error)` — 建 Episodic → upsert entities → MENTIONS → RELATES_TO（追加 episode uuid）→ 回写 episode.entity_edges
  - CLI：`gmem-cli add --content --source [--entities JSON] [--edges JSON] [--metadata JSON] [--valid-at] [--group-id] [--lenient]`

**注意：** 为保持实现简单，`Add` 不是事务性的（FalkorDB 不支持多语句事务回滚）；顺序执行，中途失败返回错误和已完成部分。skill 文档会教 agent 失败时重试（upsert 幂等，重试安全）。

- [ ] **Step 1: 写失败测试**

`pkg/gmem/add_test.go`:

```go
package gmem

import "testing"

func TestAddFullFlow(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	res, err := c.Add(&AddInput{
		Episode: &Episode{Content: "Alice joined TeamB", Source: "message"},
		Entities: []AddEntityInput{
			{Name: "AddAlice", Labels: []string{"Person"}, Summary: "engineer"},
			{Name: "TeamB"},
		},
		Edges: []AddEdgeInput{
			{Source: "AddAlice", Target: "TeamB", Name: "MEMBER_OF", Fact: "AddAlice joined TeamB", ValidAt: "2026-07-19T00:00:00Z"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.EpisodeUUID == "" || len(res.Entities) != 2 || len(res.EdgeUUIDs) != 1 {
		t.Fatalf("bad result: %+v", res)
	}
	// MENTIONS 边已建
	qr, _ := c.graph.ROQuery(`MATCH (ep:Episodic {uuid:$u})-[:MENTIONS]->(e) RETURN count(e)`,
		map[string]any{"u": res.EpisodeUUID}, nil)
	qr.Next()
	if cnt, _ := qr.Record().GetByIndex(0).(int64); cnt != 2 {
		t.Fatalf("mentions: %d", cnt)
	}
	// RELATES_TO 的 episodes 已回写
	edge, _ := c.GetEdge(res.EdgeUUIDs[0])
	if len(edge.Episodes) != 1 || edge.Episodes[0] != res.EpisodeUUID {
		t.Fatalf("edge episodes: %v", edge.Episodes)
	}
	// episode.entity_edges 已回写
	ep, _ := c.GetEpisode(res.EpisodeUUID)
	if len(ep.EntityEdges) != 1 {
		t.Fatalf("episode entity_edges: %v", ep.EntityEdges)
	}
}

func TestAddIdempotentRetry(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	in := &AddInput{
		Episode:  &Episode{Content: "retry test", Source: "text"},
		Entities: []AddEntityInput{{Name: "RetryAlice"}},
	}
	r1, err := c.Add(in)
	if err != nil {
		t.Fatal(err)
	}
	// 第二次 add 同名实体应 dedup（不报错、uuid 相同）
	r2, err := c.Add(in)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Entities["RetryAlice"] != r2.Entities["RetryAlice"] {
		t.Fatalf("retry not idempotent: %v vs %v", r1.Entities, r2.Entities)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run TestAdd -v`
Expected: FAIL — `AddInput` undefined（编译错误）

- [ ] **Step 3: 实现 add.go**

`pkg/gmem/add.go`:

```go
package gmem

import "fmt"

type AddEntityInput struct {
	Name       string         `json:"name"`
	Labels     []string       `json:"labels,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type AddEdgeInput struct {
	Source  string `json:"source"` // entity name
	Target  string `json:"target"` // entity name
	Name    string `json:"name"`
	Fact    string `json:"fact"`
	ValidAt string `json:"valid_at,omitempty"`
}

type AddInput struct {
	Episode  *Episode         `json:"episode"`
	Entities []AddEntityInput `json:"entities,omitempty"`
	Edges    []AddEdgeInput   `json:"edges,omitempty"`
	GroupID  string           `json:"group_id,omitempty"`
	Lenient  bool             `json:"-"`
}

type AddResult struct {
	EpisodeUUID string            `json:"episode_uuid"`
	Entities    map[string]string `json:"entities"` // name -> uuid
	EdgeUUIDs   []string          `json:"edge_uuids"`
}

// Add 一次写入完整记忆：episode + entities + MENTIONS + RELATES_TO
// 非事务性；upsert 幂等，失败重试安全
func (c *Client) Add(in *AddInput) (*AddResult, error) {
	gid := c.GroupID(in.GroupID)
	in.Episode.GroupID = gid
	ep, err := c.CreateEpisode(in.Episode)
	if err != nil {
		return nil, fmt.Errorf("episode: %w", err)
	}
	result := &AddResult{EpisodeUUID: ep.UUID, Entities: map[string]string{}}

	// upsert entities + MENTIONS
	for _, in2 := range in.Entities {
		e := &Entity{
			Name: in2.Name, GroupID: gid, Labels: in2.Labels,
			Summary: in2.Summary, Attributes: in2.Attributes,
		}
		saved, _, err := c.UpsertEntity(e, in.Lenient)
		if err != nil {
			return result, fmt.Errorf("entity %q: %w", in2.Name, err)
		}
		result.Entities[in2.Name] = saved.UUID
		if _, err := c.graph.Query(`MATCH (ep:Episodic {uuid: $ep}), (en:Entity {uuid: $en})
			MERGE (ep)-[:MENTIONS {uuid: $uuid, group_id: $gid, created_at: $ts}]->(en)`,
			map[string]any{"ep": ep.UUID, "en": saved.UUID, "uuid": newUUID(), "gid": gid, "ts": nowUTC()}, nil); err != nil {
			return result, fmt.Errorf("mentions %q: %w", in2.Name, err)
		}
	}

	// RELATES_TO edges（source/target 按 name 解析）
	for _, ei := range in.Edges {
		srcUUID, ok := result.Entities[ei.Source]
		if !ok {
			// 允许引用图中已存在的实体
			found, _, err := c.UpsertEntity(&Entity{Name: ei.Source, GroupID: gid}, in.Lenient)
			if err != nil {
				return result, fmt.Errorf("edge source %q: %w", ei.Source, err)
			}
			srcUUID = found.UUID
			result.Entities[ei.Source] = srcUUID
		}
		tgtUUID, ok := result.Entities[ei.Target]
		if !ok {
			found, _, err := c.UpsertEntity(&Entity{Name: ei.Target, GroupID: gid}, in.Lenient)
			if err != nil {
				return result, fmt.Errorf("edge target %q: %w", ei.Target, err)
			}
			tgtUUID = found.UUID
			result.Entities[ei.Target] = tgtUUID
		}
		edge, err := c.UpsertEdge(&Edge{
			Name: ei.Name, Fact: ei.Fact, GroupID: gid,
			SourceUUID: srcUUID, TargetUUID: tgtUUID,
			ValidAt: ei.ValidAt, Episodes: []string{ep.UUID},
		}, in.Lenient)
		if err != nil {
			return result, fmt.Errorf("edge %q: %w", ei.Name, err)
		}
		result.EdgeUUIDs = append(result.EdgeUUIDs, edge.UUID)
	}

	// 回写 episode.entity_edges
	if len(result.EdgeUUIDs) > 0 {
		if _, err := c.graph.Query(`MATCH (ep:Episodic {uuid: $uuid}) SET ep.entity_edges = $ee`,
			map[string]any{"uuid": ep.UUID, "ee": result.EdgeUUIDs}, nil); err != nil {
			return result, fmt.Errorf("writeback entity_edges: %w", err)
		}
	}
	return result, nil
}
```

- [ ] **Step 4: CLI cmd_add.go**

`cmd/gmem-cli/cmd_add.go`:

```go
package main

import (
	"encoding/json"

	"github.com/coxlong/graph-memory/pkg/gmem"
	"github.com/spf13/cobra"
)

var (
	addContent, addSource, addEntities, addEdges, addMetadata, addValidAt, addGroup string
	addLenient                                                                      bool
)

func init() {
	addCmd.Flags().StringVar(&addContent, "content", "", "episode raw content")
	addCmd.Flags().StringVar(&addSource, "source", "message", "message|text|json")
	addCmd.Flags().StringVar(&addEntities, "entities", "", "entities JSON array")
	addCmd.Flags().StringVar(&addEdges, "edges", "", "edges JSON array")
	addCmd.Flags().StringVar(&addMetadata, "metadata", "", "episode metadata JSON object")
	addCmd.Flags().StringVar(&addValidAt, "valid-at", "", "RFC3339 time of the episode")
	addCmd.Flags().StringVar(&addGroup, "group-id", "", "group id")
	addCmd.Flags().BoolVar(&addLenient, "lenient", false, "skip schema validation")
	_ = addCmd.MarkFlagRequired("content")
	rootCmd.AddCommand(addCmd)
}

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an episode with extracted entities and edges in one call",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		in := &gmem.AddInput{
			Episode: &gmem.Episode{Content: addContent, Source: addSource, ValidAt: addValidAt},
			GroupID: addGroup,
			Lenient: addLenient,
		}
		if addEntities != "" {
			if err := json.Unmarshal([]byte(addEntities), &in.Entities); err != nil {
				fatal(err)
			}
		}
		if addEdges != "" {
			if err := json.Unmarshal([]byte(addEdges), &in.Edges); err != nil {
				fatal(err)
			}
		}
		if addMetadata != "" {
			if err := json.Unmarshal([]byte(addMetadata), &in.Episode.Metadata); err != nil {
				fatal(err)
			}
		}
		res, err := c.Add(in)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}
```

- [ ] **Step 5: 运行测试 + CLI 冒烟**

Run: `go test ./pkg/gmem/ -v`
Expected: 全部 PASS

冒烟（需要本地 FalkorDB + 真实 embedding API 或 mock）：

```bash
go build -o gmem-cli ./cmd/gmem-cli
./gmem-cli init
./gmem-cli add --content "Alice joined TeamB" --source message \
  --entities '[{"name":"Alice","labels":["Person"]},{"name":"TeamB"}]' \
  --edges '[{"source":"Alice","target":"TeamB","name":"MEMBER_OF","fact":"Alice joined TeamB"}]'
./gmem-cli search --query "Alice team"
```

Expected: add 返回 episode_uuid/entities/edge_uuids JSON；search 返回三类结果。

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: composite add command"
```

---

### Task 9: `add-triplet` 组合命令

**Files:**
- Modify: `pkg/gmem/add.go`（追加）
- Modify: `pkg/gmem/add_test.go`（追加）
- Modify: `cmd/gmem-cli/cmd_add.go`（追加 add-triplet 子命令）

**Interfaces:**
- Consumes: `UpsertEntity`、`UpsertEdge`（Task 5/6）
- Produces:
  - `(*Client).AddTriplet(sourceName, edgeName, fact, targetName, groupID, validAt string, lenient bool) (*TripletResult, error)`
  - `TripletResult{SourceUUID, TargetUUID, EdgeUUID string}`
  - CLI：`gmem-cli add-triplet --source --name --fact --target [--group-id] [--valid-at] [--lenient]`

- [ ] **Step 1: 写失败测试（追加到 add_test.go）**

```go
func TestAddTriplet(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	r, err := c.AddTriplet("TriAlice", "WORKS_ON", "TriAlice works on gmem", "gmem", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceUUID == "" || r.TargetUUID == "" || r.EdgeUUID == "" {
		t.Fatalf("bad result: %+v", r)
	}
	// 重复调用同一三元组：实体 dedup，边数量不爆增（不同 fact 是不同边，同 fact 同 name 会 MERGE by uuid——
	// add-triplet 每次新建边，所以验证实体 dedup 即可）
	r2, err := c.AddTriplet("TriAlice", "WORKS_ON", "TriAlice works on gmem", "gmem", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceUUID != r2.SourceUUID || r.TargetUUID != r2.TargetUUID {
		t.Fatalf("entities should dedup: %+v vs %+v", r, r2)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run TestAddTriplet -v`
Expected: FAIL — `AddTriplet` undefined

- [ ] **Step 3: 实现（追加到 add.go）**

```go
type TripletResult struct {
	SourceUUID string `json:"source_uuid"`
	TargetUUID string `json:"target_uuid"`
	EdgeUUID   string `json:"edge_uuid"`
}

// AddTriplet 直接写一条事实三元组（对齐 graphiti add_triplet）：实体按 name 去重，边新建
func (c *Client) AddTriplet(sourceName, edgeName, fact, targetName, groupID, validAt string, lenient bool) (*TripletResult, error) {
	gid := c.GroupID(groupID)
	src, _, err := c.UpsertEntity(&Entity{Name: sourceName, GroupID: gid}, lenient)
	if err != nil {
		return nil, fmt.Errorf("source entity: %w", err)
	}
	tgt, _, err := c.UpsertEntity(&Entity{Name: targetName, GroupID: gid}, lenient)
	if err != nil {
		return nil, fmt.Errorf("target entity: %w", err)
	}
	edge, err := c.UpsertEdge(&Edge{
		Name: edgeName, Fact: fact, GroupID: gid,
		SourceUUID: src.UUID, TargetUUID: tgt.UUID, ValidAt: validAt,
	}, lenient)
	if err != nil {
		return nil, err
	}
	return &TripletResult{SourceUUID: src.UUID, TargetUUID: tgt.UUID, EdgeUUID: edge.UUID}, nil
}
```

- [ ] **Step 4: CLI（追加到 cmd_add.go）**

```go
var (
	triSource, triName, triFact, triTarget, triGroup, triValidAt string
	triLenient                                                   bool
)

func init() {
	addTripletCmd.Flags().StringVar(&triSource, "source", "", "source entity name")
	addTripletCmd.Flags().StringVar(&triName, "name", "", "relation name")
	addTripletCmd.Flags().StringVar(&triFact, "fact", "", "natural language fact")
	addTripletCmd.Flags().StringVar(&triTarget, "target", "", "target entity name")
	addTripletCmd.Flags().StringVar(&triGroup, "group-id", "", "group id")
	addTripletCmd.Flags().StringVar(&triValidAt, "valid-at", "", "RFC3339 time")
	addTripletCmd.Flags().BoolVar(&triLenient, "lenient", false, "skip schema validation")
	_ = addTripletCmd.MarkFlagRequired("source")
	_ = addTripletCmd.MarkFlagRequired("name")
	_ = addTripletCmd.MarkFlagRequired("fact")
	_ = addTripletCmd.MarkFlagRequired("target")
	rootCmd.AddCommand(addTripletCmd)
}

var addTripletCmd = &cobra.Command{
	Use:   "add-triplet",
	Short: "Add a single fact triplet (entities deduped by name)",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.AddTriplet(triSource, triName, triFact, triTarget, triGroup, triValidAt, triLenient)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./pkg/gmem/ -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: add-triplet command"
```

### Task 10: Saga 水位线

**Files:**
- Create: `pkg/gmem/saga.go`
- Create: `pkg/gmem/saga_test.go`
- Create: `cmd/gmem-cli/cmd_saga.go`

**Interfaces:**
- Produces:
  - `gmem.Saga{UUID, Name, GroupID, Summary, FirstEpisodeUUID, LastEpisodeUUID, LastSummarizedAt, LastSummarizedEpisodeValidAt, CreatedAt string}`
  - `(*Client).CreateSaga(s *Saga) (*Saga, error)`
  - `(*Client).GetSaga(uuid string) (*Saga, error)`
  - `(*Client).UpdateSaga(s *Saga) (*Saga, error)` — 非空字段覆盖
  - CLI：`saga create --name`、`saga get --uuid`、`saga update --uuid [--summary] [--last-episode-uuid] [--last-summarized-at] [--last-summarized-episode-valid-at]`

- [ ] **Step 1: 写失败测试**

`pkg/gmem/saga_test.go`:

```go
package gmem

import "testing"

func TestSagaCRUD(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	s, err := c.CreateSaga(&Saga{Name: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if s.UUID == "" || s.GroupID != "default" {
		t.Fatalf("bad saga: %+v", s)
	}
	got, err := c.GetSaga(s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "session-1" {
		t.Fatalf("get: %+v", got)
	}
	updated, err := c.UpdateSaga(&Saga{
		UUID: s.UUID, Summary: "week 1 summary",
		LastEpisodeUUID: "ep-9", LastSummarizedAt: "2026-07-19T10:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Summary != "week 1 summary" || updated.LastEpisodeUUID != "ep-9" || updated.Name != "session-1" {
		t.Fatalf("update: %+v", updated)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run TestSaga -v`
Expected: FAIL — `Saga` undefined（编译错误）

- [ ] **Step 3: 实现 saga.go**

`pkg/gmem/saga.go`:

```go
package gmem

import (
	"fmt"
	"strings"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

type Saga struct {
	UUID                        string `json:"uuid"`
	Name                        string `json:"name"`
	GroupID                     string `json:"group_id"`
	Summary                     string `json:"summary,omitempty"`
	FirstEpisodeUUID            string `json:"first_episode_uuid,omitempty"`
	LastEpisodeUUID             string `json:"last_episode_uuid,omitempty"`
	LastSummarizedAt            string `json:"last_summarized_at,omitempty"`
	LastSummarizedEpisodeValidAt string `json:"last_summarized_episode_valid_at,omitempty"`
	CreatedAt                   string `json:"created_at"`
}

func (c *Client) CreateSaga(s *Saga) (*Saga, error) {
	if s.UUID == "" {
		s.UUID = newUUID()
	}
	s.GroupID = c.GroupID(s.GroupID)
	if s.CreatedAt == "" {
		s.CreatedAt = nowUTC()
	}
	_, err := c.graph.Query(`CREATE (n:Saga {
		uuid: $uuid, name: $name, group_id: $gid, summary: $summary,
		first_episode_uuid: $fe, last_episode_uuid: $le,
		last_summarized_at: $lsa, last_summarized_episode_valid_at: $lsev,
		created_at: $created_at
	})`, map[string]any{
		"uuid": s.UUID, "name": s.Name, "gid": s.GroupID, "summary": s.Summary,
		"fe": s.FirstEpisodeUUID, "le": s.LastEpisodeUUID,
		"lsa": s.LastSummarizedAt, "lsev": s.LastSummarizedEpisodeValidAt,
		"created_at": s.CreatedAt,
	}, nil)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func sagaFromNode(n *falkordb.Node) *Saga {
	p := n.Properties
	return &Saga{
		UUID:                         fmt.Sprint(p["uuid"]),
		Name:                         fmt.Sprint(p["name"]),
		GroupID:                      fmt.Sprint(p["group_id"]),
		Summary:                      fmt.Sprint(p["summary"]),
		FirstEpisodeUUID:             fmt.Sprint(p["first_episode_uuid"]),
		LastEpisodeUUID:              fmt.Sprint(p["last_episode_uuid"]),
		LastSummarizedAt:             fmt.Sprint(p["last_summarized_at"]),
		LastSummarizedEpisodeValidAt: fmt.Sprint(p["last_summarized_episode_valid_at"]),
		CreatedAt:                    fmt.Sprint(p["created_at"]),
	}
}

func (c *Client) GetSaga(uuid string) (*Saga, error) {
	res, err := c.graph.ROQuery(`MATCH (n:Saga {uuid: $uuid}) RETURN n`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return nil, err
	}
	if !res.Next() {
		return nil, fmt.Errorf("saga %s not found", uuid)
	}
	n, ok := res.Record().GetByIndex(0).(*falkordb.Node)
	if !ok {
		return nil, fmt.Errorf("unexpected record type")
	}
	return sagaFromNode(n), nil
}

// UpdateSaga 非空字段覆盖
func (c *Client) UpdateSaga(s *Saga) (*Saga, error) {
	cur, err := c.GetSaga(s.UUID)
	if err != nil {
		return nil, err
	}
	sets := []string{}
	params := map[string]any{"uuid": s.UUID}
	fields := []struct {
		key, val, cur string
	}{
		{"summary", s.Summary, cur.Summary},
		{"first_episode_uuid", s.FirstEpisodeUUID, cur.FirstEpisodeUUID},
		{"last_episode_uuid", s.LastEpisodeUUID, cur.LastEpisodeUUID},
		{"last_summarized_at", s.LastSummarizedAt, cur.LastSummarizedAt},
		{"last_summarized_episode_valid_at", s.LastSummarizedEpisodeValidAt, cur.LastSummarizedEpisodeValidAt},
	}
	for _, f := range fields {
		if f.val != "" && f.val != f.cur {
			sets = append(sets, "n."+f.key+" = $"+f.key)
			params[f.key] = f.val
		}
	}
	if len(sets) > 0 {
		if _, err := c.graph.Query(`MATCH (n:Saga {uuid: $uuid}) SET `+strings.Join(sets, ", "),
			params, nil); err != nil {
			return nil, err
		}
	}
	return c.GetSaga(s.UUID)
}
```

- [ ] **Step 4: CLI cmd_saga.go**

`cmd/gmem-cli/cmd_saga.go`:

```go
package main

import (
	"github.com/coxlong/graph-memory/pkg/gmem"
	"github.com/spf13/cobra"
)

var (
	sagaUUID, sagaName, sagaGroup, sagaSummary, sagaLastEp, sagaLSA, sagaLSEV string
)

func init() {
	sagaCreateCmd.Flags().StringVar(&sagaName, "name", "", "saga name")
	sagaCreateCmd.Flags().StringVar(&sagaGroup, "group-id", "", "group id")
	_ = sagaCreateCmd.MarkFlagRequired("name")

	sagaGetCmd.Flags().StringVar(&sagaUUID, "uuid", "", "saga uuid")
	_ = sagaGetCmd.MarkFlagRequired("uuid")

	sagaUpdateCmd.Flags().StringVar(&sagaUUID, "uuid", "", "saga uuid")
	sagaUpdateCmd.Flags().StringVar(&sagaSummary, "summary", "", "summary")
	sagaUpdateCmd.Flags().StringVar(&sagaLastEp, "last-episode-uuid", "", "last episode uuid")
	sagaUpdateCmd.Flags().StringVar(&sagaLSA, "last-summarized-at", "", "RFC3339")
	sagaUpdateCmd.Flags().StringVar(&sagaLSEV, "last-summarized-episode-valid-at", "", "RFC3339")
	_ = sagaUpdateCmd.MarkFlagRequired("uuid")

	sagaCmd.AddCommand(sagaCreateCmd, sagaGetCmd, sagaUpdateCmd)
	rootCmd.AddCommand(sagaCmd)
}

var sagaCmd = &cobra.Command{Use: "saga", Short: "Saga watermark operations"}

var sagaCreateCmd = &cobra.Command{
	Use: "create",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		s, err := c.CreateSaga(&gmem.Saga{Name: sagaName, GroupID: sagaGroup})
		if err != nil {
			fatal(err)
		}
		printJSON(s)
	},
}

var sagaGetCmd = &cobra.Command{
	Use: "get",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		s, err := c.GetSaga(sagaUUID)
		if err != nil {
			fatal(err)
		}
		printJSON(s)
	},
}

var sagaUpdateCmd = &cobra.Command{
	Use: "update",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		s, err := c.UpdateSaga(&gmem.Saga{
			UUID: sagaUUID, Summary: sagaSummary, LastEpisodeUUID: sagaLastEp,
			LastSummarizedAt: sagaLSA, LastSummarizedEpisodeValidAt: sagaLSEV,
		})
		if err != nil {
			fatal(err)
		}
		printJSON(s)
	},
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./pkg/gmem/ -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: saga watermark crud"
```

---

### Task 11: Community —— build (label propagation) + upsert

**Files:**
- Create: `pkg/gmem/community.go`
- Create: `pkg/gmem/community_test.go`
- Create: `cmd/gmem-cli/cmd_community.go`

**Interfaces:**
- Consumes: `entityFromNode`、`Embedder.Embed`
- Produces:
  - `gmem.Cluster{ID int; MemberUUIDs []string; MemberNames []string}`
  - `(*Client).BuildCommunities(groupID string) ([]Cluster, error)` — 连通分量 + 简单 label propagation（取邻居多数标签，迭代 10 轮收敛）
  - `gmem.Community{UUID, Name, GroupID, Summary, CreatedAt string}`
  - `(*Client).UpsertCommunity(name, summary string, memberUUIDs []string, groupID string) (*Community, error)` — 生成 name_embedding，建 HAS_MEMBER 边
  - CLI：`community build [--group-id]`、`community upsert --name --summary --member-uuids uuid1,uuid2`

**BuildCommunities 算法（简单实现，无需 LLM）：**
1. 查出 group 内所有 Entity 及 RELATES_TO 邻接关系
2. 初始：每个节点标签 = 自己的 uuid
3. 迭代 ≤10 轮：每个节点取邻居中票数最多的标签（平票取字典序最小），无邻居的孤立节点自成一类
4. 收敛后按标签分组输出 Cluster（单节点 cluster 也返回，由 agent 决定是否值得写回）

- [ ] **Step 1: 写失败测试**

`pkg/gmem/community_test.go`:

```go
package gmem

import "testing"

func TestBuildCommunities(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	// 两个连通分量：{a,b,c} 全连通，{x,y} 连通
	mk := func(name string) string {
		e, _, err := c.UpsertEntity(&Entity{Name: name}, false)
		if err != nil {
			t.Fatal(err)
		}
		return e.UUID
	}
	a, b, cc := mk("ca"), mk("cb"), mk("cc")
	x, y := mk("cx"), mk("cy")
	link := func(s, t string) {
		if _, err := c.graph.Query(`MATCH (s:Entity {uuid:$s}), (t:Entity {uuid:$t})
			CREATE (s)-[:RELATES_TO {uuid:$u, group_id:'default'}]->(t)`,
			map[string]any{"s": s, "t": t, "u": newUUID()}, nil); err != nil {
			t.Fatal(err)
		}
	}
	link(a, b)
	link(b, cc)
	link(a, cc)
	link(x, y)

	clusters, err := c.BuildCommunities("")
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 2 {
		t.Fatalf("want 2 clusters, got %d: %+v", len(clusters), clusters)
	}
	sizes := map[int]bool{}
	for _, cl := range clusters {
		sizes[len(cl.MemberUUIDs)] = true
	}
	if !sizes[3] || !sizes[2] {
		t.Fatalf("cluster sizes: %v", sizes)
	}
}

func TestUpsertCommunity(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	e1, _, _ := c.UpsertEntity(&Entity{Name: "m1"}, false)
	e2, _, _ := c.UpsertEntity(&Entity{Name: "m2"}, false)

	comm, err := c.UpsertCommunity("backend-team", "backend engineers", []string{e1.UUID, e2.UUID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if comm.UUID == "" {
		t.Fatalf("bad community: %+v", comm)
	}
	// HAS_MEMBER 边
	qr, _ := c.graph.ROQuery(`MATCH (c:Community {uuid:$u})-[:HAS_MEMBER]->(e) RETURN count(e)`,
		map[string]any{"u": comm.UUID}, nil)
	qr.Next()
	if cnt, _ := qr.Record().GetByIndex(0).(int64); cnt != 2 {
		t.Fatalf("has_member: %d", cnt)
	}
	// name_embedding 已生成
	qr2, _ := c.graph.ROQuery(`MATCH (c:Community {uuid:$u}) RETURN c.name_embedding`,
		map[string]any{"u": comm.UUID}, nil)
	qr2.Next()
	if v, _ := qr2.Record().GetByIndex(0); v == nil {
		t.Fatal("name_embedding not set")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/gmem/ -run 'TestBuild|TestUpsertCommunity' -v`
Expected: FAIL — `BuildCommunities` undefined（编译错误）

- [ ] **Step 3: 实现 community.go**

`pkg/gmem/community.go`:

```go
package gmem

import (
	"fmt"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

type Cluster struct {
	ID          int      `json:"id"`
	MemberUUIDs []string `json:"member_uuids"`
	MemberNames []string `json:"member_names"`
}

// BuildCommunities 对 group 内 Entity 做 label propagation 聚类
func (c *Client) BuildCommunities(groupID string) ([]Cluster, error) {
	gid := c.GroupID(groupID)
	// 节点
	res, err := c.graph.ROQuery(`MATCH (n:Entity {group_id: $gid}) RETURN n.uuid, n.name`,
		map[string]any{"gid": gid}, nil)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for res.Next() {
		rec := res.Record()
		uuid, _ := rec.GetByIndex(0).(string)
		name, _ := rec.GetByIndex(1).(string)
		names[uuid] = name
	}
	// 邻接（无向）
	adj := map[string]map[string]bool{}
	for u := range names {
		adj[u] = map[string]bool{}
	}
	res, err = c.graph.ROQuery(`MATCH (a:Entity {group_id: $gid})-[:RELATES_TO]-(b:Entity {group_id: $gid})
		RETURN a.uuid, b.uuid`, map[string]any{"gid": gid}, nil)
	if err != nil {
		return nil, err
	}
	for res.Next() {
		rec := res.Record()
		au, _ := rec.GetByIndex(0).(string)
		bu, _ := rec.GetByIndex(1).(string)
		adj[au][bu] = true
		adj[bu][au] = true
	}
	// label propagation：标签 = 所属代表节点，迭代取邻居多数票
	label := map[string]string{}
	for u := range names {
		label[u] = u
	}
	for round := 0; round < 10; round++ {
		changed := false
		for u := range names {
			if len(adj[u]) == 0 {
				continue
			}
			votes := map[string]int{}
			for nb := range adj[u] {
				votes[label[nb]]++
			}
			best, bestN := label[u], 0
			for l, n := range votes {
				if n > bestN || (n == bestN && l < best) {
					best, bestN = l, n
				}
			}
			if best != label[u] {
				label[u] = best
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	groups := map[string][]string{}
	for u, l := range label {
		groups[l] = append(groups[l], u)
	}
	out := []Cluster{}
	i := 0
	for _, members := range groups {
		cl := Cluster{ID: i}
		for _, u := range members {
			cl.MemberUUIDs = append(cl.MemberUUIDs, u)
			cl.MemberNames = append(cl.MemberNames, names[u])
		}
		out = append(out, cl)
		i++
	}
	return out, nil
}

type Community struct {
	UUID      string   `json:"uuid"`
	Name      string   `json:"name"`
	GroupID   string   `json:"group_id"`
	Summary   string   `json:"summary,omitempty"`
	CreatedAt string   `json:"created_at"`
}

// UpsertCommunity 写回社区（agent 总结后调用），建 HAS_MEMBER 边
func (c *Client) UpsertCommunity(name, summary string, memberUUIDs []string, groupID string) (*Community, error) {
	gid := c.GroupID(groupID)
	emb, err := c.Embed.Embed(name)
	if err != nil {
		return nil, err
	}
	comm := &Community{UUID: newUUID(), Name: name, GroupID: gid, Summary: summary, CreatedAt: nowUTC()}
	_, err = c.graph.Query(`CREATE (n:Community {
		uuid: $uuid, name: $name, group_id: $gid, summary: $summary, created_at: $created_at
	}) SET n.name_embedding = vecf32($emb)`, map[string]any{
		"uuid": comm.UUID, "name": name, "gid": gid, "summary": summary,
		"created_at": comm.CreatedAt, "emb": emb,
	}, nil)
	if err != nil {
		return nil, err
	}
	for _, mu := range memberUUIDs {
		if _, err := c.graph.Query(`MATCH (c:Community {uuid: $c}), (e:Entity {uuid: $e})
			MERGE (c)-[:HAS_MEMBER {uuid: $uuid, group_id: $gid, created_at: $ts}]->(e)`,
			map[string]any{"c": comm.UUID, "e": mu, "uuid": newUUID(), "gid": gid, "ts": nowUTC()}, nil); err != nil {
			return comm, fmt.Errorf("has_member %s: %w", mu, err)
		}
	}
	return comm, nil
}

// 确保编译期引用（entityFromNode 在本包其他文件已用）
var _ = falkordb.Node{}
```

- [ ] **Step 4: CLI cmd_community.go**

`cmd/gmem-cli/cmd_community.go`:

```go
package main

import (
	"strings"

	"github.com/spf13/cobra"
)

var (
	commGroup, commName, commSummary, commMembers string
)

func init() {
	communityBuildCmd.Flags().StringVar(&commGroup, "group-id", "", "group id")

	communityUpsertCmd.Flags().StringVar(&commName, "name", "", "community name")
	communityUpsertCmd.Flags().StringVar(&commSummary, "summary", "", "community summary")
	communityUpsertCmd.Flags().StringVar(&commMembers, "member-uuids", "", "comma-separated entity uuids")
	communityUpsertCmd.Flags().StringVar(&commGroup, "group-id", "", "group id")
	_ = communityUpsertCmd.MarkFlagRequired("name")
	_ = communityUpsertCmd.MarkFlagRequired("member-uuids")

	communityCmd.AddCommand(communityBuildCmd, communityUpsertCmd)
	rootCmd.AddCommand(communityCmd)
}

var communityCmd = &cobra.Command{Use: "community", Short: "Community operations"}

var communityBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Cluster entities via label propagation",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		clusters, err := c.BuildCommunities(commGroup)
		if err != nil {
			fatal(err)
		}
		printJSON(clusters)
	},
}

var communityUpsertCmd = &cobra.Command{
	Use:   "upsert",
	Short: "Write back a summarized community",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		members := strings.Split(commMembers, ",")
		comm, err := c.UpsertCommunity(commName, commSummary, members, commGroup)
		if err != nil {
			fatal(err)
		}
		printJSON(comm)
	},
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./pkg/gmem/ -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: community build (label propagation) and upsert"
```

---

### Task 12: Skill 文档 + README + 收尾验证

**Files:**
- Create: `skills/gmem/SKILL.md`
- Create: `README.md`

**Interfaces:**
- Consumes: 全部 CLI 命令（Task 1-11）

- [ ] **Step 1: 写 SKILL.md**

`skills/gmem/SKILL.md`:

```markdown
---
name: gmem
description: Use gmem-cli to persist and recall long-term memory in a FalkorDB knowledge graph. Use when you need to remember facts about people/projects across sessions, recall prior context, or maintain the memory graph.
---

# gmem — Agent Memory via gmem-cli

gmem-cli 操作一个 FalkorDB 知识图谱（graphiti schema）。**你负责推理（抽取、判断），CLI 负责存储和检索。**

## 开始前

```bash
gmem-cli status          # 确认 FalkorDB 和 embedding API 可用
gmem-cli schema show     # 了解已定义的实体/边类型（没有则自由写入）
```

## 写入记忆

对话/事件发生后，抽取实体和关系，一次写入：

```bash
gmem-cli add --content "<原文>" --source message \
  --entities '[{"name":"Alice","labels":["Person"],"summary":"后端工程师"}]' \
  --edges '[{"source":"Alice","target":"graph-memory","name":"WORKS_ON","fact":"Alice 负责 graph-memory"}]'
```

单条事实快速写入：`gmem-cli add-triplet --source Alice --name WORKS_ON --fact "..." --target graph-memory`

规则：
- 一个实体一个主类型 label，放 labels 最前（`["Person"]`）
- edges 的 source/target 用实体 **name**，已存在的实体自动去重
- attributes 需符合 `schema show` 中的 required/enum；临时数据加 `--lenient`

## 检索记忆

```bash
gmem-cli search --query "Alice 的项目" --limit 10
```

返回 entities + facts + episodes。判断相关性是你的工作——阅读 fact 文本并自行取舍。

- `--as-of 2026-06-01T00:00:00Z`：时点查询（"6月时 Alice 在哪个团队"）
- `--include-invalid`：包含已失效的历史事实

## 事实变化：失效 + 新增（不要 update 边）

```bash
gmem-cli edge invalidate --uuid <旧边uuid>
gmem-cli add-triplet --source Alice --name MEMBER_OF --fact "Alice 于2026年7月加入B团队" --target TeamB
```

## 维护

- 实体信息加深：`gmem-cli entity update --uuid <uuid> --summary "..." --attributes '{...}'`（默认合并）
- 发现重复实体：`gmem-cli entity merge --from <A> --to <B>`（B 保留，A 的边和属性并入）
- 记错了：`gmem-cli node delete --uuid <uuid>`（物理删除，与"失效"区分）
- 定期整理（每周或记忆量大时）：
  1. `gmem-cli community build` → 得到聚类
  2. 你阅读每个 cluster 的成员并写总结
  3. `gmem-cli community upsert --name "..." --summary "..." --member-uuids uuid1,uuid2`
```

- [ ] **Step 2: 写 README.md**

`README.md`（面向人）:

```markdown
# graph-memory

Agent 长期记忆系统：FalkorDB + graphiti 图 schema + Go CLI。

- 推理由 agent 完成，gmem-cli 负责持久化/检索/向量
- 依赖：FalkorDB + OpenAI 兼容 embedding API

## 快速开始

```bash
docker run -d -p 6379:6379 falkordb/falkordb:latest
export EMBEDDING_API_KEY=sk-...
go build -o gmem-cli ./cmd/gmem-cli
./gmem-cli init
./gmem-cli add --content "..." --entities '...' --edges '...'
./gmem-cli search --query "..."
```

配置见 `gmem.yaml`（或环境变量 FALKORDB_ADDR / EMBEDDING_API_BASE / EMBEDDING_API_KEY / EMBEDDING_MODEL）。
设计文档：docs/superpowers/specs/2026-07-19-graph-memory-design.md
```

- [ ] **Step 3: 全量验证**

Run: `go vet ./... && go test ./... -v`
Expected: 全部 PASS

Run: `go build -o gmem-cli ./cmd/gmem-cli && ./gmem-cli --help`
Expected: 列出所有命令：add, add-triplet, search, init, status, schema, episode, entity, edge, node, saga, community

- [ ] **Step 4: Commit + push**

```bash
git add -A
git commit -m "feat: skill doc and readme"
git push origin main
```

## Self-Review 记录

- **Spec 覆盖**：✅ add/add-triplet/search/init/status/schema show/episode get+list/entity get+search+update+merge/edge upsert+invalidate+search+delete/node delete/saga create+get+update/community build+upsert —— 全部有对应任务
- **偏离声明**：spec "向量索引" → graphiti 现行做法（Cypher 内 cosineDistance），已在 Global Constraints 声明
- **类型一致性**：`EntityWithScore`/`EdgeWithScore`/`EpisodeWithScore` 在 Task 7 定义，Task 7 CLI 使用；`normalizeTime`/`nowUTC`/`newUUID` 在 Task 2 定义，Task 4/6/7/8/10 使用；`mapToJSON`/`jsonToMap`/`strSlice` 在 Task 4 定义，Task 4/5/6 使用；`containsStr` 在 Task 5 定义并使用 ✅
- **placeholder 扫描**：无 TBD/TODO；所有代码步骤含完整代码 ✅
