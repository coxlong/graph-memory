package main

import (
	"encoding/json"

	"github.com/coxlong/graph-memory/pkg/gmem"
	"github.com/spf13/cobra"
)

var (
	addContent, addSource, addEntities, addEdges, addMetadata, addValidAt, addSaga string
	addLenient, addDryRun                                                          bool
)

func init() {
	addCmd.Flags().StringVar(&addContent, "content", "", "episode raw content")
	addCmd.Flags().StringVar(&addSource, "source", "message", "message|text|json")
	addCmd.Flags().StringVar(&addEntities, "entities", "", "entities JSON array")
	addCmd.Flags().StringVar(&addEdges, "edges", "", "edges JSON array")
	addCmd.Flags().StringVar(&addMetadata, "metadata", "", "episode metadata JSON object")
	addCmd.Flags().StringVar(&addValidAt, "valid-at", "", "RFC3339 time of the episode")
	addCmd.Flags().StringVar(&addSaga, "saga", "", "saga name; links the episode via HAS_EPISODE/NEXT_EPISODE")
	addCmd.Flags().BoolVar(&addLenient, "lenient", false, "skip schema validation")
	addCmd.Flags().BoolVar(&addDryRun, "dry-run", false, "validate + detect duplicate candidates, zero writes")
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
			Saga:    addSaga,
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
		res, err := c.Add(in, addDryRun)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}

var (
	triSource, triName, triFact, triTarget, triValidAt, triDuplicateOf, triInvalidate, triEpisodeUUID string
	triLenient, triDryRun                                                                            bool
)

func init() {
	addTripletCmd.Flags().StringVar(&triSource, "source", "", "source entity name")
	addTripletCmd.Flags().StringVar(&triName, "name", "", "relation name")
	addTripletCmd.Flags().StringVar(&triFact, "fact", "", "natural language fact")
	addTripletCmd.Flags().StringVar(&triTarget, "target", "", "target entity name")
	addTripletCmd.Flags().StringVar(&triValidAt, "valid-at", "", "RFC3339 time")
	addTripletCmd.Flags().StringVar(&triDuplicateOf, "duplicate-of", "", "existing edge uuid: merge episode attribution instead of creating")
	addTripletCmd.Flags().StringVar(&triInvalidate, "invalidate", "", "comma-separated edge uuids to invalidate before writing")
	addTripletCmd.Flags().StringVar(&triEpisodeUUID, "episode-uuid", "", "source episode uuid (attribution)")
	addTripletCmd.Flags().BoolVar(&triLenient, "lenient", false, "skip schema validation")
	addTripletCmd.Flags().BoolVar(&triDryRun, "dry-run", false, "detect duplicate candidates, zero writes")
	_ = addTripletCmd.MarkFlagRequired("source")
	_ = addTripletCmd.MarkFlagRequired("name")
	_ = addTripletCmd.MarkFlagRequired("fact")
	_ = addTripletCmd.MarkFlagRequired("target")
	rootCmd.AddCommand(addTripletCmd)
}

var addTripletCmd = &cobra.Command{
	Use:   "add-triplet",
	Short: "Add a single fact triplet (entities deduped by name)",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.AddTriplet(&gmem.TripletInput{
			Source: triSource, Name: triName, Fact: triFact, Target: triTarget,
			ValidAt: triValidAt, DuplicateOf: triDuplicateOf,
			Invalidate: parseTypes(triInvalidate), EpisodeUUID: triEpisodeUUID,
			Lenient: triLenient,
		}, triDryRun)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}
