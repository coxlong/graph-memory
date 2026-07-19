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

Search before answering anything that depends on stored facts.

```bash
gmem-cli search --query "Alice team" --limit 10   # entities + facts + episodes
gmem-cli entity search --query "Alice"            # entities only
gmem-cli edge   search --query "works on"         # facts only
```

Temporal state of facts:

- default: only facts valid **now**
- `--as-of 2026-03-01T00:00:00Z` (top-level `search` only): facts valid at that moment
- `--include-invalid`: include superseded facts (history)

Usage rules:

- `score` is a retrieval rank, not truth. Judge relevance yourself.
- A miss is not proof of absence — retry with different query terms.
- Fetch full records by uuid: `entity get`, `episode get`, `saga get`.

## Storing memory

**Before your first write, read [references/extraction.md](references/extraction.md)
and follow it.** It defines what to extract, how to phrase facts, temporal rules,
dedup/invalidation, and summary style. `gmem-cli` stores what you give it verbatim —
extraction quality is your responsibility.

Flow:

1. `gmem-cli schema show` — use configured types if present.
2. Extract entities + facts from the content (per extraction.md).
3. `gmem-cli add ...` — episode + entities + edges in one call.
4. A new fact that contradicts a stored one: `edge invalidate` the old, then write
   the new. Never edit a fact in place.

## Commands

| Command | Use |
|---|---|
| `init` / `status` | create indexes / check connectivity |
| `schema show` | print configured entity & edge types |
| `add` | episode + extracted entities + edges in one call |
| `add-triplet` | a single fact (entities auto-deduped by name) |
| `search` / `entity search` / `edge search` | recall; `--as-of`, `--include-invalid`, `--limit` |
| `episode get` / `episode list` | raw stored content |
| `entity get` / `entity update` / `entity merge` | entity detail / deepen / dedup |
| `edge upsert` / `edge invalidate` / `edge delete` | write / supersede / remove a fact |
| `node delete` | delete any node + its edges |
| `community build` / `community upsert` | candidate clusters / write community summary |
| `saga create` / `saga get` / `saga update` | incremental summarization watermark |

## Notes

- Times are RFC3339 UTC.
- `--group-id <id>` (global flag) selects an isolated memory space; the configured
  default group applies otherwise. Run `init` once per group.
- `add` is not transactional: a failure mid-call may leave an episode without its
  entities/edges. Retrying is safe but check `episode list` and `node delete` the
  orphan.
