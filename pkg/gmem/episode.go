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
		UUID:              strVal(p["uuid"]),
		Name:              strVal(p["name"]),
		GroupID:           strVal(p["group_id"]),
		Content:           strVal(p["content"]),
		Source:            strVal(p["source"]),
		SourceDescription: strVal(p["source_description"]),
		Metadata:          jsonToMap(strVal(p["episode_metadata"])),
		EntityEdges:       strSlice(p["entity_edges"]),
		CreatedAt:         strVal(p["created_at"]),
		ValidAt:           strVal(p["valid_at"]),
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
	val, err := res.Record().GetByIndex(0)
	if err != nil {
		return nil, err
	}
	n, ok := val.(*falkordb.Node)
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
		val, err := res.Record().GetByIndex(0)
		if err != nil {
			continue
		}
		if n, ok := val.(*falkordb.Node); ok {
			out = append(out, episodeFromNode(n))
		}
	}
	return out, nil
}
