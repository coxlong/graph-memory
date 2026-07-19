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

// UpsertEdge MERGEs RELATES_TO by uuid; generates fact_embedding; validates endpoints
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
	if e.ExpiredAt, err = normalizeTime(e.ExpiredAt); err != nil {
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
			r.valid_at = $valid_at, r.invalid_at = $invalid_at, r.expired_at = $expired_at,
			r.attributes = $attrs, r.fact_embedding = vecf32($emb)`,
		map[string]any{
			"s": e.SourceUUID, "t": e.TargetUUID, "uuid": e.UUID, "name": e.Name,
			"gid": e.GroupID, "fact": e.Fact, "episodes": e.Episodes,
			"created_at": e.CreatedAt, "valid_at": e.ValidAt, "invalid_at": e.InvalidAt,
			"expired_at": e.ExpiredAt, "attrs": attrs, "emb": vecParam(emb),
		}, nil)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func edgeFromRel(r *falkordb.Edge) *Edge {
	p := r.Properties
	e := &Edge{
		UUID:       strVal(p["uuid"]),
		Name:       strVal(p["name"]),
		GroupID:    strVal(p["group_id"]),
		Fact:       strVal(p["fact"]),
		Episodes:   strSlice(p["episodes"]),
		CreatedAt:  strVal(p["created_at"]),
		ValidAt:    strVal(p["valid_at"]),
		InvalidAt:  strVal(p["invalid_at"]),
		ExpiredAt:  strVal(p["expired_at"]),
		Attributes: jsonToMap(strVal(p["attributes"])),
	}
	if r.Source != nil && r.Source.Properties != nil {
		e.SourceUUID = strVal(r.Source.Properties["uuid"])
	}
	if r.Destination != nil && r.Destination.Properties != nil {
		e.TargetUUID = strVal(r.Destination.Properties["uuid"])
	}
	return e
}

// edgeFromRecord extracts an edge with its score and endpoint uuids from a
// record laid out as [r, sourceUUID, targetUUID, score].
func edgeFromRecord(rec *falkordb.Record) *EdgeWithScore {
	if rec == nil {
		return nil
	}
	rVal, err := rec.GetByIndex(0)
	if err != nil {
		return nil
	}
	r, ok := rVal.(*falkordb.Edge)
	if !ok {
		return nil
	}
	e := edgeFromRel(r)
	if su, err := rec.GetByIndex(1); err == nil {
		if s, ok := su.(string); ok {
			e.SourceUUID = s
		}
	}
	if tu, err := rec.GetByIndex(2); err == nil {
		if t, ok := tu.(string); ok {
			e.TargetUUID = t
		}
	}
	sc, _ := rec.GetByIndex(3)
	return &EdgeWithScore{Edge: *e, Score: toFloat(sc)}
}

func (c *Client) GetEdge(uuid string) (*Edge, error) {
	res, err := c.graph.ROQuery(`MATCH (s:Entity)-[r:RELATES_TO {uuid: $uuid}]->(t:Entity)
		RETURN r, s.uuid AS suuid, t.uuid AS tuuid`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return nil, err
	}
	if !res.Next() {
		return nil, fmt.Errorf("edge %s not found", uuid)
	}
	rVal, err := res.Record().GetByIndex(0)
	if err != nil {
		return nil, err
	}
	r, ok := rVal.(*falkordb.Edge)
	if !ok {
		return nil, fmt.Errorf("unexpected record type")
	}
	e := edgeFromRel(r)
	su, _ := res.Record().GetByIndex(1)
	tu, _ := res.Record().GetByIndex(2)
	if s, ok := su.(string); ok {
		e.SourceUUID = s
	}
	if t, ok := tu.(string); ok {
		e.TargetUUID = t
	}
	return e, nil
}

// InvalidateEdge marks an edge invalid; invalidAt defaults to now
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
		cnt, _ := res.Record().GetByIndex(0)
		if n, ok := cnt.(int64); ok && n == 0 {
			return fmt.Errorf("edge %s not found", uuid)
		}
	}
	return nil
}
