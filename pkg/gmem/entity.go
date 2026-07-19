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

// labelSet returns a de-duplicated label string ensuring Entity is present
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

// vecParam converts a float32 embedding to []interface{} so the falkordb-go
// param header (which only serializes []interface{}) can carry it as a vector.
func vecParam(f []float32) []interface{} {
	out := make([]interface{}, len(f))
	for i, v := range f {
		out[i] = float64(v)
	}
	return out
}

// UpsertEntity MERGEs by (name, group_id); returns entity and whether it was newly created
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
	// try find existing entity
	res, err := c.graph.ROQuery(`MATCH (n:Entity {name: $name, group_id: $gid}) RETURN n LIMIT 1`,
		map[string]any{"name": e.Name, "gid": gid}, nil)
	if err != nil {
		return nil, false, err
	}
	if res.Next() {
		val, err := res.Record().GetByIndex(0)
		if err != nil {
			return nil, false, err
		}
		n, ok := val.(*falkordb.Node)
		if !ok {
			return nil, false, fmt.Errorf("unexpected record type")
		}
		return entityFromNode(n), false, nil
	}
	// create new
	_, err = c.graph.Query(`CREATE (n:Entity {
		uuid: $uuid, name: $name, group_id: $gid, summary: $summary,
		attributes: $attrs, created_at: $created_at
	}) SET n:`+labelSet(e.Labels)+` SET n.name_embedding = vecf32($emb)`,
		map[string]any{
			"uuid": e.UUID, "name": e.Name, "gid": gid, "summary": e.Summary,
			"attrs": attrs, "created_at": nowUTC(), "emb": vecParam(emb),
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
	val, err := res.Record().GetByIndex(0)
	if err != nil {
		return nil, err
	}
	n, ok := val.(*falkordb.Node)
	if !ok {
		return nil, fmt.Errorf("unexpected record type")
	}
	return entityFromNode(n), nil
}

// UpdateEntity updates entity; empty name/summary means unchanged; attrs default-merged unless replace
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
		params["emb"] = vecParam(emb)
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

// MergeEntities rewires from's edges to to, merges attributes (to wins) and labels (union), deletes from
func (c *Client) MergeEntities(fromUUID, toUUID string) (*Entity, error) {
	from, err := c.GetEntity(fromUUID)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	to, err := c.GetEntity(toUUID)
	if err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	// rewire outgoing edges
	if _, err := c.graph.Query(`MATCH (a:Entity {uuid: $from})-[r:RELATES_TO]->(m)
		MATCH (b:Entity {uuid: $to})
		CREATE (b)-[nr:RELATES_TO]->(m) SET nr = properties(r)`,
		map[string]any{"from": fromUUID, "to": toUUID}, nil); err != nil {
		return nil, err
	}
	// rewire incoming edges
	if _, err := c.graph.Query(`MATCH (m)-[r:RELATES_TO]->(a:Entity {uuid: $from})
		MATCH (b:Entity {uuid: $to})
		CREATE (m)-[nr:RELATES_TO]->(b) SET nr = properties(r)`,
		map[string]any{"from": fromUUID, "to": toUUID}, nil); err != nil {
		return nil, err
	}
	// rewire MENTIONS
	if _, err := c.graph.Query(`MATCH (ep:Episodic)-[r:MENTIONS]->(a:Entity {uuid: $from})
		MATCH (b:Entity {uuid: $to})
		MERGE (ep)-[:MENTIONS]->(b)`,
		map[string]any{"from": fromUUID, "to": toUUID}, nil); err != nil {
		return nil, err
	}
	// merge attributes (to wins) and labels (union)
	mergedAttrs := from.Attributes
	for k, v := range to.Attributes {
		mergedAttrs[k] = v
	}
	labelUnion := to.Labels
	for _, l := range from.Labels {
		if !contains(labelUnion, l) {
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
	// delete from (with its edges)
	if _, err := c.graph.Query(`MATCH (a:Entity {uuid: $from}) DETACH DELETE a`,
		map[string]any{"from": fromUUID}, nil); err != nil {
		return nil, err
	}
	return c.GetEntity(toUUID)
}

// DeleteNode deletes any labeled node and cascades its edges
func (c *Client) DeleteNode(uuid string) error {
	res, err := c.graph.Query(`MATCH (n {uuid: $uuid}) DETACH DELETE n RETURN count(n) AS cnt`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return err
	}
	if res.Next() {
		cnt, _ := res.Record().GetByIndex(0)
		if n, ok := cnt.(int64); ok && n == 0 {
			return fmt.Errorf("node %s not found", uuid)
		}
	}
	return nil
}
