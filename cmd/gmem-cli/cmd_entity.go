package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

var (
	entityUUID, entityName, entitySummary, entityAttrs, entityFrom, entityTo string
	entityReplace                                                                  bool
)

func init() {
	entityGetCmd.Flags().StringVar(&entityUUID, "uuid", "", "entity uuid")
	_ = entityGetCmd.MarkFlagRequired("uuid")

	entityUpdateCmd.Flags().StringVar(&entityUUID, "uuid", "", "entity uuid")
	entityUpdateCmd.Flags().StringVar(&entityName, "name", "", "new name")
	entityUpdateCmd.Flags().StringVar(&entitySummary, "summary", "", "new summary")
	entityUpdateCmd.Flags().StringVar(&entityAttrs, "attributes", "", "attributes JSON")
	entityUpdateCmd.Flags().BoolVar(&entityReplace, "replace", false, "replace attributes instead of merge")
	_ = entityUpdateCmd.MarkFlagRequired("uuid")

	entityMergeCmd.Flags().StringVar(&entityFrom, "from", "", "source entity uuid (deleted)")
	entityMergeCmd.Flags().StringVar(&entityTo, "to", "", "target entity uuid (kept)")
	_ = entityMergeCmd.MarkFlagRequired("from")
	_ = entityMergeCmd.MarkFlagRequired("to")

	entityCmd.AddCommand(entityGetCmd, entityUpdateCmd, entityMergeCmd)
	rootCmd.AddCommand(entityCmd)
}

var entityCmd = &cobra.Command{Use: "entity", Short: "Entity operations"}

var entityGetCmd = &cobra.Command{
	Use: "get",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		e, err := c.GetEntity(entityUUID)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}

var entityUpdateCmd = &cobra.Command{
	Use: "update",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		var attrs map[string]any
		if entityAttrs != "" {
			if err := json.Unmarshal([]byte(entityAttrs), &attrs); err != nil {
				fatal(err)
			}
		}
		e, err := c.UpdateEntity(entityUUID, entityName, entitySummary, attrs, entityReplace)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}

var entityMergeCmd = &cobra.Command{
	Use: "merge",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		e, err := c.MergeEntities(entityFrom, entityTo)
		if err != nil {
			fatal(err)
		}
		printJSON(e)
	},
}
