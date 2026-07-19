package main

import (
	"encoding/json"

	"github.com/coxlong/graph-memory/pkg/gmem"
	"github.com/spf13/cobra"
)

var (
	addContent, addSource, addEntities, addEdges, addMetadata, addValidAt, addGroup string
	addLenient                                                                      bool
)

func init() {
	addCmd.Flags().StringVar(&addContent, "content", "", "episode raw content")
	addCmd.Flags().StringVar(&addSource, "source", "message", "message|text|json")
	addCmd.Flags().StringVar(&addEntities, "entities", "", "entities JSON array")
	addCmd.Flags().StringVar(&addEdges, "edges", "", "edges JSON array")
	addCmd.Flags().StringVar(&addMetadata, "metadata", "", "episode metadata JSON object")
	addCmd.Flags().StringVar(&addValidAt, "valid-at", "", "RFC3339 time of the episode")
	addCmd.Flags().StringVar(&addGroup, "group-id", "", "group id")
	addCmd.Flags().BoolVar(&addLenient, "lenient", false, "skip schema validation")
	_ = addCmd.MarkFlagRequired("content")
	rootCmd.AddCommand(addCmd)
}

var addCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an episode with extracted entities and edges in one call",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		in := &gmem.AddInput{
			Episode: &gmem.Episode{Content: addContent, Source: addSource, ValidAt: addValidAt},
			GroupID: addGroup,
			Lenient: addLenient,
		}
		if addEntities != "" {
			if err := json.Unmarshal([]byte(addEntities), &in.Entities); err != nil {
				fatal(err)
			}
		}
		if addEdges != "" {
			if err := json.Unmarshal([]byte(addEdges), &in.Edges); err != nil {
				fatal(err)
			}
		}
		if addMetadata != "" {
			if err := json.Unmarshal([]byte(addMetadata), &in.Episode.Metadata); err != nil {
				fatal(err)
			}
		}
		res, err := c.Add(in)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}
