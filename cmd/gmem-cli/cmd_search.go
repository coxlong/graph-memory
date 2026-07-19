package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	searchQuery, searchAsOf string
	searchLimit             int
	searchIncludeInvalid    bool
	searchTypes, searchMethod string
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

// validSearchMethod validates --method; empty falls back to the default "hybrid".
func validSearchMethod(m string) error {
	switch m {
	case "", "hybrid", "vector", "bm25":
		return nil
	default:
		return fmt.Errorf("--method must be hybrid|vector|bm25, got %q", m)
	}
}

func init() {
	entitySearchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	entitySearchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results")
	entitySearchCmd.Flags().StringVar(&searchTypes, "type", "", "comma-separated entity types (node labels) to filter by")
	entitySearchCmd.Flags().StringVar(&searchMethod, "method", "hybrid", "retrieval method: hybrid|vector|bm25")
	_ = entitySearchCmd.MarkFlagRequired("query")

	edgeSearchCmd.Flags().StringVar(&searchQuery, "query", "", "search query")
	edgeSearchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results")
	edgeSearchCmd.Flags().StringVar(&searchAsOf, "as-of", "", "RFC3339 point-in-time filter")
	edgeSearchCmd.Flags().StringVar(&searchTypes, "type", "", "comma-separated edge types (r.name) to filter by")
	edgeSearchCmd.Flags().StringVar(&searchMethod, "method", "hybrid", "retrieval method: hybrid|vector|bm25")
	edgeSearchCmd.Flags().BoolVar(&searchIncludeInvalid, "include-invalid", false, "include invalidated facts")
	_ = edgeSearchCmd.MarkFlagRequired("query")

	entityCmd.AddCommand(entitySearchCmd)
	edgeCmd.AddCommand(edgeSearchCmd)
}

var entitySearchCmd = &cobra.Command{
	Use: "search",
	Run: func(cmd *cobra.Command, args []string) {
		if err := validSearchMethod(searchMethod); err != nil {
			fatal(err)
		}
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.SearchEntities(searchQuery, searchLimit, parseTypes(searchTypes), searchMethod)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}

var edgeSearchCmd = &cobra.Command{
	Use: "search",
	Run: func(cmd *cobra.Command, args []string) {
		if err := validSearchMethod(searchMethod); err != nil {
			fatal(err)
		}
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		res, err := c.SearchEdges(searchQuery, searchLimit, searchAsOf, parseTypes(searchTypes), searchMethod, searchIncludeInvalid)
		if err != nil {
			fatal(err)
		}
		printJSON(res)
	},
}
