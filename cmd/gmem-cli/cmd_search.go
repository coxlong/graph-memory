package main

import (
	"github.com/coxlong/graph-memory/pkg/gmem"
	"github.com/spf13/cobra"
)

var (
	searchQuery, searchAsOf, searchGroup string
	searchLimit                          int
	searchIncludeInvalid                 bool
)

func init() {
	searchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results per category")
	searchCmd.Flags().StringVar(&searchAsOf, "as-of", "", "RFC3339 point-in-time filter")
	searchCmd.Flags().StringVar(&searchGroup, "group-id", "", "group id")
	searchCmd.Flags().BoolVar(&searchIncludeInvalid, "include-invalid", false, "include invalidated facts")
	_ = searchCmd.MarkFlagRequired("query")

	entitySearchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	entitySearchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results")
	_ = entitySearchCmd.MarkFlagRequired("query")

	edgeSearchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	edgeSearchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results")
	edgeSearchCmd.Flags().BoolVar(&searchIncludeInvalid, "include-invalid", false, "include invalidated facts")
	_ = edgeSearchCmd.MarkFlagRequired("query")

	entityCmd.AddCommand(entitySearchCmd)
	edgeCmd.AddCommand(edgeSearchCmd)
	rootCmd.AddCommand(searchCmd)
}

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Hybrid search across entities, facts and episodes",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.Search(searchQuery, gmem.SearchOpts{
			GroupID:        searchGroup,
			AsOf:           searchAsOf,
			Limit:          searchLimit,
			IncludeInvalid: searchIncludeInvalid,
		})
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}

var entitySearchCmd = &cobra.Command{
	Use: "search",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.SearchEntities(searchQuery, searchLimit)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}

var edgeSearchCmd = &cobra.Command{
	Use: "search",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.SearchEdges(searchQuery, searchLimit, searchIncludeInvalid)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}
