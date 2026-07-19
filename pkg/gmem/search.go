package gmem

import (
	"fmt"
	"sort"
	"strings"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

type SearchOpts struct {
	GroupID        string
	AsOf           string
	Limit          int
	IncludeInvalid bool
}

type EntityWithScore struct {
	Entity
	Score float64 `json:"score"`
}

type EdgeWithScore struct {
	Edge
	Score float64 `json:"score"`
}

type EpisodeWithScore struct {
	Episode
	Score float64 `json:"score"`
}

type SearchResult struct {
	Entities []EntityWithScore  `json:"entities"`
	Edges    []EdgeWithScore    `json:"edges"`
	Episodes []EpisodeWithScore `json:"episodes"`
}

// escapeFTQuery clears RedisSearch special characters
func escapeFTQuery(q string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`@{}()"|~*:-><[]`, r) {
			return ' '
		}
		return r
	}, q)
}

// rrfFuse fuses multi-path ranked lists, returns doc ids ordered by RRF score desc
func rrfFuse(rankLists [][]string, k float64) []string {
	scores := map[string]float64{}
	for _, list := range rankLists {
		for rank, id := range list {
			scores[id] += 1.0 / (k + float64(rank+1))
		}
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] == scores[ids[j]] {
			return ids[i] < ids[j]
		}
		return scores[ids[i]] > scores[ids[j]]
	})
	return ids
}

// SearchEntities vector ∪ fulltext retrieval of entities
func (c *Client) SearchEntities(query string, limit int) ([]EntityWithScore, error) {
	vec, err := c.Embed.Embed(query)
	if err != nil {
		return nil, err
	}
	byID := map[string]EntityWithScore{}
	ranks := [][]string{}

	// vector path
	res, err := c.graph.ROQuery(`MATCH (n:Entity)
		WHERE n.name_embedding IS NOT NULL
		WITH n, (2 - vec.cosineDistance(n.name_embedding, vecf32($vec)))/2 AS score
		WHERE score > 0.3
		RETURN n, score ORDER BY score DESC LIMIT $limit`,
		map[string]any{"vec": vecParam(vec), "limit": limit}, nil)
	if err != nil {
		return nil, err
	}
	vr := []string{}
	for res.Next() {
		nVal, err := res.Record().GetByIndex(0)
		if err != nil {
			continue
		}
		n, ok := nVal.(*falkordb.Node)
		if !ok {
			continue
		}
		e := entityFromNode(n)
		s, _ := res.Record().GetByIndex(1)
		score := toFloat(s)
		byID[e.UUID] = EntityWithScore{Entity: *e, Score: score}
		vr = append(vr, e.UUID)
	}
	ranks = append(ranks, vr)

	// fulltext path (tolerated if index procedure is unavailable)
	res, err = c.graph.ROQuery(`CALL db.idx.fulltext.queryNodes('Entity', $q) YIELD node, score
		RETURN node, score LIMIT $limit`,
		map[string]any{"q": escapeFTQuery(query), "limit": limit}, nil)
	if err == nil {
		fr := []string{}
		for res.Next() {
			nVal, err := res.Record().GetByIndex(0)
			if err != nil {
				continue
			}
			n, ok := nVal.(*falkordb.Node)
			if !ok {
				continue
			}
			e := entityFromNode(n)
			s, _ := res.Record().GetByIndex(1)
			score := toFloat(s)
			if _, seen := byID[e.UUID]; !seen {
				byID[e.UUID] = EntityWithScore{Entity: *e, Score: score}
			}
			fr = append(fr, e.UUID)
		}
		ranks = append(ranks, fr)
	}

	order := rrfFuse(ranks, 60)
	out := []EntityWithScore{}
	for i, id := range order {
		if i >= limit {
			break
		}
		out = append(out, byID[id])
	}
	return out, nil
}

// edgeTemporalFilter returns temporal WHERE clause and params.
// A valid edge is one whose invalid_at is absent (NULL) or empty string.
func edgeTemporalFilter(asOf string, includeInvalid bool) (string, map[string]any) {
	if asOf != "" {
		return "r.valid_at <= $asOf AND (r.invalid_at IS NULL OR r.invalid_at = '' OR r.invalid_at > $asOf)",
			map[string]any{"asOf": asOf}
	}
	if !includeInvalid {
		return "r.invalid_at IS NULL OR r.invalid_at = ''", map[string]any{}
	}
	return "true", map[string]any{}
}

