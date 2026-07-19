package gmem

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	FalkorAddr     string `yaml:"falkordb_addr"`
	FalkorUser     string `yaml:"falkordb_user"`
	FalkorPassword string `yaml:"falkordb_password"`
	Graph          string `yaml:"falkordb_graph"`
	EmbedBase      string `yaml:"embedding_api_base"`
	EmbedKey       string `yaml:"embedding_api_key"`
	EmbedModel     string `yaml:"embedding_model"`
	SchemaPath     string `yaml:"schema_path"`
}

func LoadConfig(configPath string) (*Config, error) {
	cfg := &Config{
		FalkorAddr: "localhost:6379",
		Graph:      "gmem",
		EmbedBase:  "https://api.openai.com/v1",
		EmbedModel: "text-embedding-3-small",
	}
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}
	env := map[string]*string{
		"FALKORDB_ADDR":      &cfg.FalkorAddr,
		"FALKORDB_USER":      &cfg.FalkorUser,
		"FALKORDB_PASSWORD":  &cfg.FalkorPassword,
		"FALKORDB_GRAPH":     &cfg.Graph,
		"EMBEDDING_API_BASE": &cfg.EmbedBase,
		"EMBEDDING_API_KEY":  &cfg.EmbedKey,
		"EMBEDDING_MODEL":    &cfg.EmbedModel,
		"GMEM_SCHEMA":        &cfg.SchemaPath,
	}
	for k, ptr := range env {
		if v := os.Getenv(k); v != "" {
			*ptr = v
		}
	}
	return cfg, nil
}
