package main

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/chaeeundad/PFCN/internal/store"
)

func reputationCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reputation",
		Short: "Show local per-peer history (separate dimensions, not a global score)",
		RunE: func(*cobra.Command, []string) error {
			var stats []*store.PeerStats
			if err := newClient().get("/v1/reputation", &stats); err != nil {
				return err
			}
			sort.Slice(stats, func(i, j int) bool { return stats[i].LastSeenAt > stats[j].LastSeenAt })
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "PEER\tAS\tTOTAL\tCOMPLETED\tFAILED\tLOST\tDISPUTED\tREJECTED\tDECAYED CORE-S\tLAST SEEN")
			for _, s := range stats {
				as := "worker for them"
				if s.Role == store.RoleRequester {
					as = "their requester"
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%.0f\t%s\n", shortID(s.PeerID), as, s.Total, s.Completed, s.Failed, s.Lost, s.Disputed, s.Rejected, s.WallUsage, s.LastSeenAt)
			}
			return tw.Flush()
		},
	}
}
