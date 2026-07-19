package gmem

import "testing"

func TestBuildCommunities(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	c.UpsertEntity(&Entity{Name: "Alice", Labels: []string{"Person"}}, false)
	c.UpsertEntity(&Entity{Name: "Bob", Labels: []string{"Person"}}, false)
	c.UpsertEntity(&Entity{Name: "ProjX", Labels: []string{"Project"}}, false)

	comms, err := c.BuildCommunities("")
	if err != nil {
		t.Fatal(err)
	}
	if len(comms) != 2 {
		t.Fatalf("want 2 type-clusters, got %d", len(comms))
	}
	total := 0
	for _, cm := range comms {
		total += len(cm.MemberUUIDs)
	}
	if total != 3 {
		t.Fatalf("want 3 total members, got %d", total)
	}
}

func TestUpsertAndGetCommunity(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "Alice", Labels: []string{"Person"}}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "Bob", Labels: []string{"Person"}}, false)

	com, err := c.UpsertCommunity("People", "two engineers", []string{a.UUID, b.UUID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(com.MemberUUIDs) != 2 {
		t.Fatalf("want 2 members, got %v", com.MemberUUIDs)
	}
	got, err := c.GetCommunity(com.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "two engineers" || len(got.MemberUUIDs) != 2 {
		t.Fatalf("bad get: %+v", got)
	}
	// upsert again with different members rewires
	com2, err := c.UpsertCommunity("People", "only alice", []string{a.UUID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(com2.MemberUUIDs) != 1 {
		t.Fatalf("want 1 member after rewire, got %v", com2.MemberUUIDs)
	}
}
