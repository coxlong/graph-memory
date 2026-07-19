package gmem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FalkorAddr != "localhost:6379" || cfg.Graph != "gmem" || cfg.EmbedModel != "text-embedding-3-small" {
		t.Fatalf("bad defaults: %+v", cfg)
	}
}

func TestLoadConfigFileAndEnvOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gmem.yaml")
	os.WriteFile(p, []byte("falkordb_addr: file:1234\nembedding_model: file-model\n"), 0o644)
	t.Setenv("FALKORDB_ADDR", "env:6379")
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FalkorAddr != "env:6379" {
		t.Fatalf("env should override file: %s", cfg.FalkorAddr)
	}
	if cfg.EmbedModel != "file-model" {
		t.Fatalf("file value lost: %s", cfg.EmbedModel)
	}
}
