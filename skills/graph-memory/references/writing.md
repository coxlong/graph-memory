# Writing to memory

Read this before writing to the graph. `gmem-cli` persists what you give it
verbatim; extraction quality is entirely your responsibility. These principles are
adapted from graphiti's extraction prompts (Apache-2.0, see NOTICES).

## 1. Episodes

One `add` call = one episode (the raw content, kept verbatim as the source of
truth) + the entities and facts you extract from it.

- `--source`: `message` (a conversation turn), `text` (document/prose),
  `json` (a structured record).
- `--content`: the raw text. Do not pre-summarize — the episode is the evidence.
- `--valid-at`: when the event happened (default: unset).
- Keep one episode to one coherent event or message. Don't dump a whole session
  into a single episode.

`add` returns `{episode_uuid, entities: {name -> uuid}, edge_uuids, edges}`.
`edges[i]` corresponds to `--edges[i]`: on a real write it carries `edge_uuid`
and `merged` (true when the edge was an attribution merge via `duplicate_of`);
with `--dry-run` it carries `candidates` instead and nothing is written.

## 2. Entity extraction

Extract entities **explicitly mentioned** in the current content that are specific
enough to be uniquely identifiable. Ask: "Could this have its own Wikipedia
article, or be distinguished from other items of its kind within this memory?"

Always extract:

- The speaker (the part before the colon in a dialogue line) — once, even if
  mentioned again in the body.
- Named people, organizations, projects, places, products, documents.
- Specific objects carrying a distinguishing detail (brand, color, model, owner,
  material): "wool coat", "Ford Mustang", "dog leash", "cracked windshield".

NEVER extract:

- Pronouns (you, me, I, he, she, they, it, this, that, those) — resolve them to
  the referenced entity's name instead.
- Abstract concepts or feelings (joy, growth, motivation, balance).
- Generic common nouns (day, work, stuff, things, supplies, gear, people).
- Generic media/event nouns unless uniquely identified (photo, pic, game, meeting,
  event, workshop).
- Broad institutional nouns unless explicitly named (government, school, company,
  team, office).
- Bare relational or pet terms — qualify with the possessor: "Nisha's dad", not
  "dad"; "Jordan's cat", not "cat".
- Dates, times, temporal information (handled as `valid_at` / `invalid_at`).
- Relationships or actions (those become edges, not entities).
- Sentence fragments, adjectives, descriptive phrases.

Rules:

- Use the **most specific form** mentioned: "road cycling", not "cycling".
- Use full, unambiguous names when available.
- When in doubt, do NOT extract.

Examples:

```
Message: "Nisha: My dad is visiting next week. He loves walking his dogs in Riverside Park."
Good: "Nisha", "Nisha's dad", "Riverside Park"
Bad:  "dad" (bare term), "dogs" (bare animal), "next week" (temporal)

Message: "Alex: I shared a pic from the game after the event."
Good: "Alex"
Bad:  "pic", "game", "event" (generic nouns)

Message: "Mary: I forgot Trigger's leash so I went road cycling in my new wool coat."
Good: "Mary", "Trigger", "dog leash", "road cycling", "wool coat"
Bad:  "leash", "cycling", "coat" (too generic — keep the qualifier)
```

## 3. Types and attributes

- Run `gmem-cli schema show` first. If a schema is configured, use exactly those
  entity labels and required attributes. With no schema, labels are free-form.
- Under a configured schema, new entities must carry a configured label —
  `add-triplet` (which creates untyped entities) is only for facts between
  **existing** typed entities; for new entities use `add` with typed `--entities`.
- Attribute values must be one of:
  (a) a value copied or directly normalized from the content,
  (b) the existing value on the entity (preserved unchanged),
  (c) null / omitted.
- NEVER write reasoning or commentary into an attribute ("(implied by ...)",
  "X, or maybe Y"). NEVER write "N/A", "unknown", or a sentence describing
  absence. NEVER infer values from the entity's name or world knowledge.

## 4. Fact (edge) extraction

- A fact connects two **distinct** entities from your extracted set, referenced by
  **name**, never by pronoun.
