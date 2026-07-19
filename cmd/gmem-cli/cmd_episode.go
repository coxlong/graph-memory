package main

import (
	"github.com/spf13/cobra"
)

var episodeUUID string
var episodeLimit int

func init() {
	episodeGetCmd.Flags().StringVar(&episodeUUID, "uuid", "", "episode uuid")
	_ = episodeGetCmd.MarkFlagRequired("uuid")
	episodeListCmd.Flags().IntVar(&episodeLimit, "limit", 20, "max episodes")
	episodeCmd.AddCommand(episodeGetCmd, episodeListCmd)
	rootCmd.AddCommand(episodeCmd)
}

var episodeCmd = &cobra.Command{Use: "episode", Short: "Episode operations"}

var episodeGetCmd = &cobra.Command{
	Use: "get",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		ep, err := c.GetEpisode(episodeUUID)
		if err != nil {
			fatal(err)
		}
		printJSON(ep)
	},
}

var episodeListCmd = &cobra.Command{
	Use: "list",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		eps, err := c.ListEpisodes("", episodeLimit)
		if err != nil {
			fatal(err)
		}
		printJSON(eps)
	},
}
