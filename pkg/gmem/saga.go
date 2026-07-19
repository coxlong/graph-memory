package gmem

import (
	"fmt"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

// Saga is an incremental summarization watermark: tracks which episode range
// has already been summarized so future summarization resumes from the watermark.
type Saga struct {
	UUID                            string `json:"uuid"`
	Name                            string `json:"name"`
	GroupID                         string `json:"group_id"`
	Summary                         string `json:"summary,omitempty"`
	FirstEpisodeUUID                string `json:"first_episode_uuid,omitempty"`
	LastEpisodeUUID                 string `json:"last_episode_uuid,omitempty"`
	LastSummarizedAt                string `json:"last_summarized_at,omitempty"`
	LastSummarizedEpisodeValidAt    string `json:"last_summarized_episode_valid_at,omitempty"`
	CreatedAt                       string `json:"created_at"`
}

func (c *Client) CreateSaga(s *Saga) (*Saga, error) {
	if s.Name == "" {
		return nil, fmt.Errorf("saga name required")
	}
	if s.UUID == "" {
		s.UUID = newUUID()
	}
	s.GroupID = c.GroupID(s.GroupID)
	if s.CreatedAt == "" {
		s.CreatedAt = nowUTC()
	}
	var err error
	if s.LastSummarizedEpisodeValidAt, err = normalizeTime(s.LastSummarizedEpisodeValidAt); err != nil {
		return nil, err
	}
	_, err = c.graph.Query(`CREATE (n:Saga {
		uuid: $uuid, name: $name, group_id: $gid, summary: $summary,
		first_episode_uuid: $first, last_episode_uuid: $last,
		last_summarized_at: $lsa, last_summarized_episode_valid_at: $lseva,
		created_at: $created_at
	})`, map[string]any{
		"uuid": s.UUID, "name": s.Name, "gid": s.GroupID, "summary": s.Summary,
		"first": s.FirstEpisodeUUID, "last": s.LastEpisodeUUID,
		"lsa": s.LastSummarizedAt, "lseva": s.LastSummarizedEpisodeValidAt,
		"created_at": s.CreatedAt,
	}, nil)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func sagaFromNode(n *falkordb.Node) *Saga {
	p := n.Properties
	return &Saga{
		UUID:                            fmt.Sprint(p["uuid"]),
		Name:                            fmt.Sprint(p["name"]),
		GroupID:                         fmt.Sprint(p["group_id"]),
		Summary:                         fmt.Sprint(p["summary"]),
		FirstEpisodeUUID:                fmt.Sprint(p["first_episode_uuid"]),
		LastEpisodeUUID:                 fmt.Sprint(p["last_episode_uuid"]),
		LastSummarizedAt:                fmt.Sprint(p["last_summarized_at"]),
		LastSummarizedEpisodeValidAt:    fmt.Sprint(p["last_summarized_episode_valid_at"]),
		CreatedAt:                       fmt.Sprint(p["created_at"]),
	}
}

func (c *Client) GetSaga(uuid string) (*Saga, error) {
	res, err := c.graph.ROQuery(`MATCH (n:Saga {uuid: $uuid}) RETURN n`,
		map[string]any{"uuid": uuid}, nil)
	if err != nil {
		return nil, err
	}
	if !res.Next() {
		return nil, fmt.Errorf("saga %s not found", uuid)
	}
	val, err := res.Record().GetByIndex(0)
	if err != nil {
		return nil, err
	}
	n, ok := val.(*falkordb.Node)
	if !ok {
		return nil, fmt.Errorf("unexpected record type")
	}
	return sagaFromNode(n), nil
}

// UpdateSaga advances the watermark: summary + last episode + timestamps.
// Empty strings leave the existing value unchanged.
func (c *Client) UpdateSaga(uuid, summary, lastEpisodeUUID, lastSummarizedAt, lastSummarizedEpisodeValidAt string) (*Saga, error) {
	existing, err := c.GetSaga(uuid)
	if err != nil {
		return nil, err
	}
	if summary == "" {
		summary = existing.Summary
	}
	if lastEpisodeUUID == "" {
		lastEpisodeUUID = existing.LastEpisodeUUID
	}
	if lastSummarizedAt == "" {
		lastSummarizedAt = existing.LastSummarizedAt
	}
	if lastSummarizedEpisodeValidAt, err = normalizeTime(lastSummarizedEpisodeValidAt); err != nil {
		return nil, err
	}
	if lastSummarizedEpisodeValidAt == "" {
		// keep existing if not provided
		lastSummarizedEpisodeValidAt = existing.LastSummarizedEpisodeValidAt
	}
	if _, err := c.graph.Query(`MATCH (n:Saga {uuid: $uuid})
		SET n.summary = $summary, n.last_episode_uuid = $last,
			n.last_summarized_at = $lsa, n.last_summarized_episode_valid_at = $lseva`,
		map[string]any{
			"uuid": uuid, "summary": summary, "last": lastEpisodeUUID,
			"lsa": lastSummarizedAt, "lseva": lastSummarizedEpisodeValidAt,
		}, nil); err != nil {
		return nil, err
	}
	return c.GetSaga(uuid)
}
