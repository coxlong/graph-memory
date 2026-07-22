package gmem

import "testing"

func TestUpsertAndGetEdge(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "Alice"}, false)
	p, _, _ := c.UpsertEntity(&Entity{Name: "ProjX"}, false)

	e, err := c.UpsertEdge(&Edge{
		Name: "WORKS_ON", Fact: "Alice works on ProjX",
		SourceUUID: a.UUID, TargetUUID: p.UUID,
		ValidAt: "2026-07-19T00:00:00Z",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetEdge(e.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fact != "Alice works on ProjX" || got.SourceUUID != a.UUID || got.TargetUUID != p.UUID {
		t.Fatalf("bad edge: %+v", got)
	}
}

func TestInvalidateEdge(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "A"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "B"}, false)
	e, _ := c.UpsertEdge(&Edge{Name: "KNOWS", Fact: "A knows B", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)

	got, err := c.InvalidateEdge(e.UUID, "2026-07-19T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if got.InvalidAt != "2026-07-19T12:00:00Z" {
		t.Fatalf("invalid_at: %q", got.InvalidAt)
	}
}

func TestInvalidateEdgeDefaultTime(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "A2"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "B2"}, false)
	e, _ := c.UpsertEdge(&Edge{Name: "KNOWS", Fact: "x", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)
	got, err := c.InvalidateEdge(e.UUID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.InvalidAt == "" {
		t.Fatal("invalid_at should default to now")
	}
}

func TestDeleteEdge(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "A3"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "B3"}, false)
	e, _ := c.UpsertEdge(&Edge{Name: "KNOWS", Fact: "x", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)
	if err := c.DeleteEdge(e.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetEdge(e.UUID); err == nil {
		t.Fatal("edge should be gone")
	}
}

func TestUpsertEdgeMissingEndpoint(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	b, _, _ := c.UpsertEntity(&Entity{Name: "B4"}, false)
	_, err := c.UpsertEdge(&Edge{Name: "X", Fact: "x", SourceUUID: newUUID(), TargetUUID: b.UUID}, false)
	if err == nil {
		t.Fatal("expected missing endpoint error")
	}
}

func TestUpsertEdgeExpiredAt(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "ExpAlice"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "ExpTeam"}, false)
	e, err := c.UpsertEdge(&Edge{
		Name: "MEMBER_OF", Fact: "ExpAlice member of ExpTeam",
		SourceUUID: a.UUID, TargetUUID: b.UUID,
		ValidAt: "2026-01-01T00:00:00Z", ExpiredAt: "2026-06-01T00:00:00Z",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetEdge(e.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpiredAt != "2026-06-01T00:00:00Z" {
		t.Fatalf("expired_at not persisted: %q", got.ExpiredAt)
	}
	// The edge expired 2026-06; default search at now(2026-07) must hide it.
	res, _ := c.SearchEdges("member of ExpTeam", 10, "", nil, "", false)
	if len(res) != 0 {
		t.Fatalf("expired edge should be hidden by default: %v", res)
	}
	// as-of before the expiry: edge active
	res, err = c.searchEdgesFiltered("member of ExpTeam", 10, "2026-05-01T00:00:00Z", nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("as-of before expiry should find edge: %v", res)
	}
	// includeInvalid returns it regardless
	res, _ = c.SearchEdges("member of ExpTeam", 10, "", nil, "", true)
	if len(res) != 1 {
		t.Fatalf("includeInvalid should find expired edge: %v", res)
	}
}

func TestFindEdgeCandidates(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "CandA"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "CandB"}, false)
	// two edges same (src,tgt,name) + one different-name edge
	c.UpsertEdge(&Edge{Name: "KNOWS", Fact: "CandA knows CandB v1", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)
	c.UpsertEdge(&Edge{Name: "KNOWS", Fact: "CandA knows CandB v2", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)
	c.UpsertEdge(&Edge{Name: "WORKS_WITH", Fact: "different", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)

	cands, err := c.FindEdgeCandidates(a.UUID, b.UUID, "KNOWS")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(cands))
	}
	// invalidate one; candidates still include it with InvalidAt set
	c.InvalidateEdge(cands[0].UUID, "2026-07-19T12:00:00Z")
	cands2, _ := c.FindEdgeCandidates(a.UUID, b.UUID, "KNOWS")
	if len(cands2) != 2 {
		t.Fatalf("want 2 candidates after invalidate, got %d", len(cands2))
	}
	for _, e := range cands2 {
		if e.UUID == cands[0].UUID && e.InvalidAt == "" {
			t.Fatalf("invalidated candidate missing InvalidAt: %+v", e)
		}
	}
}

func TestMergeEdgeEpisode(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "MrgA"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "MrgB"}, false)
	e, _ := c.UpsertEdge(&Edge{Name: "KNOWS", Fact: "MrgA knows MrgB", SourceUUID: a.UUID, TargetUUID: b.UUID}, false)

	// append episode
	merged, err := c.MergeEdgeEpisode(e.UUID, "ep1")
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Episodes) != 1 || merged.Episodes[0] != "ep1" {
		t.Fatalf("episodes: %v", merged.Episodes)
	}
	// idempotent: same episode twice
	merged2, _ := c.MergeEdgeEpisode(e.UUID, "ep1")
	if len(merged2.Episodes) != 1 {
		t.Fatalf("want 1 episode after dup, got %v", merged2.Episodes)
	}
	// second episode appends
	merged3, _ := c.MergeEdgeEpisode(e.UUID, "ep2")
	if len(merged3.Episodes) != 2 {
		t.Fatalf("want 2 episodes, got %v", merged3.Episodes)
	}
	// empty episode is a no-op
	merged4, _ := c.MergeEdgeEpisode(e.UUID, "")
	if len(merged4.Episodes) != 2 {
		t.Fatalf("empty episode should not change count: %v", merged4.Episodes)
	}
	// unknown uuid errors
	if _, err := c.MergeEdgeEpisode("nonexistent", "ep"); err == nil {
		t.Fatal("want error for unknown uuid")
	}
}
