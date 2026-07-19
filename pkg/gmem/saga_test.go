package gmem

import "testing"

func TestCreateAndGetSaga(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	s, err := c.CreateSaga(&Saga{
		Name:             "project-alpha",
		Summary:          "summary v1",
		FirstEpisodeUUID: "ep1",
		LastEpisodeUUID:  "ep5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.UUID == "" || s.GroupID == "" || s.CreatedAt == "" {
		t.Fatalf("bad saga: %+v", s)
	}
	got, err := c.GetSaga(s.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "project-alpha" || got.FirstEpisodeUUID != "ep1" || got.LastEpisodeUUID != "ep5" {
		t.Fatalf("bad get: %+v", got)
	}
}

func TestUpdateSaga(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	s, _ := c.CreateSaga(&Saga{Name: "saga-1", Summary: "v1", FirstEpisodeUUID: "ep1"})

	updated, err := c.UpdateSaga(s.UUID, "v2", "ep10", "2026-07-19T00:00:00Z", "2026-07-18T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Summary != "v2" || updated.LastEpisodeUUID != "ep10" || updated.FirstEpisodeUUID != "ep1" {
		t.Fatalf("bad update: %+v", updated)
	}
	if updated.LastSummarizedAt != "2026-07-19T00:00:00Z" || updated.LastSummarizedEpisodeValidAt != "2026-07-18T00:00:00Z" {
		t.Fatalf("bad timestamps: %+v", updated)
	}
}
