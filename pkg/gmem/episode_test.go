package gmem

import "testing"

func TestCreateAndGetEpisode(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	ep, err := c.CreateEpisode(&Episode{
		Name: "chat-1", Content: "user: hello", Source: "message",
		Metadata: map[string]any{"channel": "cli"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ep.UUID == "" || ep.CreatedAt == "" || ep.GroupID == "" {
		t.Fatalf("bad episode: %+v", ep)
	}

	got, err := c.GetEpisode(ep.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "user: hello" || got.Metadata["channel"] != "cli" {
		t.Fatalf("bad get: %+v", got)
	}
}

func TestCreateEpisodeBadSource(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	_, err := c.CreateEpisode(&Episode{Content: "x", Source: "bogus"})
	if err == nil {
		t.Fatal("expected source validation error")
	}
}

func TestListEpisodes(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	for _, name := range []string{"e1", "e2"} {
		if _, err := c.CreateEpisode(&Episode{Name: name, Content: name, Source: "text"}); err != nil {
			t.Fatal(err)
		}
	}
	eps, err := c.ListEpisodes("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 {
		t.Fatalf("want 2, got %d", len(eps))
	}
}
