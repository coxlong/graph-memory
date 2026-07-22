# graph-memory (gmem-cli)

Agent memory system backed by [FalkorDB](https://www.falkordb.com/), with a graph
schema aligned to [graphiti](https://github.com/getzep/graphiti) (Apache-2.0; see
[NOTICES](NOTICES)). The `gmem-cli` command line tool persists and retrieves memory;
**inference is left to the calling agent** (entity extraction, deciding a fact is
stale, summarization). Core logic lives in the reusable `pkg/gmem` library so a
future MCP server can share it.

## Architecture

- `pkg/gmem` — library: FalkorDB client, embedding, CRUD, search, graph ops.
- `cmd/gmem-cli` — thin cobra CLI over the library.

External dependencies are only **FalkorDB** and an **OpenAI-compatible embedding
API**. No LLM/chat dependency.

## Graph model

- **Nodes**: `Episodic` (raw events, single label), `Entity` (multi-label
  `:Entity:Person:Project`), `Community`, `Saga`.
- **Edges**: `MENTIONS` (Episodic→Entity), `RELATES_TO` (Entity→Entity, with
  temporal fields `valid_at`/`invalid_at`/`expired_at`), `HAS_MEMBER`
  (Community→Entity), `HAS_EPISODE` (Saga→Episodic), `NEXT_EPISODE`
  (Episodic→Episodic, chains episodes within a saga).
- **Temporal facts are immutable**: a change = invalidate old edge + create new.
  `valid_at` / `invalid_at` support point-in-time (`--as-of`) queries.

## Build & test

```bash
make build      # or: go build -o gmem-cli ./cmd/gmem-cli
make install    # installs to ~/.local/bin (PREFIX=... to override)
make test       # needs a reachable FalkorDB (env FALKORDB_TEST_ADDR)
```

Integration tests use an env-provided FalkorDB and a local httptest embedding
server; they `t.Skip` when no server is available.

## Configure

`gmem-cli` reads `~/.gmem.yaml` if present; environment variables override the
file. Both are optional — defaults apply when absent.

```bash
# ~/.gmem.yaml
falkordb_addr: host:port
falkordb_user: ...            # optional
falkordb_password: ...        # optional
group_id: default             # default group; each group_id is a separate FalkorDB graph
embedding_api_base: https://api.openai.com/v1
embedding_api_key: ...
embedding_model: text-embedding-3-small
schema:                       # optional inline type schema (entity/edge validation)
  entity_types: ...
  edge_types: ...
```

See [`gmem.example.yaml`](gmem.example.yaml) for a complete annotated example
including a full schema.

The same keys can be set as env vars: `FALKORDB_ADDR`, `FALKORDB_USER`,
`FALKORDB_PASSWORD`, `GMEM_GROUP_ID`, `EMBEDDING_API_BASE`,
`EMBEDDING_API_KEY`, `EMBEDDING_MODEL`.

`--group-id <id>` overrides the configured default on any command and selects a
different FalkorDB graph (the graph is named by the group id itself). Groups are
**physically isolated** (graphiti-aligned): data in one group is invisible to
another. Run `gmem-cli --group-id <id> init` once per new group to create its
indexes.

A configured `schema` enables validating required/enum attributes and edge
endpoint types; `--lenient` skips validation per command.

## Commands

| Command | Purpose |
|---|---|
| `init` / `status` | create indexes / check connectivity |
| `schema show` | print configured entity & edge types |
| `add` | episode + extracted entities + edges in one call; `--dry-run` preflight returns duplicate candidates, `duplicate_of`/`invalidate` per-edge dispositions |
| `add-triplet` | a single fact (entities deduped by name); `--dry-run`, `--duplicate-of`, `--invalidate`, `--episode-uuid` |
| `entity search` / `edge search` | hybrid vector+fulltext (RRF); `--method`, `--type`, `--as-of`, `--include-invalid`, `--limit` |
| `episode get|list` | episode operations |
| `entity get|update|merge` | entity operations |
| `edge upsert|invalidate|delete` | fact edges with temporal invalidation |
| `node delete` | delete any node + cascading edges |
| `community build|upsert` | candidate clusters → agent summary writeback |
| `saga create|get|update` | incremental summarization watermark |

See [`skills/graph-memory/SKILL.md`](skills/graph-memory/SKILL.md) for agent-facing
usage (recall/store workflows) and
[`skills/graph-memory/references/writing.md`](skills/graph-memory/references/writing.md)
for the write-side guide agents follow (extraction, dedup, maintenance).
