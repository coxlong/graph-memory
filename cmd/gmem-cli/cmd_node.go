package main

import (
	"github.com/spf13/cobra"
)

var nodeUUID string

func init() {
	nodeDeleteCmd.Flags().StringVar(&nodeUUID, "uuid", "", "node uuid")
	_ = nodeDeleteCmd.MarkFlagRequired("uuid")
	nodeCmd.AddCommand(nodeDeleteCmd)
	rootCmd.AddCommand(nodeCmd)
}

var nodeCmd = &cobra.Command{Use: "node", Short: "Node operations"}

var nodeDeleteCmd = &cobra.Command{
	Use: "delete",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		if err := c.DeleteNode(nodeUUID); err != nil {
			fatal(err)
		}
		printJSON(map[string]string{"status": "ok"})
	},
}