- `name` (relation type): prefer a configured edge type from the schema; otherwise
  derive a SCREAMING_SNAKE_CASE predicate (WORKS_AT, LIVES_IN, IS_FRIENDS_WITH).
- `fact`: one natural-language sentence that preserves **every concrete detail**
  from the source — proper nouns, brands, model numbers, quantities, colors,
  dates. Paraphrase the syntax, but NEVER generalize:
  - "Gamecube" must not become "gaming console"
  - "Ford Mustang" must not become "car"
  - "three screenplays" must not become "several screenplays"
- Do not emit semantically redundant facts. But a version with **more** detail is
  a new fact, not a duplicate: "user plays video games" then "user plays games on
  a Gamecube" → keep both.
- A concrete detail about a single entity should be anchored to a second entity
  when one exists: Alice → OWNS → wool coat. If no second entity fits, put the
  detail in the entity's summary/attributes instead of making a vague edge.
  - BAD: "Alice feels happy" (vague state, no anchor)
  - GOOD: Alice → FEELS_HAPPY_ABOUT → Bob's promotion

## 5. Temporal rules

- `valid_at`: when the fact became true. `invalid_at`: when it stopped being true.
  `expired_at`: a hard expiry after which the fact must not surface (rare — a
  coupon, a lease). Set only with explicit evidence.
- Resolve relative expressions ("last week", "yesterday", "two years ago") against
  the time of the episode the fact comes from. Use that episode time as
  `valid_at` for ongoing, present-tense facts.
- Only a date known → `T00:00:00Z`. Only a year → January 1, `T00:00:00Z`.
  All times RFC3339 UTC.
- NEVER invent dates or infer temporal bounds from unrelated events. If no time
  is stated or resolvable, leave the field unset.

## 6. Dedup and invalidation

Entities:

- Two entities are duplicates **only** if they refer to the same real-world
  object. Same name but different things ("Java" the language vs "Java" the
  island) are NOT duplicates. When unsure, they are NOT duplicates.
- Entities are auto-deduped by exact name at write time. If you later discover
  near-duplicates ("NYC" vs "New York City"), merge them:
  `gmem-cli entity merge --from <dup-uuid> --to <canonical-uuid>`.

Facts — `gmem-cli add --dry-run` returns `edges[].candidates` for every edge:
existing edges with the same source, target, and relation name, invalidated ones
included. Judge each candidate before the real write:

- **DUPLICATE** (identical factual information): do not create a parallel edge.
  Set `"duplicate_of": "<candidate uuid>"` on that edge in the real `add` so
  your episode is appended to the existing edge's `episodes` attribution:
  ```bash
  gmem-cli add-triplet --source Alice --name WORKS_AS --fact "..." --target "Acme Corp" \
    --duplicate-of <existing-edge-uuid> [--episode-uuid <ep>]
  ```
