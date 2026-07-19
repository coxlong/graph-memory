package gmem

import (
	"sort"
	"strings"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

type EntityWithScore struct {
	Entity
	Score float64 `json:"score"`
}

type EdgeWithScore struct {
	Edge
	Score float64 `json:"score"`
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

// entityTypePredicate returns a Cypher predicate matching an entity's labels
// against the given type names. Empty types disables filtering ("true").
func entityTypePredicate(types []string) string {
	if len(types) == 0 {
		return "true"
	}
	return "any(t IN $types WHERE t IN labels(n))"
}

// SearchEntities vector ∪ fulltext retrieval of entities, optionally narrowed
// to the given entity type names (node labels excluding the base "Entity").
func (c *Client) SearchEntities(query string, limit int, types []string) ([]EntityWithScore, error) {
	vec, err := c.Embed.Embed(query)
	if err != nil {
		return nil, err
	}
	byID := map[string]EntityWithScore{}
	ranks := [][]string{}
	typePred := entityTypePredicate(types)

	// vector path
	params := map[string]any{"vec": vecParam(vec), "limit": limit}
	if len(types) > 0 {
		params["types"] = types
	}
	res, err := c.graph.ROQuery(`MATCH (n:Entity)
		WHERE n.name_embedding IS NOT NULL AND `+typePred+`
		WITH n, (2 - vec.cosineDistance(n.name_embedding, vecf32($vec)))/2 AS score
		WHERE score > 0.3
		RETURN n, score ORDER BY score DESC LIMIT $limit`,
		params, nil)
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
	fulltextParams := map[string]any{"q": escapeFTQuery(query), "limit": limit}
	if len(types) > 0 {
		fulltextParams["types"] = types
	}
	res, err = c.graph.ROQuery(`CALL db.idx.fulltext.queryNodes('Entity', $q) YIELD node, score
		WITH node AS n, score WHERE `+typePred+`
		RETURN n, score LIMIT $limit`,
		fulltextParams, nil)
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

// edgeTemporalFilter returns the temporal WHERE clause and params.
// At a point in time T, an edge is "active" if valid_at <= T and neither
// invalid_at nor expired_at has occurred by T (absent, empty, or > T) —
// aligned with graphiti's valid_at/invalid_at/expired_at semantics.
// With no as-of, "now" is used (so future expirations remain active).
func edgeTemporalFilter(asOf string, includeInvalid bool) (string, map[string]any) {
	if includeInvalid {
		return "true", map[string]any{}
	}
	if asOf == "" {
		asOf = nowUTC()
	}
	return "r.valid_at <= $asOf AND " +
			"(r.invalid_at IS NULL OR r.invalid_at = '' OR r.invalid_at > $asOf) AND " +
			"(r.expired_at IS NULL OR r.expired_at = '' OR r.expired_at > $asOf)",
		map[string]any{"asOf": asOf}
}

// edgeTypePredicate returns a Cypher predicate matching an edge's semantic
// type (the "name" property on RELATES_TO) against the given type names.
// Empty types disables filtering ("true").
func edgeTypePredicate(types []string) string {
	if len(types) == 0 {
		return "true"
	}
	return "r.name IN $types"
}

// SearchEdges vector ∪ fulltext retrieval of RELATES_TO edges with temporal
// and type filtering. asOf (RFC3339) constrains to a point in time; types
// narrows to the given semantic edge types (r.name).
func (c *Client) SearchEdges(query string, limit int, asOf string, types []string, includeInvalid bool) ([]EdgeWithScore, error) {
	if asOf != "" {
		var err error
		if asOf, err = normalizeTime(asOf); err != nil {
			return nil, err
		}
	}
	return c.searchEdgesFiltered(query, limit, asOf, types, includeInvalid)
}

func (c *Client) searchEdgesFiltered(query string, limit int, asOf string, types []string, includeInvalid bool) ([]EdgeWithScore, error) {
	vec, err := c.Embed.Embed(query)
	if err != nil {
		return nil, err
	}
	filter, fparams := edgeTemporalFilter(asOf, includeInvalid)
	typePred := edgeTypePredicate(types)
	byID := map[string]EdgeWithScore{}
	ranks := [][]string{}

	// vector path
	params := map[string]any{"vec": vecParam(vec), "limit": limit}
	for k, v := range fparams {
		params[k] = v
	}
	if len(types) > 0 {
		params["types"] = types
	}
	res, err := c.graph.ROQuery(`MATCH (s:Entity)-[r:RELATES_TO]->(t)
		WHERE r.fact_embedding IS NOT NULL AND `+filter+` AND `+typePred+`
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
	fulltextParams := map[string]any{"q": escapeFTQuery(query), "limit": limit, "asOf": asOf}
	if len(types) > 0 {
		fulltextParams["types"] = types
	}
	res, err = c.graph.ROQuery(`CALL db.idx.fulltext.queryRelationships('RELATES_TO', $q) YIELD relationship, score
		WITH relationship AS r, score WHERE `+filter+` AND `+typePred+`
		MATCH (s:Entity)-[r]->(t)
		RETURN r, s.uuid, t.uuid, score LIMIT $limit`,
		fulltextParams, nil)
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
