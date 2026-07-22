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
	}, false)
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
	r1, err := c.Add(in, false)
	if err != nil {
		t.Fatal(err)
	}
	// second add with same entity name should dedup (no error, same uuid)
	r2, err := c.Add(in, false)
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

	r, err := c.AddTriplet(&TripletInput{Source: "TriAlice", Name: "WORKS_ON", Fact: "TriAlice works on gmem", Target: "gmem"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceUUID == "" || r.TargetUUID == "" || r.EdgeUUID == "" {
		t.Fatalf("bad result: %+v", r)
	}
	// repeat: entities dedup, edge uuid changes (new edge each call)
	r2, err := c.AddTriplet(&TripletInput{Source: "TriAlice", Name: "WORKS_ON", Fact: "TriAlice works on gmem", Target: "gmem"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceUUID != r2.SourceUUID || r.TargetUUID != r2.TargetUUID {
		t.Fatalf("entities should dedup: %+v vs %+v", r, r2)
	}
}

// TestAddPreflightAbortsBeforeWrites: every parameter-validation failure must
// abort before the episode is created, leaving zero nodes behind.
func TestAddPreflightAbortsBeforeWrites(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	cases := []struct {
		name string
		in   *AddInput
	}{
		{"bad episode source", &AddInput{
			Episode: &Episode{Content: "x", Source: "bogus"},
		}},
		{"bad episode valid_at", &AddInput{
			Episode: &Episode{Content: "x", Source: "text", ValidAt: "not-a-time"},
		}},
		{"bad entity label", &AddInput{
			Episode:  &Episode{Content: "x", Source: "text"},
			Entities: []AddEntityInput{{Name: "E1", Labels: []string{"bad label!"}}},
		}},
		{"bad edge valid_at", &AddInput{
			Episode:  &Episode{Content: "x", Source: "text"},
			Entities: []AddEntityInput{{Name: "E1"}, {Name: "E2"}},
			Edges:    []AddEdgeInput{{Source: "E1", Target: "E2", Name: "KNOWS", Fact: "f", ValidAt: "garbage"}},
		}},
		{"bad edge expired_at", &AddInput{
			Episode:  &Episode{Content: "x", Source: "text"},
			Entities: []AddEntityInput{{Name: "E1"}, {Name: "E2"}},
			Edges:    []AddEdgeInput{{Source: "E1", Target: "E2", Name: "KNOWS", Fact: "f", ExpiredAt: "garbage"}},
		}},
	}
	for _, tc := range cases {
		if _, err := c.Add(tc.in, false); err == nil {
			t.Errorf("%s: want error, got nil", tc.name)
		}
	}

	// nothing was written: no episodes, no entities, no edges
	for _, q := range []string{
		`MATCH (n:Episodic) RETURN count(n)`,
		`MATCH (n:Entity) RETURN count(n)`,
		`MATCH ()-[r]->() RETURN count(r)`,
	} {
		res, err := c.graph.ROQuery(q, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		res.Next()
		v, _ := res.Record().GetByIndex(0)
		if n, _ := v.(int64); n != 0 {
			t.Errorf("%s: want 0, got %d", q, n)
		}
	}
}

// TestAddPreflightEdgeSchema: with a configured schema, an edge violating
// source/target constraints aborts before any write.
func TestAddPreflightEdgeSchema(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	c.Schema = &Schema{
		EntityTypes: map[string]EntityTypeDef{
			"Person": {},
			"City":   {},
		},
		EdgeTypes: map[string]EdgeTypeDef{
			"LIVES_IN": {Source: []string{"Person"}, Target: []string{"City"}},
		},
	}

	// City -> Person violates LIVES_IN; must abort with nothing written
	_, err := c.Add(&AddInput{
		Episode: &Episode{Content: "x", Source: "text"},
		Entities: []AddEntityInput{
			{Name: "Shanghai", Labels: []string{"City"}},
			{Name: "Bob", Labels: []string{"Person"}},
		},
		Edges: []AddEdgeInput{{Source: "Shanghai", Target: "Bob", Name: "LIVES_IN", Fact: "bad direction"}},
	}, false)
	if err == nil {
		t.Fatal("want schema error, got nil")
	}
	res, err := c.graph.ROQuery(`MATCH (n) RETURN count(n)`, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Next()
	v, _ := res.Record().GetByIndex(0)
	if n, _ := v.(int64); n != 0 {
		t.Fatalf("want 0 nodes, got %d", n)
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
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	// second episode under the same saga
	r2, err := c.Add(&AddInput{
		Episode: &Episode{Content: "ep2", Source: "text", ValidAt: "2026-07-02T00:00:00Z"},
		Saga:    "proj-x",
	}, false)
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

// TestAddDryRunNoWrites: dry-run returns candidates but writes nothing.
func TestAddDryRunNoWrites(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	// seed one edge via a real add
	_, err := c.Add(&AddInput{
		Episode:  &Episode{Content: "seed", Source: "text"},
		Entities: []AddEntityInput{{Name: "DryA"}, {Name: "DryB"}},
		Edges:    []AddEdgeInput{{Source: "DryA", Target: "DryB", Name: "KNOWS", Fact: "DryA knows DryB"}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	nodeCnt := countEdges(t, c, `MATCH (n) RETURN count(n)`, "")

	// dry-run the same add: candidates must include the seeded edge, zero new writes
	dr, err := c.Add(&AddInput{
		Episode:  &Episode{Content: "dup", Source: "text"},
		Entities: []AddEntityInput{{Name: "DryA"}, {Name: "DryB"}},
		Edges:    []AddEdgeInput{{Source: "DryA", Target: "DryB", Name: "KNOWS", Fact: "DryA knows DryB"}},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if dr.EpisodeUUID != "" || len(dr.EdgeUUIDs) != 0 {
		t.Fatalf("dry-run should not write: %+v", dr)
	}
	if len(dr.Edges) != 1 || len(dr.Edges[0].Candidates) != 1 {
		t.Fatalf("want 1 candidate, got %+v", dr.Edges)
	}
	if n := countEdges(t, c, `MATCH (n) RETURN count(n)`, ""); n != nodeCnt {
		t.Fatalf("dry-run wrote nodes: %d -> %d", nodeCnt, n)
	}
}

// TestAddDuplicateOfMerges: duplicate_of merges episode attribution, no new edge.
func TestAddDuplicateOfMerges(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	r1, err := c.Add(&AddInput{
		Episode:  &Episode{Content: "first", Source: "text"},
		Entities: []AddEntityInput{{Name: "DupA"}, {Name: "DupB"}},
		Edges:    []AddEdgeInput{{Source: "DupA", Target: "DupB", Name: "KNOWS", Fact: "DupA knows DupB"}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	edgeUUID := r1.EdgeUUIDs[0]

	r2, err := c.Add(&AddInput{
		Episode:  &Episode{Content: "second", Source: "text"},
		Entities: []AddEntityInput{{Name: "DupA"}, {Name: "DupB"}},
		Edges: []AddEdgeInput{{Source: "DupA", Target: "DupB", Name: "KNOWS", Fact: "DupA knows DupB",
			DuplicateOf: edgeUUID}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Edges[0].Merged || r2.Edges[0].EdgeUUID != edgeUUID {
		t.Fatalf("want merged into %s, got %+v", edgeUUID, r2.Edges[0])
	}
	// still exactly one RELATES_TO edge, with both episodes
	if n := countEdges(t, c, `MATCH ()-[r:RELATES_TO]->() RETURN count(r)`, ""); n != 1 {
		t.Fatalf("want 1 edge, got %d", n)
	}
	e, _ := c.GetEdge(edgeUUID)
	if len(e.Episodes) != 2 {
		t.Fatalf("want 2 episodes, got %v", e.Episodes)
	}
	// writeback: episode 2's entity_edges contains the merged edge uuid
	ep2, _ := c.GetEpisode(r2.EpisodeUUID)
	if len(ep2.EntityEdges) != 1 || ep2.EntityEdges[0] != edgeUUID {
		t.Fatalf("ep2 entity_edges: %v", ep2.EntityEdges)
	}
}

// TestAddInvalidateThenWrite: invalidate + new edge in one add.
func TestAddInvalidateThenWrite(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	r1, err := c.Add(&AddInput{
		Episode:  &Episode{Content: "old fact", Source: "text"},
		Entities: []AddEntityInput{{Name: "InvA"}, {Name: "InvB"}},
		Edges:    []AddEdgeInput{{Source: "InvA", Target: "InvB", Name: "WORKS_AT", Fact: "InvA works at InvB as junior"}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	oldUUID := r1.EdgeUUIDs[0]

	r2, err := c.Add(&AddInput{
		Episode:  &Episode{Content: "new fact", Source: "text"},
		Entities: []AddEntityInput{{Name: "InvA"}, {Name: "InvB"}},
		Edges: []AddEdgeInput{{Source: "InvA", Target: "InvB", Name: "WORKS_AT", Fact: "InvA works at InvB as senior",
			Invalidate: []string{oldUUID}}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	// old edge invalidated
	old, _ := c.GetEdge(oldUUID)
	if old.InvalidAt == "" {
		t.Fatal("old edge not invalidated")
	}
	// new edge valid, two edges total
	if n := countEdges(t, c, `MATCH ()-[r:RELATES_TO]->() RETURN count(r)`, ""); n != 2 {
		t.Fatalf("want 2 edges, got %d", n)
	}
	if r2.Edges[0].EdgeUUID == oldUUID {
		t.Fatal("new edge reuses old uuid")
	}
}

// TestAddTripletDryRun: dry-run returns candidates, zero writes.
func TestAddTripletDryRun(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	if _, err := c.AddTriplet(&TripletInput{Source: "TriDryA", Name: "KNOWS", Fact: "TriDryA knows TriDryB", Target: "TriDryB"}, false); err != nil {
		t.Fatal(err)
	}
	dr, err := c.AddTriplet(&TripletInput{Source: "TriDryA", Name: "KNOWS", Fact: "TriDryA knows TriDryB", Target: "TriDryB"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dr.Candidates) != 1 {
		t.Fatalf("want 1 candidate, got %+v", dr.Candidates)
	}
	if dr.EdgeUUID != "" {
		t.Fatal("dry-run should not write")
	}
}

// TestAddTripletDuplicateOf: merge via add-triplet.
func TestAddTripletDuplicateOf(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	r1, err := c.AddTriplet(&TripletInput{Source: "TriDupA", Name: "KNOWS", Fact: "TriDupA knows TriDupB", Target: "TriDupB"}, false)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := c.AddTriplet(&TripletInput{Source: "TriDupA", Name: "KNOWS", Fact: "TriDupA knows TriDupB", Target: "TriDupB",
		DuplicateOf: r1.EdgeUUID, EpisodeUUID: "some-episode"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Merged || r2.EdgeUUID != r1.EdgeUUID {
		t.Fatalf("want merged, got %+v", r2)
	}
	if n := countEdges(t, c, `MATCH ()-[r:RELATES_TO]->() RETURN count(r)`, ""); n != 1 {
		t.Fatalf("want 1 edge, got %d", n)
	}
	e, _ := c.GetEdge(r1.EdgeUUID)
	if len(e.Episodes) != 1 || e.Episodes[0] != "some-episode" {
		t.Fatalf("episodes: %v", e.Episodes)
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