- **CONTRADICTION** (same relationship, updated content — "works as a software
  engineer" vs "works as a senior engineer"): set `"invalidate": ["<old-uuid>"]`
  on that edge. The old edge is invalidated (`invalid_at` = now) and the new one
  written in the same `add` call. If the fact stopped being true at an earlier
  time you know, invalidate it yourself first so history gets the right
  timestamp, then write without the `invalidate` field:
  ```bash
  gmem-cli edge invalidate --uuid <old-uuid> --invalid-at <when it stopped being true>
  ```
  Never update an edge's fact in place — facts are immutable; history is kept.
- **DIFFERENT events** ("ran 5 miles Tuesday" vs "ran 3 miles Wednesday"):
  neither duplicate nor contradiction — both coexist, no disposition.

## 7. Summaries

Applies to entity summaries (`entity update --summary`), community summaries
(`community upsert`), and saga briefs (`saga update --summary`):

- 2–6 dense sentences, third person. State facts directly.
- NEVER use meta-language: "mentioned", "discussed", "noted", "stated",
  "described", "referenced", "indicated", "reported", "the entity", "the
  conversation". Use a communication verb only when the act of communicating is
  itself the fact ("announced", "asked").
- Lead with the entity's name or a concrete fact — never with "A", "An", "The",
  or "This is" (unless part of a proper name).
- Preserve all names, roles, places, dates, counts, and temporal qualifiers.
- Newer explicit facts supersede older content. If nothing new was learned,
  leave the summary unchanged.
- Never infer habits, preferences, or trends from a single mention — capture a
  preference only when explicitly stated ("I prefer X") or repeatedly evidenced.
- State the content of what was said, not that it was said.

Example:

```
GOOD: "Deployment moved from March 8 to March 15 because the staging environment
       is not ready. Priya owns updating the client timeline."
BAD:  "Jordan mentioned moving the deployment date. Priya discussed updating
       the timeline."
```

## 8. Write workflows

Every write follows the same flow:

1. `gmem-cli schema show` — use configured types if present.
2. Extract entities + facts from the content (per §2–§5).
3. `gmem-cli add --dry-run ...` — preflight: validates all inputs and returns
   `edges[].candidates` (existing edges with the same source, target, and
   relation name, invalidated ones included). `edges[i]` matches `--edges[i]`.
   Zero writes.
4. Judge each candidate (§6) and set dispositions (`duplicate_of` / `invalidate`)
   on the affected edges.
5. `gmem-cli add ...` (same args, no `--dry-run`) — episode + entities + edges
   written with your dispositions applied. Never skip the dry-run: it exposes
   duplicates and contradictions you would otherwise create blindly.

New episode with entities and facts (the common case):

```bash
# 1. preflight: validate + see duplicates/contradictions (zero writes)
gmem-cli add --content "Alice joined TeamB as backend engineer" --source message --dry-run \
  --entities '[{"name":"Alice","labels":["Person"],"summary":"backend engineer","attributes":{"role":"backend"}},
               {"name":"TeamB","labels":["Project"]}]' \
  --edges '[{"source":"Alice","target":"TeamB","name":"MEMBER_OF",
             "fact":"Alice is a backend engineer on TeamB","valid_at":"2026-07-19T00:00:00Z"}]'

# 2. real write — apply dispositions from the candidates you reviewed.
#    duplicate_of and invalidate are mutually exclusive per edge:
#    a duplicate is never written, so there is nothing to invalidate.
gmem-cli add --content "Alice joined TeamB as backend engineer" --source message \
  --entities '[...]' \
  --edges '[{"source":"Alice","target":"TeamB","name":"MEMBER_OF","fact":"..."}]'
#   add  "duplicate_of": "<uuid>"  to an edge whose candidate was a DUPLICATE
#   add  "invalidate": ["<uuid>"]  to an edge whose candidate was a CONTRADICTION
```

Single fact (entities auto-created/deduped by name):

```bash
gmem-cli add-triplet --source Alice --name WORKS_ON --fact "Alice works on gmem" --target gmem
```

A fact changed → invalidate + write (see §6).

Deepening an entity with no new relation:

```bash
gmem-cli entity update --uuid <uuid> --summary "..." --attributes '{"role":"lead"}'   # merges; --replace overwrites
```

Maintaining stored facts (corrections after the fact):

```bash
gmem-cli edge invalidate --uuid <uuid> [--invalid-at <t>]   # retire a fact; default: now
gmem-cli edge delete --uuid <uuid>                          # remove a fact written by mistake
gmem-cli edge upsert --source-uuid <u> --target-uuid <u> --name REL --fact "..." \
  [--episode-uuid <ep>] [--attributes '{...}']
# edge upsert is the low-level path: it skips dedup/candidate checks. Use it only
# for what add/add-triplet cannot do (edge attributes); normal writes go through
# add / add-triplet so duplicates are caught.
```

Periodic consolidation:

```bash
gmem-cli community build                          # candidate clusters; you review
gmem-cli community upsert --name People --summary "..." --member-uuids <uuid1>,<uuid2>
gmem-cli add --saga project-x --content "..." --entities '[]' --edges '[]'   # episode chained into saga
gmem-cli saga update --uuid <saga> --summary "..." --last-episode-uuid <ep> \
  --last-summarized-at <now> --last-summarized-episode-valid-at <t>
```

Failure handling: `add` is not transactional. If it fails partway (e.g. embedding
API error), an orphan episode may remain. Retry the call, then check
`episode list` and `node delete --uuid <orphan-episode>` the leftover.
