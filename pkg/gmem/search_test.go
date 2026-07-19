package gmem

import "testing"

func TestRRFFuse(t *testing.T) {
	// two recall paths: vector [a b c], fulltext [b a d]
	ranked := rrfFuse([][]string{{"a", "b", "c"}, {"b", "a", "d"}}, 60)
	if len(ranked) != 4 {
		t.Fatalf("want 4 docs, got %d", len(ranked))
	}
	// a: 1/61+1/62 ≈ 0.0325; b: 1/62+1/61 ≈ 0.0325 — tied top two; c, d after
	top2 := map[string]bool{ranked[0]: true, ranked[1]: true}
	if !top2["a"] || !top2["b"] {
		t.Fatalf("top2: %v", ranked)
	}
	if ranked[2] == ranked[3] || (ranked[2] != "c" && ranked[2] != "d") {
		t.Fatalf("tail: %v", ranked)
	}
}

func TestSearchEntities(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	if _, _, err := c.UpsertEntity(&Entity{Name: "Alice Wonderland"}, false); err != nil {
		t.Fatal(err)
	}
	res, err := c.SearchEntities("Alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 || res[0].Name != "Alice Wonderland" {
		t.Fatalf("search: %+v", res)
	}
}

func TestSearchEdgesTemporalFilter(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "TAlice"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "TeamA"}, false)
	e, _ := c.UpsertEdge(&Edge{
		Name: "MEMBER_OF", Fact: "TAlice member of TeamA",
		SourceUUID: a.UUID, TargetUUID: b.UUID,
		ValidAt: "2026-01-01T00:00:00Z",
	}, false)

	// default (valid edge): should be found
	res, err := c.SearchEdges("member of TeamA", 10, false)
	if err != nil || len(res) == 0 {
		t.Fatalf("valid edge search: %v %v", res, err)
	}
	// after invalidation, default search hides it
	if _, err := c.InvalidateEdge(e.UUID, "2026-06-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	res, _ = c.SearchEdges("member of TeamA", 10, false)
	if len(res) != 0 {
		t.Fatalf("invalidated edge should be hidden: %v", res)
	}
	// includeInvalid finds it
	res, _ = c.SearchEdges("member of TeamA", 10, true)
	if len(res) != 1 {
		t.Fatalf("includeInvalid: %v", res)
	}
}

func TestSearchAsOf(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "AsOfAlice"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "TeamB"}, false)
	e, _ := c.UpsertEdge(&Edge{
		Name: "MEMBER_OF", Fact: "AsOfAlice member of TeamB",
		SourceUUID: a.UUID, TargetUUID: b.UUID,
		ValidAt: "2026-01-01T00:00:00Z",
	}, false)
	if _, err := c.InvalidateEdge(e.UUID, "2026-06-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// as-of March: edge still valid
	res, err := c.Search("member of TeamB", SearchOpts{AsOf: "2026-03-01T00:00:00Z", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) != 1 {
		t.Fatalf("as-of should see the edge: %+v", res.Edges)
	}
	// now: default hides it
	res, _ = c.Search("member of TeamB", SearchOpts{Limit: 10})
	if len(res.Edges) != 0 {
		t.Fatalf("now should not see invalidated edge: %+v", res.Edges)
	}
}
