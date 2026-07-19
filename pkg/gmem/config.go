package gmem

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	FalkorAddr     string `yaml:"falkordb_addr"`
	FalkorUser     string `yaml:"falkordb_user"`
	FalkorPassword string `yaml:"falkordb_password"`
	GroupID        string `yaml:"group_id"`
	EmbedBase      string `yaml:"embedding_api_base"`
	EmbedKey       string `yaml:"embedding_api_key"`
	EmbedModel     string `yaml:"embedding_model"`
	SchemaPath     string `yaml:"schema_path"`
}

// defaultConfigPath returns the default config file path ~/.gmem.yaml, or ""
// if the home directory cannot be resolved (file read is then skipped).
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".gmem.yaml")
}

func LoadConfig(configPath string) (*Config, error) {
	cfg := &Config{
		FalkorAddr: "localhost:6379",
		GroupID:    "default",
		EmbedBase:  "https://api.openai.com/v1",
		EmbedModel: "text-embedding-3-small",
	}
	if configPath == "" {
		configPath = defaultConfigPath()
	}
	if configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, err
			}
		}
	}
	env := map[string]*string{
		"FALKORDB_ADDR":      &cfg.FalkorAddr,
		"FALKORDB_USER":      &cfg.FalkorUser,
		"FALKORDB_PASSWORD":  &cfg.FalkorPassword,
		"GMEM_GROUP_ID":      &cfg.GroupID,
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
