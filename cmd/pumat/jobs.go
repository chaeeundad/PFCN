package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/chaeeundad/PFCN/internal/agent"
	"github.com/chaeeundad/PFCN/internal/store"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

func submitCmd() *cobra.Command {
	var peers []string
	var detach bool
	var retention string
	cmd := &cobra.Command{
		Use:   "submit <job.yaml>",
		Short: "Submit a job to a worker",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			c := newClient()
			res, err := c.submit(agent.SubmitRequest{JobPath: path, Peer: peers, Detach: detach, Retention: retention}, func(s string) { fmt.Println(s) })
			if err != nil {
				return err
			}
			if res.Mode == "detached" {
				fmt.Println()
				fmt.Println("Detached. The worker keeps the sealed result for the lease retention window.")
				fmt.Println("The local agent fetches it automatically while it is online, or run:")
				fmt.Println("  pumat job fetch " + res.ExecID)
				return nil
			}
			return waitJob(c, res.ExecID)
		},
	}
	cmd.Flags().StringArrayVar(&peers, "peer", nil, "worker multiaddr ending in /p2p/<peer-id> (repeatable); omit to discover workers via DHT/mDNS")
	cmd.Flags().BoolVar(&detach, "detach", false, "return after the input upload; fetch the result later")
	cmd.Flags().StringVar(&retention, "retention", "", "requested result retention, e.g. 24h")
	return cmd
}

func waitJob(c *client, execID string) error {
	last := ""
	for {
		var j agent.JobView
		if err := c.get("/v1/jobs/"+execID, &j); err != nil {
			return err
		}
		line := j.State
		if j.State == store.StateRunning && j.ElapsedSeconds > 0 {
			line += " (" + humanDuration(time.Duration(j.ElapsedSeconds)*time.Second) + ")"
		}
		if line != last {
			fmt.Println(stateMessage(j.State, line))
			last = line
		}
		if store.Terminal(j.State) {
			return printOutcome(j)
		}
		time.Sleep(2 * time.Second)
	}
}

func stateMessage(state, line string) string {
	switch state {
	case store.StateRunning:
		return "Running... " + strings.TrimPrefix(line, state)
	case store.StateSealed:
		return "Result sealed by worker. Downloading..."
	case store.StateDelivered:
		return "Result decrypted and hash verified. Parsing locally..."
	case store.StateAcknowledged:
		return "Receipt acknowledgment signed."
	}
	return line
}

func printOutcome(j agent.JobView) error {
	fmt.Println()
	fmt.Println("Execution:", j.ExecID)
	fmt.Println("State:    ", j.State)
	if j.ReceiptID != "" {
		fmt.Println("Receipt:  ", j.ReceiptID)
		fmt.Println("Outcome:  ", j.Outcome)
	}
	if j.Verdict != "" {
		fmt.Println("Verdict:  ", j.Verdict)
	}
	if j.Error != "" {
		fmt.Println("Error:    ", j.Error)
	}
	if j.State == store.StateCompleted && j.ResultDir != "" {
		fmt.Println("Result:   ", j.ResultDir)
		if summary, err := resultSummary(filepath.Join(j.ResultDir, "parsed", "result.json")); err == nil {
			fmt.Print(summary)
		}
		return nil
	}
	return fmt.Errorf("execution ended in %s", j.State)
}

func jobCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "job", Short: "Inspect and manage executions"}
	var pending bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List executions",
		RunE: func(*cobra.Command, []string) error {
			path := "/v1/jobs"
			if pending {
				path += "?pending=1"
			}
			var jobs []agent.JobView
			if err := newClient().get(path, &jobs); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "EXECUTION\tROLE\tSTATE\tJOB\tPEER\tUPDATED")
			for _, j := range jobs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", contentid.Short(j.ExecID), j.Role, j.State, j.JobName, shortID(j.Peer), j.UpdatedAt)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&pending, "pending", false, "only executions that are not finished")
	cmd.AddCommand(list)
	cmd.AddCommand(publishCmd())
	cmd.AddCommand(&cobra.Command{
		Use:   "cancel <exec-id>",
		Short: "Cancel an execution that has not been sealed yet",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := newClient().post("/v1/jobs/"+args[0]+"/cancel", nil, nil); err != nil {
				return err
			}
			fmt.Println("Cancelled. The worker stopped the job and deleted its workspace.")
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "status <exec-id>",
		Short: "Show the last known state of an execution",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var j agent.JobView
			if err := newClient().get("/v1/jobs/"+args[0], &j); err != nil {
				return err
			}
			fmt.Println("Execution:", j.ExecID)
			fmt.Println("Role:     ", j.Role)
			fmt.Println("Job:      ", j.JobName)
			fmt.Println("Peer:     ", j.Peer)
			fmt.Println("Mode:     ", j.Mode)
			state := j.State
			if j.LastConfirmedAt != "" && !store.Terminal(j.State) {
				if t, err := time.Parse(time.RFC3339, j.LastConfirmedAt); err == nil {
					state += " (last confirmed " + humanDuration(time.Since(t)) + " ago)"
				}
			}
			fmt.Println("State:    ", state)
			if j.ReceiptID != "" {
				fmt.Println("Receipt:  ", j.ReceiptID)
				fmt.Println("Outcome:  ", j.Outcome)
			}
			if j.RetentionUntil != "" {
				fmt.Println("Held until:", j.RetentionUntil)
			}
			if j.ResultDir != "" {
				fmt.Println("Result:   ", j.ResultDir)
			}
			if j.Error != "" {
				fmt.Println("Error:    ", j.Error)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "wait <exec-id>",
		Short: "Wait until an execution finishes",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := newClient()
			var j agent.JobView
			if err := c.get("/v1/jobs/"+args[0], &j); err != nil {
				return err
			}
			c.post("/v1/jobs/"+j.ExecID+"/fetch", nil, nil)
			return waitJob(c, j.ExecID)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "fetch <exec-id>",
		Short: "Try to fetch a held result now",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var out struct {
				Job        agent.JobView `json:"job"`
				State      string        `json:"state"`
				FetchError string        `json:"fetch_error"`
			}
			if err := newClient().post("/v1/jobs/"+args[0]+"/fetch", nil, &out); err != nil {
				return err
			}
			if out.FetchError != "" {
				fmt.Println("Fetch attempt failed:", out.FetchError)
			}
			if store.Terminal(out.Job.State) {
				return printOutcome(out.Job)
			}
			fmt.Println("State:", out.Job.State)
			if out.FetchError != "" {
				return errors.New("result not fetched yet; the agent keeps retrying")
			}
			return nil
		},
	})
	return cmd
}

func shortID(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:6] + "…" + s[len(s)-4:]
}