// SearchEdges vector ∪ fulltext retrieval of RELATES_TO edges with temporal filtering
func (c *Client) SearchEdges(query string, limit int, includeInvalid bool) ([]EdgeWithScore, error) {
	return c.searchEdgesFiltered(query, limit, "", includeInvalid)
}

func (c *Client) searchEdgesFiltered(query string, limit int, asOf string, includeInvalid bool) ([]EdgeWithScore, error) {
	vec, err := c.Embed.Embed(query)
	if err != nil {
		return nil, err
	}
	filter, fparams := edgeTemporalFilter(asOf, includeInvalid)
	byID := map[string]EdgeWithScore{}
	ranks := [][]string{}

	// vector path
	params := map[string]any{"vec": vecParam(vec), "limit": limit}
	for k, v := range fparams {
		params[k] = v
	}
	res, err := c.graph.ROQuery(`MATCH (s:Entity)-[r:RELATES_TO]->(t)
		WHERE r.fact_embedding IS NOT NULL AND `+filter+`
		WITH r, s, t, (2 - vec.cosineDistance(r.fact_embedding, vecf32($vec)))/2 AS score
		WHERE score > 0.3
		RETURN r, s.uuid, t.uuid, score ORDER BY score DESC LIMIT $limit`, params, nil)
	if err != nil {
		return nil, err
	}
	vr := []string{}
	for res.Next() {
		e := edgeFromRecord(res.Record())
		if e == nil {
			continue
		}
		byID[e.UUID] = *e
		vr = append(vr, e.UUID)
	}
	ranks = append(ranks, vr)

	// fulltext path (tolerated if relationship index procedure is unavailable)
	res, err = c.graph.ROQuery(`CALL db.idx.fulltext.queryRelationships('RELATES_TO', $q) YIELD relationship, score
		WITH relationship AS r, score WHERE `+filter+`
		MATCH (s:Entity)-[r]->(t)
		RETURN r, s.uuid, t.uuid, score LIMIT $limit`,
		map[string]any{"q": escapeFTQuery(query), "limit": limit, "asOf": asOf}, nil)
	if err == nil {
		fr := []string{}
		for res.Next() {
			e := edgeFromRecord(res.Record())
			if e == nil {
				continue
			}
			if _, seen := byID[e.UUID]; !seen {
				byID[e.UUID] = *e
			}
			fr = append(fr, e.UUID)
		}
		ranks = append(ranks, fr)
	}

	order := rrfFuse(ranks, 60)
	out := []EdgeWithScore{}
	for i, id := range order {
		if i >= limit {
			break
		}
		out = append(out, byID[id])
	}
	return out, nil
}

// searchEpisodes fulltext retrieval of episodes (no vector; fulltext only)
func (c *Client) searchEpisodes(query string, limit int) ([]EpisodeWithScore, error) {
	res, err := c.graph.ROQuery(`CALL db.idx.fulltext.queryNodes('Episodic', $q) YIELD node, score
		RETURN node, score LIMIT $limit`,
		map[string]any{"q": escapeFTQuery(query), "limit": limit}, nil)
	if err != nil {
		return nil, err
	}
	out := []EpisodeWithScore{}
	for res.Next() {
		nVal, err := res.Record().GetByIndex(0)
		if err != nil {
			continue
		}
		n, ok := nVal.(*falkordb.Node)
		if !ok {
			continue
		}
		s, _ := res.Record().GetByIndex(1)
		out = append(out, EpisodeWithScore{Episode: *episodeFromNode(n), Score: toFloat(s)})
	}
	return out, nil
}

// toFloat coerces a FalkorDB numeric result to float64
func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	default:
		return 0
	}
}

// Search hybrid retrieval: entities + edges + episodes
func (c *Client) Search(query string, opts SearchOpts) (*SearchResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.AsOf != "" {
		var err error
		if opts.AsOf, err = normalizeTime(opts.AsOf); err != nil {
			return nil, err
		}
	}
	entities, err := c.SearchEntities(query, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("entities: %w", err)
	}
	edges, err := c.searchEdgesFiltered(query, opts.Limit, opts.AsOf, opts.IncludeInvalid)
	if err != nil {
		return nil, fmt.Errorf("edges: %w", err)
	}
	episodes, err := c.searchEpisodes(query, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("episodes: %w", err)
	}
	return &SearchResult{Entities: entities, Edges: edges, Episodes: episodes}, nil
}
