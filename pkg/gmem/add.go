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

// Add writes a complete memory in one call: episode + entities + MENTIONS + RELATES_TO.
// Not transactional; upserts are idempotent so a failed retry is safe.
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

	// RELATES_TO edges (source/target resolved by name)
	for _, ei := range in.Edges {
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
			ValidAt: ei.ValidAt, Episodes: []string{ep.UUID},
		}, in.Lenient)
		if err != nil {
			return result, fmt.Errorf("edge %q: %w", ei.Name, err)
		}
		result.EdgeUUIDs = append(result.EdgeUUIDs, edge.UUID)
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

type TripletResult struct {
	SourceUUID string `json:"source_uuid"`
	TargetUUID string `json:"target_uuid"`
	EdgeUUID   string `json:"edge_uuid"`
}

// AddTriplet writes a single fact triplet (aligns with graphiti add_triplet):
// entities deduped by name, edge created fresh.
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
