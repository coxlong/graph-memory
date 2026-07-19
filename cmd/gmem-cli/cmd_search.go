package main

import (
	"strings"

	"github.com/spf13/cobra"
)

var (
	searchQuery, searchAsOf string
	searchLimit             int
	searchIncludeInvalid    bool
	searchTypes             string
)

func parseTypes(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func init() {
	entitySearchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	entitySearchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results")
	entitySearchCmd.Flags().StringVar(&searchTypes, "type", "", "comma-separated entity types (node labels) to filter by")
	_ = entitySearchCmd.MarkFlagRequired("query")

	edgeSearchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	edgeSearchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results")
	edgeSearchCmd.Flags().StringVar(&searchAsOf, "as-of", "", "RFC3339 point-in-time filter")
	edgeSearchCmd.Flags().StringVar(&searchTypes, "type", "", "comma-separated edge types (r.name) to filter by")
	edgeSearchCmd.Flags().BoolVar(&searchIncludeInvalid, "include-invalid", false, "include invalidated facts")
	_ = edgeSearchCmd.MarkFlagRequired("query")

	entityCmd.AddCommand(entitySearchCmd)
	edgeCmd.AddCommand(edgeSearchCmd)
}

var entitySearchCmd = &cobra.Command{
	Use: "search",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.SearchEntities(searchQuery, searchLimit, parseTypes(searchTypes))
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
		res, err := c.SearchEdges(searchQuery, searchLimit, searchAsOf, parseTypes(searchTypes), searchIncludeInvalid)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}
