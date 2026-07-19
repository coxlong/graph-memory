package gmem

import (
	"fmt"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

// Community is a cluster of entities with an agent-written summary.
type Community struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	GroupID     string   `json:"group_id"`
	Summary     string   `json:"summary,omitempty"`
	MemberUUIDs []string `json:"member_uuids,omitempty"`
	CreatedAt   string   `json:"created_at"`
}

// firstNonEntityLabel returns the first label that is not "Entity", else "".
func firstNonEntityLabel(labels []string) string {
	for _, l := range labels {
		if l != "Entity" && l != "" {
			return l
		}
	}
	return ""
}

// BuildCommunities groups entities by their primary type as candidate
// communities. The calling agent reviews these and writes the final summary
// back via UpsertCommunity (label propagation is delegated to the agent).
func (c *Client) BuildCommunities(groupID string) ([]Community, error) {
	gid := c.GroupID(groupID)
	res, err := c.graph.ROQuery(`MATCH (n:Entity {group_id: $gid})
		RETURN n.uuid AS uuid, n.name AS name, labels(n) AS labels`,
		map[string]any{"gid": gid}, nil)
	if err != nil {
		return nil, err
	}
	type ent struct {
		uuid   string
		labels []string
	}
	byType := map[string][]ent{}
	order := []string{}
	for res.Next() {
		u, _ := res.Record().GetByIndex(0)
		l, _ := res.Record().GetByIndex(2)
		uuid, _ := u.(string)
		labels := strSlice(l)
		pt := firstNonEntityLabel(labels)
		if pt == "" {
			pt = "Entity"
		}
		if _, ok := byType[pt]; !ok {
			order = append(order, pt)
		}
		byType[pt] = append(byType[pt], ent{uuid: uuid, labels: labels})
	}
	out := []Community{}
	for _, pt := range order {
		members := byType[pt]
		uuids := make([]string, len(members))
		for i, m := range members {
			uuids[i] = m.uuid
		}
		example := ""
		if len(members) > 0 {
			example = members[0].uuid
		}
		out = append(out, Community{
			Name:        pt,
			GroupID:     gid,
			MemberUUIDs: uuids,
			Summary:     fmt.Sprintf("Candidate community of %d %s entities (e.g. %s)", len(members), pt, example),
		})
	}
	return out, nil
}

// UpsertCommunity MERGEs a Community by name and rewires HAS_MEMBER edges.
func (c *Client) UpsertCommunity(name, summary string, memberUUIDs []string, groupID string) (*Community, error) {
	if name == "" {
		return nil, fmt.Errorf("community name required")
	}
	gid := c.GroupID(groupID)
	// find or create community node
	var comUUID string
	res, err := c.graph.ROQuery(`MATCH (n:Community {name: $name, group_id: $gid}) RETURN n.uuid LIMIT 1`,
		map[string]any{"name": name, "gid": gid}, nil)
	if err != nil {
		return nil, err
	}
	if res.Next() {
		if u, err := res.Record().GetByIndex(0); err == nil {
			if s, ok := u.(string); ok {
				comUUID = s
			}
		}
	}
	if comUUID == "" {
		comUUID = newUUID()
		_, err := c.graph.Query(`CREATE (n:Community {
			uuid: $uuid, name: $name, group_id: $gid, summary: $summary, created_at: $created_at
		})`, map[string]any{
			"uuid": comUUID, "name": name, "gid": gid,
			"summary": summary, "created_at": nowUTC(),
		}, nil)
		if err != nil {
			return nil, err
		}
	} else {
		if _, err := c.graph.Query(`MATCH (n:Community {uuid: $uuid}) SET n.summary = $summary`,
			map[string]any{"uuid": comUUID, "summary": summary}, nil); err != nil {
			return nil, err
		}
	}
	// rewire HAS_MEMBER: drop old, add new
	if _, err := c.graph.Query(`MATCH (c:Community {uuid: $uuid})-[r:HAS_MEMBER]->() DELETE r`,
		map[string]any{"uuid": comUUID}, nil); err != nil {
		return nil, err
	}
	for _, mu := range memberUUIDs {
		if _, err := c.graph.Query(`MATCH (c:Community {uuid: $cu}), (e:Entity {uuid: $eu})
			MERGE (c)-[:HAS_MEMBER {uuid: $ru, group_id: $gid, created_at: $ts}]->(e)`,
			map[string]any{"cu": comUUID, "eu": mu, "ru": newUUID(), "gid": gid, "ts": nowUTC()}, nil); err != nil {
			return nil, err
		}
	}
	return c.GetCommunity(comUUID)
}

func (c *Client) GetCommunity(uuid string) (*Community, error) {
	res, err := c.graph.ROQuery(`MATCH (n:Community {uuid: $uuid}) RETURN n`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return nil, err
	}
	if !res.Next() {
		return nil, fmt.Errorf("community %s not found", uuid)
	}
	val, err := res.Record().GetByIndex(0)
	if err != nil {
		return nil, err
	}
	n, ok := val.(*falkordb.Node)
	if !ok {
		return nil, fmt.Errorf("unexpected record type")
	}
	com := &Community{
		UUID:    strVal(n.Properties["uuid"]),
		Name:    strVal(n.Properties["name"]),
		GroupID: strVal(n.Properties["group_id"]),
		Summary: strVal(n.Properties["summary"]),
	}
	// fetch members
	mres, err := c.graph.ROQuery(`MATCH (c:Community {uuid: $uuid})-[r:HAS_MEMBER]->(e:Entity) RETURN e.uuid`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return nil, err
	}
	for mres.Next() {
		if u, err := mres.Record().GetByIndex(0); err == nil {
			if s, ok := u.(string); ok {
				com.MemberUUIDs = append(com.MemberUUIDs, s)
			}
		}
	}
	return com, nil
}
