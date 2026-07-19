package gmem

import (
	"testing"

	falkordb "github.com/FalkorDB/falkordb-go/v2"
)

func TestAddFullFlow(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	res, err := c.Add(&AddInput{
		Episode: &Episode{Content: "Alice joined TeamB", Source: "message"},
		Entities: []AddEntityInput{
			{Name: "AddAlice", Labels: []string{"Person"}, Summary: "engineer"},
			{Name: "TeamB"},
		},
		Edges: []AddEdgeInput{
			{Source: "AddAlice", Target: "TeamB", Name: "MEMBER_OF", Fact: "AddAlice joined TeamB", ValidAt: "2026-07-19T00:00:00Z"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.EpisodeUUID == "" || len(res.Entities) != 2 || len(res.EdgeUUIDs) != 1 {
		t.Fatalf("bad result: %+v", res)
	}
	// MENTIONS edges created
	qr, _ := c.graph.ROQuery(`MATCH (ep:Episodic {uuid:$u})-[:MENTIONS]->(e) RETURN count(e)`,
		map[string]any{"u": res.EpisodeUUID}, nil)
	qr.Next()
	cnt, _ := qr.Record().GetByIndex(0)
	if n, ok := cnt.(int64); !ok || n != 2 {
		t.Fatalf("mentions: %v", cnt)
	}
	// RELATES_TO episodes written back
	edge, _ := c.GetEdge(res.EdgeUUIDs[0])
	if len(edge.Episodes) != 1 || edge.Episodes[0] != res.EpisodeUUID {
		t.Fatalf("edge episodes: %v", edge.Episodes)
	}
	// episode.entity_edges written back
	ep, _ := c.GetEpisode(res.EpisodeUUID)
	if len(ep.EntityEdges) != 1 {
		t.Fatalf("episode entity_edges: %v", ep.EntityEdges)
	}
}

func TestAddIdempotentRetry(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	in := &AddInput{
		Episode:  &Episode{Content: "retry test", Source: "text"},
		Entities: []AddEntityInput{{Name: "RetryAlice"}},
	}
	r1, err := c.Add(in)
	if err != nil {
		t.Fatal(err)
	}
	// second add with same entity name should dedup (no error, same uuid)
	r2, err := c.Add(in)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Entities["RetryAlice"] != r2.Entities["RetryAlice"] {
		t.Fatalf("retry not idempotent: %v vs %v", r1.Entities, r2.Entities)
	}
}

func TestAddTriplet(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	r, err := c.AddTriplet("TriAlice", "WORKS_ON", "TriAlice works on gmem", "gmem", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceUUID == "" || r.TargetUUID == "" || r.EdgeUUID == "" {
		t.Fatalf("bad result: %+v", r)
	}
	// repeat: entities dedup, edge uuid changes (new edge each call)
	r2, err := c.AddTriplet("TriAlice", "WORKS_ON", "TriAlice works on gmem", "gmem", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceUUID != r2.SourceUUID || r.TargetUUID != r2.TargetUUID {
		t.Fatalf("entities should dedup: %+v vs %+v", r, r2)
	}
}

func TestAddSagaLinkage(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	// first episode under saga "proj-x"
	r1, err := c.Add(&AddInput{
		Episode: &Episode{Content: "ep1", Source: "text", ValidAt: "2026-07-01T00:00:00Z"},
		Saga:    "proj-x",
	})
	if err != nil {
		t.Fatal(err)
	}
	// second episode under the same saga
	r2, err := c.Add(&AddInput{
		Episode: &Episode{Content: "ep2", Source: "text", ValidAt: "2026-07-02T00:00:00Z"},
		Saga:    "proj-x",
	})
	if err != nil {
		t.Fatal(err)
	}

	// saga created once (same name -> same saga)
	sagas := sagaList(t, c)
	if len(sagas) != 1 {
		t.Fatalf("want 1 saga, got %d", len(sagas))
	}
	sg := sagas[0]
	if sg.FirstEpisodeUUID != r1.EpisodeUUID || sg.LastEpisodeUUID != r2.EpisodeUUID {
		t.Fatalf("saga first/last: %s/%s want %s/%s", sg.FirstEpisodeUUID, sg.LastEpisodeUUID, r1.EpisodeUUID, r2.EpisodeUUID)
	}
	// HAS_EPISODE: saga -> both episodes
	hasEp := countEdges(t, c, `MATCH (:Saga {uuid:$s})-[:HAS_EPISODE]->(:Episodic) RETURN count(*)`, sg.UUID)
	if hasEp != 2 {
		t.Fatalf("HAS_EPISODE count: %d", hasEp)
	}
	// NEXT_EPISODE: ep1 -> ep2 (one chain link)
	nextEp := countEdges(t, c, `MATCH (:Episodic)-[r:NEXT_EPISODE]->(:Episodic) RETURN count(r)`, "")
	if nextEp != 1 {
		t.Fatalf("NEXT_EPISODE count: %d", nextEp)
	}
}

func sagaList(t *testing.T, c *Client) []*Saga {
	t.Helper()
	res, err := c.graph.ROQuery(`MATCH (n:Saga) RETURN n`, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := []*Saga{}
	for res.Next() {
		v, err := res.Record().GetByIndex(0)
		if err != nil {
			t.Fatal(err)
		}
		if n, ok := v.(*falkordb.Node); ok {
			out = append(out, sagaFromNode(n))
		}
	}
	return out
}

func countEdges(t *testing.T, c *Client, q, uuid string) int64 {
	t.Helper()
	params := map[string]any{}
	if uuid != "" {
		params["s"] = uuid
	}
	res, err := c.graph.ROQuery(q, params, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Next() {
		return 0
	}
	v, _ := res.Record().GetByIndex(0)
	n, _ := v.(int64)
	return n
}
