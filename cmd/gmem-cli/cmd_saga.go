package main

import (
	"github.com/coxlong/graph-memory/pkg/gmem"
	"github.com/spf13/cobra"
)

var (
	sagaUUID, sagaName, sagaSummary, sagaGroupID, sagaFirst, sagaLast, sagaLSA, sagaLSEVA string
)

func init() {
	sagaCreateCmd.Flags().StringVar(&sagaName, "name", "", "saga name")
	sagaCreateCmd.Flags().StringVar(&sagaSummary, "summary", "", "initial summary")
	sagaCreateCmd.Flags().StringVar(&sagaFirst, "first-episode-uuid", "", "first episode uuid")
	sagaCreateCmd.Flags().StringVar(&sagaLast, "last-episode-uuid", "", "last episode uuid")
	sagaCreateCmd.Flags().StringVar(&sagaLSEVA, "last-summarized-episode-valid-at", "", "RFC3339 valid_at of last summarized episode")
	sagaCreateCmd.Flags().StringVar(&sagaGroupID, "group-id", "", "group id")
	_ = sagaCreateCmd.MarkFlagRequired("name")

	sagaGetCmd.Flags().StringVar(&sagaUUID, "uuid", "", "saga uuid")
	_ = sagaGetCmd.MarkFlagRequired("uuid")

	sagaUpdateCmd.Flags().StringVar(&sagaUUID, "uuid", "", "saga uuid")
	sagaUpdateCmd.Flags().StringVar(&sagaSummary, "summary", "", "new summary")
	sagaUpdateCmd.Flags().StringVar(&sagaLast, "last-episode-uuid", "", "new last episode uuid")
	sagaUpdateCmd.Flags().StringVar(&sagaLSA, "last-summarized-at", "", "RFC3339 time of summarization")
	sagaUpdateCmd.Flags().StringVar(&sagaLSEVA, "last-summarized-episode-valid-at", "", "RFC3339 valid_at of last summarized episode")
	_ = sagaUpdateCmd.MarkFlagRequired("uuid")

	sagaCmd.AddCommand(sagaCreateCmd, sagaGetCmd, sagaUpdateCmd)
	rootCmd.AddCommand(sagaCmd)
}

var sagaCmd = &cobra.Command{Use: "saga", Short: "Saga (incremental summarization watermark) operations"}

var sagaCreateCmd = &cobra.Command{
	Use: "create",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		s, err := c.CreateSaga(&gmem.Saga{
			Name:                            sagaName,
			Summary:                         sagaSummary,
			GroupID:                         sagaGroupID,
			FirstEpisodeUUID:                sagaFirst,
			LastEpisodeUUID:                 sagaLast,
			LastSummarizedEpisodeValidAt:    sagaLSEVA,
		})
		if err != nil {
			fatal(err)
		}
		printJSON(s)
	},
}

var sagaGetCmd = &cobra.Command{
	Use: "get",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		s, err := c.GetSaga(sagaUUID)
		if err != nil {
			fatal(err)
		}
		printJSON(s)
	},
}

var sagaUpdateCmd = &cobra.Command{
	Use: "update",
	Run: func(cmd *cobra.Command, args []string) {
		c, err := loadClient()
		if err != nil {
			fatal(err)
		}
		s, err := c.UpdateSaga(sagaUUID, sagaSummary, sagaLast, sagaLSA, sagaLSEVA)
		if err != nil {
			fatal(err)
		}
		printJSON(s)
	},
}
