package main

import (
	"strings"

	"github.com/spf13/cobra"
)

var (
	communityGroup, communityName, communitySummary, communityMembers string
)

func init() {
	communityBuildCmd.Flags().StringVar(&communityGroup, "group-id", "", "group id")
	communityUpsertCmd.Flags().StringVar(&communityName, "name", "", "community name")
	communityUpsertCmd.Flags().StringVar(&communitySummary, "summary", "", "agent-written summary")
	communityUpsertCmd.Flags().StringVar(&communityMembers, "member-uuids", "", "comma-separated entity uuids")
	communityUpsertCmd.Flags().StringVar(&communityGroup, "group-id", "", "group id")
	_ = communityUpsertCmd.MarkFlagRequired("name")
	_ = communityUpsertCmd.MarkFlagRequired("summary")

	communityCmd.AddCommand(communityBuildCmd, communityUpsertCmd)
	rootCmd.AddCommand(communityCmd)
}

var communityCmd = &cobra.Command{Use: "community", Short: "Community (clustering) operations"}

var communityBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build candidate communities (grouped by entity type)",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		comms, err := c.BuildCommunities(communityGroup)
		if err != nil {
			fatal(err)
		}
		printJSON(comms)
	},
}

var communityUpsertCmd = &cobra.Command{
	Use:   "upsert",
	Short: "Write a community with agent summary and members",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		var members []string
		if communityMembers != "" {
			for _, m := range strings.Split(communityMembers, ",") {
				m = strings.TrimSpace(m)
				if m != "" {
					members = append(members, m)
				}
			}
		}
		com, err := c.UpsertCommunity(communityName, communitySummary, members, communityGroup)
		if err != nil {
			fatal(err)
		}
		printJSON(com)
	},
}
