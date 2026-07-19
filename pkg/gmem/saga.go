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

// GetOrCreateSaga returns the existing saga by (name, group_id), or creates one.
// Aligns with graphiti's _get_or_create_saga. createdAt, if empty, defaults to now.
func (c *Client) GetOrCreateSaga(name, groupID, createdAt string) (*Saga, error) {
	if name == "" {
		return nil, fmt.Errorf("saga name required")
	}
	gid := c.GroupID(groupID)
	if createdAt == "" {
		createdAt = nowUTC()
	}
	res, err := c.graph.ROQuery(`MATCH (n:Saga {name: $name, group_id: $gid}) RETURN n LIMIT 1`,
		map[string]any{"name": name, "gid": gid}, nil)
	if err != nil {
		return nil, err
	}
	if res.Next() {
		if val, err := res.Record().GetByIndex(0); err == nil {
			if n, ok := val.(*falkordb.Node); ok {
				return sagaFromNode(n), nil
			}
		}
	}
	return c.CreateSaga(&Saga{Name: name, GroupID: gid, CreatedAt: createdAt})
}

// previousEpisodeUUID returns the saga's most recent episode UUID excluding the
// current one, ordered by valid_at DESC then created_at DESC — aligns with
// graphiti's _saga_get_previous_episode_uuid. Empty if the saga has no prior episode.
func (c *Client) previousEpisodeUUID(sagaUUID, currentEpisodeUUID string) (string, error) {
	res, err := c.graph.ROQuery(`MATCH (s:Saga {uuid: $saga})-[:HAS_EPISODE]->(e:Episodic)
		WHERE e.uuid <> $cur RETURN e.uuid AS uuid ORDER BY e.valid_at DESC, e.created_at DESC LIMIT 1`,
		map[string]any{"saga": sagaUUID, "cur": currentEpisodeUUID}, nil)
	if err != nil {
		return "", err
	}
	if !res.Next() {
		return "", nil
	}
	v, err := res.Record().GetByIndex(0)
	if err != nil {
		return "", err
	}
	return strVal(v), nil
}

// linkEpisodeToSaga creates the HAS_EPISODE (saga→episode) and, if the saga
// already had a prior episode, the NEXT_EPISODE (prev→episode) edge, then
// updates the saga's first/last_episode_uuid. Aligns with graphiti's episode
// saga bookkeeping. No-op if sagaUUID is empty.
func (c *Client) linkEpisodeToSaga(sagaUUID, episodeUUID, groupID string) error {
	if sagaUUID == "" {
		return nil
	}
	gid := c.GroupID(groupID)
	ts := nowUTC()
	// HAS_EPISODE: saga -> episode
	if _, err := c.graph.Query(`MATCH (s:Saga {uuid: $s}), (e:Episodic {uuid: $ep})
		MERGE (s)-[r:HAS_EPISODE {uuid: $ru}]->(e)
		SET r.group_id = $gid, r.created_at = $ts`,
		map[string]any{"s": sagaUUID, "ep": episodeUUID, "ru": newUUID(), "gid": gid, "ts": ts}, nil); err != nil {
		return fmt.Errorf("has_episode: %w", err)
	}
	// NEXT_EPISODE: previous episode -> this episode
	prev, err := c.previousEpisodeUUID(sagaUUID, episodeUUID)
	if err != nil {
		return fmt.Errorf("previous episode: %w", err)
	}
	if prev != "" {
		if _, err := c.graph.Query(`MATCH (p:Episodic {uuid: $p}), (e:Episodic {uuid: $ep})
			MERGE (p)-[r:NEXT_EPISODE {uuid: $ru}]->(e)
			SET r.group_id = $gid, r.created_at = $ts`,
			map[string]any{"p": prev, "ep": episodeUUID, "ru": newUUID(), "gid": gid, "ts": ts}, nil); err != nil {
			return fmt.Errorf("next_episode: %w", err)
		}
	}
	// update first/last_episode_uuid on the saga
	if _, err := c.graph.Query(`MATCH (s:Saga {uuid: $s})
		SET s.last_episode_uuid = $ep,
			s.first_episode_uuid = CASE WHEN s.first_episode_uuid IS NULL OR s.first_episode_uuid = '' THEN $ep ELSE s.first_episode_uuid END`,
		map[string]any{"s": sagaUUID, "ep": episodeUUID}, nil); err != nil {
		return fmt.Errorf("saga episode watermark: %w", err)
	}
	return nil
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
		UUID:                            strVal(p["uuid"]),
		Name:                            strVal(p["name"]),
		GroupID:                         strVal(p["group_id"]),
		Summary:                         strVal(p["summary"]),
		FirstEpisodeUUID:                strVal(p["first_episode_uuid"]),
		LastEpisodeUUID:                 strVal(p["last_episode_uuid"]),
		LastSummarizedAt:                strVal(p["last_summarized_at"]),
		LastSummarizedEpisodeValidAt:    strVal(p["last_summarized_episode_valid_at"]),
		CreatedAt:                       strVal(p["created_at"]),
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
