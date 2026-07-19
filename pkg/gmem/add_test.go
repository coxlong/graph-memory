package gmem

import "testing"

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
