package gmem

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func newFakeEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8]}]}`)
	}))
}

func newTestClient(t *testing.T, embedURL string) *Client {
	t.Helper()
	addr := os.Getenv("FALKORDB_TEST_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	cfg := &Config{
		FalkorAddr:     addr,
		FalkorUser:     os.Getenv("FALKORDB_TEST_USER"),
		FalkorPassword: os.Getenv("FALKORDB_TEST_PASSWORD"),
		Graph:          fmt.Sprintf("gmem_test_%d", time.Now().UnixNano()),
		EmbedBase:      embedURL,
		EmbedKey:       "test",
		EmbedModel:     "test-model",
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Skipf("FalkorDB unavailable at %s: %v", addr, err)
	}
	ctx := context.Background()
	if err := c.db.Conn.Ping(ctx).Err(); err != nil {
		t.Skipf("FalkorDB unavailable at %s: %v", addr, err)
	}
	if err := c.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		c.db.Conn.Do(ctx, "GRAPH.DELETE", cfg.Graph)
	})
	return c
}

func TestInitIdempotent(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	// newTestClient 已 Init 一次；再调一次不应报错
	if err := c.Init(); err != nil {
		t.Fatalf("second init: %v", err)
	}
}

func TestStatus(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	c := newTestClient(t, srv.URL)
	st := c.Status()
	if st.FalkorDB != "ok" || st.Embedding != "ok" {
		t.Fatalf("bad status: %+v", st)
	}
	// IndexesOK may be false if the FalkorDB user lacks db.indexes() permission
}
