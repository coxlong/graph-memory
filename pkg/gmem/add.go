package gmem

import "fmt"

type AddEntityInput struct {
	Name       string         `json:"name"`
	Labels     []string       `json:"labels,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type AddEdgeInput struct {
	Source      string   `json:"source"` // entity name
	Target      string   `json:"target"` // entity name
	Name        string   `json:"name"`
	Fact        string   `json:"fact"`
	ValidAt     string   `json:"valid_at,omitempty"`
	ExpiredAt   string   `json:"expired_at,omitempty"`
	DuplicateOf string   `json:"duplicate_of,omitempty"` // existing edge uuid: merge episode attribution, don't create
	Invalidate  []string `json:"invalidate,omitempty"`   // edge uuids to invalidate before writing this edge
}

type AddInput struct {
	Episode  *Episode         `json:"episode"`
	Entities []AddEntityInput `json:"entities,omitempty"`
	Edges    []AddEdgeInput   `json:"edges,omitempty"`
	GroupID  string           `json:"group_id,omitempty"`
	Saga     string           `json:"saga,omitempty"` // saga name; links episode via HAS_EPISODE/NEXT_EPISODE
	Lenient  bool             `json:"-"`
}

// EdgeWriteResult reports one edge write: the edge uuid, whether it was an
// attribution merge into an existing edge, and (dry-run only) pre-existing
// edges with the same (source, target, name) for the agent to dedup against.
type EdgeWriteResult struct {
	EdgeUUID   string          `json:"edge_uuid"`
	Merged     bool            `json:"merged"`
	Candidates []EdgeCandidate `json:"candidates,omitempty"`
}

type AddResult struct {
	EpisodeUUID string            `json:"episode_uuid"`
	Entities    map[string]string `json:"entities"` // name -> uuid
	EdgeUUIDs   []string          `json:"edge_uuids"`
	Edges       []EdgeWriteResult `json:"edges,omitempty"`
}

// validateAddInput pre-checks every input parameter before any write.
// Parameter errors abort before the episode is created, so a bad batch never
// leaves partial writes. DB errors (embedding, network) can still fail mid-way.
func (c *Client) validateAddInput(in *AddInput) error {
	gid := c.GroupID(in.GroupID)

	// episode: source + valid_at (mirrors CreateEpisode checks)
	if !validSources[in.Episode.Source] {
		return fmt.Errorf("episode: invalid source %q (message|text|json)", in.Episode.Source)
	}
	if _, err := normalizeTime(in.Episode.ValidAt); err != nil {
		return fmt.Errorf("episode: %w", err)
	}

	// entities: labels safe + schema-valid for the ones declared in this batch
	declared := map[string]AddEntityInput{}
	for _, e := range in.Entities {
		if err := ValidateLabels(e.Labels); err != nil {
			return fmt.Errorf("entity %q: %w", e.Name, err)
		}
		if err := c.Schema.ValidateEntity(e.Labels, e.Attributes, in.Lenient); err != nil {
			return fmt.Errorf("entity %q: %w", e.Name, err)
		}
		declared[e.Name] = e
	}

	// edges: time formats + schema. Endpoint labels come from the batch when
	// declared there, else from the existing entity in the graph.
	labelsOf := func(name string) ([]string, error) {
		if e, ok := declared[name]; ok {
			return e.Labels, nil
		}
		res, err := c.graph.ROQuery(`MATCH (n:Entity {name: $name, group_id: $gid}) RETURN labels(n) LIMIT 1`,
			map[string]any{"name": name, "gid": gid}, nil)
		if err != nil {
			return nil, err
		}
		if !res.Next() {
			return nil, nil // entity will be created with no labels; ValidateEdge will reject if schema requires them
		}
		v, err := res.Record().GetByIndex(0)
		if err != nil {
			return nil, err
		}
		raw, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("unexpected labels type for %q", name)
		}
		labels := make([]string, 0, len(raw))
		for _, l := range raw {
			if s, ok := l.(string); ok {
				labels = append(labels, s)
			}
		}
		return labels, nil
	}
	edgeExists := func(uuid string) error {
		res, err := c.graph.ROQuery(`MATCH ()-[r:RELATES_TO {uuid: $uuid}]->() RETURN count(r)`,
			map[string]any{"uuid": uuid}, nil)
		if err != nil {
			return err
		}
		if res.Next() {
			cnt, _ := res.Record().GetByIndex(0)
			if n, ok := cnt.(int64); ok && n > 0 {
				return nil
			}
		}
		return fmt.Errorf("edge %s not found", uuid)
	}
	for _, ei := range in.Edges {
		if _, err := normalizeTime(ei.ValidAt); err != nil {
			return fmt.Errorf("edge %q: valid_at: %w", ei.Name, err)
		}
		if _, err := normalizeTime(ei.ExpiredAt); err != nil {
			return fmt.Errorf("edge %q: expired_at: %w", ei.Name, err)
		}
		if ei.DuplicateOf != "" {
			if err := edgeExists(ei.DuplicateOf); err != nil {
				return fmt.Errorf("edge %q: duplicate_of: %w", ei.Name, err)
			}
			continue // merge path: no schema/time checks needed for a fact we won't write
		}
		for _, inv := range ei.Invalidate {
			if err := edgeExists(inv); err != nil {
				return fmt.Errorf("edge %q: invalidate: %w", ei.Name, err)
			}
		}
		srcLabels, err := labelsOf(ei.Source)
		if err != nil {
			return fmt.Errorf("edge %q: source %q: %w", ei.Name, ei.Source, err)
		}
		tgtLabels, err := labelsOf(ei.Target)
		if err != nil {
			return fmt.Errorf("edge %q: target %q: %w", ei.Name, ei.Target, err)
		}
		if err := c.Schema.ValidateEdge(ei.Name, srcLabels, tgtLabels, in.Lenient); err != nil {
			return fmt.Errorf("edge %q: %w", ei.Name, err)
		}
	}
	return nil
}

// Add writes a complete memory in one call: episode + entities + MENTIONS + RELATES_TO.
// Not transactional; upserts are idempotent so a failed retry is safe.
// All parameters are validated before any write; only DB/embedding failures
// can leave partial writes.
// dryRun=true runs validation + candidate detection only, zero writes.
func (c *Client) Add(in *AddInput, dryRun bool) (*AddResult, error) {
	if err := c.validateAddInput(in); err != nil {
		return nil, err
	}
	gid := c.GroupID(in.GroupID)
	result := &AddResult{Entities: map[string]string{}}

	if dryRun {
		// resolve endpoint uuids without writing, then detect candidates per edge
		uuidOf := func(name string) string {
			res, err := c.graph.ROQuery(`MATCH (n:Entity {name: $name, group_id: $gid}) RETURN n.uuid LIMIT 1`,
				map[string]any{"name": name, "gid": gid}, nil)
			if err != nil || !res.Next() {
				return ""
			}
			v, _ := res.Record().GetByIndex(0)
			s, _ := v.(string)
			return s
		}
		for _, ei := range in.Edges {
			wr := EdgeWriteResult{}
			if ei.DuplicateOf == "" {
				src, tgt := uuidOf(ei.Source), uuidOf(ei.Target)
				if src != "" && tgt != "" {
					if cands, err := c.FindEdgeCandidates(src, tgt, ei.Name); err == nil {
						for _, cand := range cands {
							wr.Candidates = append(wr.Candidates, edgeCandidate(cand))
						}
					}
				}
			}
			result.Edges = append(result.Edges, wr)
		}
		return result, nil
	}

	in.Episode.GroupID = gid
	ep, err := c.CreateEpisode(in.Episode)
	if err != nil {
		return nil, fmt.Errorf("episode: %w", err)
	}
	result.EpisodeUUID = ep.UUID

	// optional saga linkage (HAS_EPISODE + NEXT_EPISODE chain)
	if in.Saga != "" {
		sagaCreatedAt := ep.ValidAt
		if sagaCreatedAt == "" {
			sagaCreatedAt = ep.CreatedAt
		}
		saga, err := c.GetOrCreateSaga(in.Saga, gid, sagaCreatedAt)
		if err != nil {
			return result, fmt.Errorf("saga: %w", err)
		}
		if err := c.linkEpisodeToSaga(saga.UUID, ep.UUID, gid); err != nil {
			return result, err
		}
	}

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

	// RELATES_TO edges (source/target resolved by name)
	for _, ei := range in.Edges {
		var wr EdgeWriteResult
		if ei.DuplicateOf != "" {
			merged, err := c.MergeEdgeEpisode(ei.DuplicateOf, ep.UUID)
			if err != nil {
				return result, fmt.Errorf("edge %q: duplicate_of %s: %w", ei.Name, ei.DuplicateOf, err)
			}
			wr = EdgeWriteResult{EdgeUUID: merged.UUID, Merged: true}
		} else {
			for _, inv := range ei.Invalidate {
				if _, err := c.InvalidateEdge(inv, ""); err != nil {
					return result, fmt.Errorf("edge %q: invalidate %s: %w", ei.Name, inv, err)
				}
			}
			srcUUID, ok := result.Entities[ei.Source]
			if !ok {
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
				ValidAt: ei.ValidAt, ExpiredAt: ei.ExpiredAt, Episodes: []string{ep.UUID},
			}, in.Lenient)
			if err != nil {
				return result, fmt.Errorf("edge %q: %w", ei.Name, err)
			}
			wr = EdgeWriteResult{EdgeUUID: edge.UUID}
		}
		result.EdgeUUIDs = append(result.EdgeUUIDs, wr.EdgeUUID)
		result.Edges = append(result.Edges, wr)
	}

	// write back episode.entity_edges
	if len(result.EdgeUUIDs) > 0 {
		if _, err := c.graph.Query(`MATCH (ep:Episodic {uuid: $uuid}) SET ep.entity_edges = $ee`,
			map[string]any{"uuid": ep.UUID, "ee": result.EdgeUUIDs}, nil); err != nil {
			return result, fmt.Errorf("writeback entity_edges: %w", err)
		}
	}
	return result, nil
}

type TripletInput struct {
	Source      string   `json:"source"`
	Name        string   `json:"name"`
	Fact        string   `json:"fact"`
	Target      string   `json:"target"`
	GroupID     string   `json:"group_id,omitempty"`
	ValidAt     string   `json:"valid_at,omitempty"`
	DuplicateOf string   `json:"duplicate_of,omitempty"` // merge into this edge instead of creating
	Invalidate  []string `json:"invalidate,omitempty"`   // edge uuids to invalidate before writing
	EpisodeUUID string   `json:"episode_uuid,omitempty"` // attribution; also fills Episodes on normal writes
	Lenient     bool     `json:"-"`
}

type TripletResult struct {
	SourceUUID string          `json:"source_uuid"`
	TargetUUID string          `json:"target_uuid"`
	EdgeUUID   string          `json:"edge_uuid"`
	Merged     bool            `json:"merged"`
	Candidates []EdgeCandidate `json:"candidates,omitempty"`
}

// AddTriplet writes a single fact triplet (aligns with graphiti add_triplet):
// entities deduped by name, edge created fresh.
// dryRun=true runs candidate detection only, zero writes.
func (c *Client) AddTriplet(in *TripletInput, dryRun bool) (*TripletResult, error) {
	gid := c.GroupID(in.GroupID)
	res := &TripletResult{}

	if dryRun {
		src, _ := c.entityUUIDByName(in.Source, gid)
		tgt, _ := c.entityUUIDByName(in.Target, gid)
		if src != "" && tgt != "" && in.DuplicateOf == "" {
			if cands, err := c.FindEdgeCandidates(src, tgt, in.Name); err == nil {
				for _, cand := range cands {
					res.Candidates = append(res.Candidates, edgeCandidate(cand))
				}
			}
		}
		return res, nil
	}

	src, _, err := c.UpsertEntity(&Entity{Name: in.Source, GroupID: gid}, in.Lenient)
	if err != nil {
		return nil, fmt.Errorf("source entity: %w", err)
	}
	tgt, _, err := c.UpsertEntity(&Entity{Name: in.Target, GroupID: gid}, in.Lenient)
	if err != nil {
		return nil, fmt.Errorf("target entity: %w", err)
	}
	res.SourceUUID, res.TargetUUID = src.UUID, tgt.UUID

	if in.DuplicateOf != "" {
		merged, err := c.MergeEdgeEpisode(in.DuplicateOf, in.EpisodeUUID)
		if err != nil {
			return nil, fmt.Errorf("duplicate_of %s: %w", in.DuplicateOf, err)
		}
		res.EdgeUUID = merged.UUID
		res.Merged = true
		return res, nil
	}
	for _, inv := range in.Invalidate {
		if _, err := c.InvalidateEdge(inv, ""); err != nil {
			return nil, fmt.Errorf("invalidate %s: %w", inv, err)
		}
	}
	var episodes []string
	if in.EpisodeUUID != "" {
		episodes = []string{in.EpisodeUUID}
	}
	edge, err := c.UpsertEdge(&Edge{
		Name: in.Name, Fact: in.Fact, GroupID: gid,
		SourceUUID: src.UUID, TargetUUID: tgt.UUID, ValidAt: in.ValidAt,
		Episodes: episodes,
	}, in.Lenient)
	if err != nil {
		return nil, err
	}
	res.EdgeUUID = edge.UUID
	return res, nil
}

// entityUUIDByName returns the uuid of an existing entity, or "" if not found.
func (c *Client) entityUUIDByName(name, gid string) (string, error) {
	res, err := c.graph.ROQuery(`MATCH (n:Entity {name: $name, group_id: $gid}) RETURN n.uuid LIMIT 1`,
		map[string]any{"name": name, "gid": gid}, nil)
	if err != nil || !res.Next() {
		return "", err
	}
	v, _ := res.Record().GetByIndex(0)
	s, _ := v.(string)
	return s, nil
}
