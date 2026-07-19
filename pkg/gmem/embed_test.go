package gmem

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbed(t *testing.T) {
	srv := newFakeEmbedServer(t)
	defer srv.Close()
	e := NewEmbedder(srv.URL, "k", "m")
	vec, err := e.Embed("hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 8 || vec[0] != 0.1 {
		t.Fatalf("bad vector: %v", vec)
	}
}

func TestEmbedHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
	defer srv.Close()
	e := NewEmbedder(srv.URL, "k", "m")
	if _, err := e.Embed("x"); err == nil {
		t.Fatal("expected error on 500")
	}
}

