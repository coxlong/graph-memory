---
name: graph-memory
description: Recall stored facts (people, projects, decisions, preferences, history) before answering questions that depend on them, and store durable knowledge as it appears — decisions and outcomes, state changes (role, project, location, status), explicitly stated preferences or requirements, plans and commitments, corrections of earlier facts. Do NOT store small talk, transient details, speculation, or secrets/credentials.
---

# graph-memory

`gmem-cli` stores and retrieves long-term memory as a knowledge graph. Call it via
Bash. All output is JSON on stdout; failures exit non-zero.

## Setup (once per group)

```bash
gmem-cli status        # FalkorDB + embedding API reachable?
gmem-cli init          # create indexes (idempotent)
gmem-cli schema show   # configured entity/edge types — read BEFORE writing
```

## Recalling memory

Search before answering anything that depends on stored facts. Facts live on
edges; entities are the anchors they connect.

```bash
gmem-cli edge   search --query "works on"         # facts (main recall path)
gmem-cli entity search --query "Alice"            # entities / anchors
```

Narrow by type (configured schema types) and point in time:

- `--type Person` or `--type MEMBER_OF,WORKS_ON`: filter by entity label / edge name.
  Comma-separated; an unknown type simply matches nothing.
- `--method hybrid|vector|bm25` (default `hybrid`)
- default: only facts valid **now**
- `--as-of 2026-03-01T00:00:00Z` (edge search): facts valid at that moment
- `--include-invalid` (edge search): include superseded facts (history)

Usage rules:

- `score` is a retrieval rank, not truth. Judge relevance yourself.
- A miss is not proof of absence — retry with different query terms.
- Provenance: an edge carries `episodes` (source episode uuids); `episode get`
  or `episode list` retrieves the raw content a fact came from.
- Fetch full records by uuid: `entity get`, `episode get`, `saga get`.

## Storing memory

**Before any write, read [references/writing.md](references/writing.md) and
follow it.** It defines what to extract, how to phrase facts, temporal rules, the
write flow (preflight, dedup, invalidation), and summary style. `gmem-cli` stores
what you give it verbatim — extraction quality is your responsibility.

## Notes

- Times are RFC3339 UTC.
- `--group-id <id>` (global flag) selects an isolated memory space; the configured
  default group applies otherwise. Run `init` once per group.
- Edges are the memory: entities are anchors, facts live on edges. After `add`,
  `edge_uuids` must be non-empty whenever the content contained a durable fact —
  an empty list means nothing was remembered.
- `add` is not transactional: a failure mid-call may leave an episode without its
  entities/edges. Retrying is safe but check `episode list` and `node delete` the
  orphan.
