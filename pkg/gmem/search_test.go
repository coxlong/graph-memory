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
	res, err := c.SearchEntities("Alice", 10, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 || res[0].Name != "Alice Wonderland" {
		t.Fatalf("search: %+v", res)
	}
}

func TestSearchEntitiesTypeFilter(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	if _, _, err := c.UpsertEntity(&Entity{Name: "FilterAlice", Labels: []string{"Person"}}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.UpsertEntity(&Entity{Name: "FilterAcme", Labels: []string{"Organization"}}, false); err != nil {
		t.Fatal(err)
	}
	// match by type
	res, err := c.SearchEntities("Filter", 10, []string{"Person"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Name != "FilterAlice" {
		t.Fatalf("type filter Person: %+v", res)
	}
	// mismatched type -> no results
	res, _ = c.SearchEntities("Filter", 10, []string{"Location"}, "")
	if len(res) != 0 {
		t.Fatalf("type filter Location should match nothing: %+v", res)
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
	res, err := c.SearchEdges("member of TeamA", 10, "", nil, "", false)
	if err != nil || len(res) == 0 {
		t.Fatalf("valid edge search: %v %v", res, err)
	}
	// after invalidation, default search hides it
	if _, err := c.InvalidateEdge(e.UUID, "2026-06-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	res, _ = c.SearchEdges("member of TeamA", 10, "", nil, "", false)
	if len(res) != 0 {
		t.Fatalf("invalidated edge should be hidden: %v", res)
	}
	// includeInvalid finds it
	res, _ = c.SearchEdges("member of TeamA", 10, "", nil, "", true)
	if len(res) != 1 {
		t.Fatalf("includeInvalid: %v", res)
	}
}

func TestSearchEdgesTypeFilter(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "TypeAlice"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "TypeTeam"}, false)
	if _, err := c.UpsertEdge(&Edge{
		Name: "MEMBER_OF", Fact: "TypeAlice member of TypeTeam",
		SourceUUID: a.UUID, TargetUUID: b.UUID,
		ValidAt: "2026-01-01T00:00:00Z",
	}, false); err != nil {
		t.Fatal(err)
	}
	// match by type
	res, err := c.SearchEdges("TypeAlice", 10, "", []string{"MEMBER_OF"}, "", false)
	if err != nil || len(res) != 1 {
		t.Fatalf("type filter MEMBER_OF: %v %v", res, err)
	}
	// mismatched type -> no results
	res, _ = c.SearchEdges("TypeAlice", 10, "", []string{"KNOWS"}, "", false)
	if len(res) != 0 {
		t.Fatalf("type filter KNOWS should match nothing: %+v", res)
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
	res, err := c.SearchEdges("member of TeamB", 10, "2026-03-01T00:00:00Z", nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("as-of should see the edge: %+v", res)
	}
	// now: default hides it
	res, _ = c.SearchEdges("member of TeamB", 10, "", nil, "", false)
	if len(res) != 0 {
		t.Fatalf("now should not see invalidated edge: %+v", res)
	}
}

func TestSearchEntitiesMethod(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	if _, _, err := c.UpsertEntity(&Entity{Name: "MethodAlice"}, false); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"vector", "bm25"} {
		res, err := c.SearchEntities("MethodAlice", 10, nil, m)
		if err != nil {
			t.Fatalf("method %s: %v", m, err)
		}
		if len(res) == 0 || res[0].Name != "MethodAlice" {
			t.Fatalf("method %s should find MethodAlice: %+v", m, res)
		}
	}
}

func TestSearchEdgesMethod(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "MethodAlice"}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "MethodTeam"}, false)
	if _, err := c.UpsertEdge(&Edge{
		Name: "MEMBER_OF", Fact: "MethodAlice member of MethodTeam",
		SourceUUID: a.UUID, TargetUUID: b.UUID,
		ValidAt: "2026-01-01T00:00:00Z",
	}, false); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"vector", "bm25"} {
		res, err := c.SearchEdges("member of MethodTeam", 10, "", nil, m, false)
		if err != nil {
			t.Fatalf("method %s: %v", m, err)
		}
		if len(res) != 1 {
			t.Fatalf("method %s should find the edge: %+v", m, res)
		}
	}
}
