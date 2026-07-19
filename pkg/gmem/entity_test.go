package gmem

import "testing"

func TestUpsertEntityDedup(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	e1, created1, err := c.UpsertEntity(&Entity{Name: "Alice", Labels: []string{"Person"}}, false)
	if err != nil || !created1 {
		t.Fatalf("first upsert: %v created=%v", err, created1)
	}
	e2, created2, err := c.UpsertEntity(&Entity{Name: "Alice"}, false)
	if err != nil || created2 {
		t.Fatalf("second upsert should dedup: %v created=%v", err, created2)
	}
	if e1.UUID != e2.UUID {
		t.Fatalf("dedup uuid mismatch: %s vs %s", e1.UUID, e2.UUID)
	}
}

func TestUpsertEntityLabelsAndEmbedding(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	e, _, err := c.UpsertEntity(&Entity{Name: "Bob", Labels: []string{"Person"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.graph.ROQuery(`MATCH (n {uuid: $uuid}) RETURN labels(n), n.name_embedding`,
		map[string]any{"uuid": e.UUID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Next() {
		t.Fatal("no entity returned")
	}
	labVal, _ := res.Record().GetByIndex(0)
	embVal, _ := res.Record().GetByIndex(1)
	labels := strSlice(labVal)
	if !contains(labels, "Entity") || !contains(labels, "Person") {
		t.Fatalf("labels: %v", labels)
	}
	if embVal == nil {
		t.Fatal("name_embedding not set")
	}
}

func TestUpdateEntityMergeAttrs(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	e, _, _ := c.UpsertEntity(&Entity{Name: "Carol", Attributes: map[string]any{"a": 1}}, false)
	got, err := c.UpdateEntity(e.UUID, "", "new summary", map[string]any{"b": 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "new summary" {
		t.Fatalf("summary not updated: %+v", got)
	}
	if got.Attributes["a"] != float64(1) {
		t.Fatalf("attr a lost: %+v", got.Attributes)
	}
	if _, ok := got.Attributes["b"]; !ok {
		t.Fatal("attr b missing")
	}
}

func TestMergeEntities(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	a, _, _ := c.UpsertEntity(&Entity{Name: "Alice", Labels: []string{"Person"}, Attributes: map[string]any{"role": "be"}}, false)
	b, _, _ := c.UpsertEntity(&Entity{Name: "Alice Wang", Labels: []string{"User"}}, false)
	x, _, _ := c.UpsertEntity(&Entity{Name: "ProjectX"}, false)
	if _, err := c.graph.Query(`MATCH (s:Entity {uuid:$s}), (t:Entity {uuid:$t})
		CREATE (s)-[:RELATES_TO {uuid:$e, group_id:'default'}]->(t)`,
		map[string]any{"s": a.UUID, "t": x.UUID, "e": newUUID()}, nil); err != nil {
		t.Fatal(err)
	}
	merged, err := c.MergeEntities(a.UUID, b.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(merged.Labels, "Person") || !contains(merged.Labels, "User") {
		t.Fatalf("labels union: %v", merged.Labels)
	}
	if merged.Attributes["role"] != "be" {
		t.Fatalf("attrs merged: %v", merged.Attributes)
	}
	res, _ := c.graph.ROQuery(`MATCH (n:Entity {uuid:$u})-[r:RELATES_TO]->() RETURN count(r)`,
		map[string]any{"u": b.UUID}, nil)
	res.Next()
	cnt, _ := res.Record().GetByIndex(0)
	if n, ok := cnt.(int64); !ok || n != 1 {
		t.Fatalf("rewired edges: %v", cnt)
	}
	if _, err := c.GetEntity(a.UUID); err == nil {
		t.Fatal("from entity should be deleted")
	}
}

func TestDeleteNode(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	e, _, _ := c.UpsertEntity(&Entity{Name: "Temp"}, false)
	if err := c.DeleteNode(e.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetEntity(e.UUID); err == nil {
		t.Fatal("node should be gone")
	}
}

// Under a configured schema, referencing an existing entity by name (empty
// labels) must succeed; creating a new entity without a configured type fails.
func TestUpsertEntitySchemaExistingVsNew(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	c.Schema = &Schema{EntityTypes: map[string]EntityTypeDef{
		"Person": {Attributes: map[string]AttributeDef{"role": {Type: "string", Required: true}}},
	}}

	// create a typed entity
	if _, _, err := c.UpsertEntity(&Entity{
		Name: "TypedAlice", Labels: []string{"Person"},
		Attributes: map[string]any{"role": "be"},
	}, false); err != nil {
		t.Fatal(err)
	}
	// reference it by name with no labels: OK, and deduped
	got, created, err := c.UpsertEntity(&Entity{Name: "TypedAlice"}, false)
	if err != nil || created {
		t.Fatalf("existing entity by name: %v created=%v", err, created)
	}
	if !contains(got.Labels, "Person") {
		t.Fatalf("labels lost: %v", got.Labels)
	}
	// new entity without a configured type: rejected
	if _, _, err := c.UpsertEntity(&Entity{Name: "UntypedBob"}, false); err == nil {
		t.Fatal("expected schema rejection for untyped new entity")
	}
	// new typed entity missing a required attribute: rejected
	if _, _, err := c.UpsertEntity(&Entity{Name: "NoRole", Labels: []string{"Person"}}, false); err == nil {
		t.Fatal("expected missing required attribute error")
	}
}
