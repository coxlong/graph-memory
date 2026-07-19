package main

import (
	"encoding/json"
	"log/slog"
	"os"

	"github.com/coxlong/graph-memory/pkg/gmem"
	"github.com/spf13/cobra"
)

var groupIDFlag string

var rootCmd = &cobra.Command{
	Use:   "gmem-cli",
	Short: "Graph memory CLI for agents (FalkorDB + graphiti schema)",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&groupIDFlag, "group-id", "", "group id (selects the FalkorDB graph; default from config)")
}

func loadClient() (*gmem.Client, error) {
	cfg, err := gmem.LoadConfig("")
	if err != nil {
		return nil, err
	}
	if groupIDFlag != "" {
		cfg.GroupID = groupIDFlag
	}
	return gmem.NewClient(cfg)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	slog.Error("command failed", "err", err)
	os.Exit(1)
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
