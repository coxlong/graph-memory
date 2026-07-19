package main

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(initCmd, statusCmd)
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create graph indexes (idempotent)",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		if err := c.Init(); err != nil {
			fatal(err)
		}
		printJSON(map[string]string{"status": "ok"})
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check FalkorDB, indexes and embedding API",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		printJSON(c.Status())
	},
}
