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
