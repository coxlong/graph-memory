package main

import (
	"github.com/spf13/cobra"
)

func init() {
	schemaCmd.AddCommand(schemaShowCmd)
	rootCmd.AddCommand(schemaCmd)
}

var schemaCmd = &cobra.Command{Use: "schema", Short: "Type schema operations"}

var schemaShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print configured entity/edge types as JSON",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		printJSON(c.Schema)
	},
}
