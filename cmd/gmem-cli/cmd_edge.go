package main

import (
	"encoding/json"

	"github.com/coxlong/graph-memory/pkg/gmem"
	"github.com/spf13/cobra"
)

var (
	edgeUUID, edgeSrc, edgeTgt, edgeName, edgeFact, edgeValidAt, edgeInvalidAt, edgeEpUUID, edgeAttrs string
	edgeLenient                                                                                       bool
)

func init() {
	edgeUpsertCmd.Flags().StringVar(&edgeSrc, "source-uuid", "", "source entity uuid")
	edgeUpsertCmd.Flags().StringVar(&edgeTgt, "target-uuid", "", "target entity uuid")
	edgeUpsertCmd.Flags().StringVar(&edgeName, "name", "", "relation name (e.g. WORKS_ON)")
	edgeUpsertCmd.Flags().StringVar(&edgeFact, "fact", "", "natural language fact")
	edgeUpsertCmd.Flags().StringVar(&edgeValidAt, "valid-at", "", "RFC3339 time")
	edgeUpsertCmd.Flags().StringVar(&edgeEpUUID, "episode-uuid", "", "source episode uuid")
	edgeUpsertCmd.Flags().StringVar(&edgeAttrs, "attributes", "", "attributes JSON")
	edgeUpsertCmd.Flags().BoolVar(&edgeLenient, "lenient", false, "skip schema validation")
	_ = edgeUpsertCmd.MarkFlagRequired("source-uuid")
	_ = edgeUpsertCmd.MarkFlagRequired("target-uuid")
	_ = edgeUpsertCmd.MarkFlagRequired("name")
	_ = edgeUpsertCmd.MarkFlagRequired("fact")

	edgeInvalidateCmd.Flags().StringVar(&edgeUUID, "uuid", "", "edge uuid")
	edgeInvalidateCmd.Flags().StringVar(&edgeInvalidAt, "invalid-at", "", "RFC3339 time (default now)")
	_ = edgeInvalidateCmd.MarkFlagRequired("uuid")

	edgeDeleteCmd.Flags().StringVar(&edgeUUID, "uuid", "", "edge uuid")
	_ = edgeDeleteCmd.MarkFlagRequired("uuid")

	edgeCmd.AddCommand(edgeUpsertCmd, edgeInvalidateCmd, edgeDeleteCmd)
	rootCmd.AddCommand(edgeCmd)
}

var edgeCmd = &cobra.Command{Use: "edge", Short: "Edge operations"}

var edgeUpsertCmd = &cobra.Command{
	Use: "upsert",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		var attrs map[string]any
		if edgeAttrs != "" {
			if err := json.Unmarshal([]byte(edgeAttrs), &attrs); err != nil {
				fatal(err)
			}
		}
		var episodes []string
		if edgeEpUUID != "" {
			episodes = []string{edgeEpUUID}
		}
		e, err := c.UpsertEdge(&gmem.Edge{
			Name: edgeName, Fact: edgeFact, SourceUUID: edgeSrc, TargetUUID: edgeTgt,
			ValidAt: edgeValidAt, Episodes: episodes, Attributes: attrs,
		}, edgeLenient)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}

var edgeInvalidateCmd = &cobra.Command{
	Use: "invalidate",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		e, err := c.InvalidateEdge(edgeUUID, edgeInvalidAt)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}

var edgeDeleteCmd = &cobra.Command{
	Use: "delete",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		if err := c.DeleteEdge(edgeUUID); err != nil {
			fatal(err)
		}
		printJSON(map[string]string{"status": "ok"})
	},
}
