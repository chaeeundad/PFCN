package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/chaeeundad/PFCN/internal/agent"
	"github.com/chaeeundad/PFCN/internal/protocol"
	"github.com/chaeeundad/PFCN/internal/qeparse"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

func receiptsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "receipts", Short: "List compute receipts"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List executions that produced a receipt",
		RunE: func(*cobra.Command, []string) error {
			var jobs []agent.JobView
			if err := newClient().get("/v1/jobs", &jobs); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "RECEIPT\tROLE\tSTATE\tOUTCOME\tVERDICT\tPEER")
			for _, j := range jobs {
				if j.ReceiptID == "" {
					continue
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", contentid.Short(j.ReceiptID), j.Role, j.State, j.Outcome, j.Verdict, shortID(j.Peer))
			}
			return tw.Flush()
		},
	})
	return cmd
}

func receiptCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "receipt", Short: "Show or verify one compute receipt"}
	var asJSON bool
	show := &cobra.Command{
		Use:   "show <receipt-or-exec-id>",
		Short: "Show a receipt (use --json to export it for third-party verification)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var rv agent.ReceiptView
			if err := newClient().get("/v1/receipts/"+args[0], &rv); err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rv)
			}
			return printReceipt(&rv)
		},
	}
	show.Flags().BoolVar(&asJSON, "json", false, "print the signed statements as JSON")
	cmd.AddCommand(show)
	cmd.AddCommand(&cobra.Command{
		Use:   "verify <receipt-id | receipt.json>",
		Short: "Verify every signature and binding of a receipt",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var rv agent.ReceiptView
			if strings.HasSuffix(args[0], ".json") {
				raw, err := os.ReadFile(args[0])
				if err != nil {
					return err
				}
				if err := json.Unmarshal(raw, &rv); err != nil {
					return err
				}
			} else if err := newClient().get("/v1/receipts/"+args[0], &rv); err != nil {
				return err
			}
			return printReceipt(&rv)
		},
	})
	return cmd
}

func printReceipt(rv *agent.ReceiptView) error {
	v, err := protocol.VerifyReceipt(rv.Lease, rv.Completion, rv.Acceptance)
	if err != nil {
		return fmt.Errorf("receipt INVALID: %w", err)
	}
	c := v.Completion
	fmt.Println("Receipt:      ", v.ReceiptID)
	fmt.Println("State:        ", v.State, "(all signatures and bindings verified)")
	fmt.Println("Execution:    ", c.ExecID)
	fmt.Println("Calculation:  ", c.CalcID)
	fmt.Println("Requester:    ", c.RequesterPeerID)
	fmt.Println("Worker:       ", c.WorkerPeerID)
	fmt.Println("Solver:       ", c.SolverManifestDigest)
	fmt.Println("Artifact:     ", c.ArtifactDigest, "("+c.Platform+")")
	fmt.Println("Input bundle: ", c.InputBundleDigest)
	fmt.Println("Output bundle:", c.OutputBundleDigest)
	fmt.Println("Outcome:      ", c.Outcome, fmt.Sprintf("(exit %d, %ds wall, %d cores)", c.ExitCode, c.ResourceClaim.WallSeconds, c.ResourceClaim.CPUMillicores/1000))
	if a := v.Acceptance; a != nil {
		fmt.Println("Verdict:      ", a.Verdict)
		fmt.Println("Parser:       ", a.ParserDigest)
		fmt.Println("Parsed result:", a.ParsedResultDigest)
	}
	return nil
}

func ledgerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ledger", Short: "Local signed event chain"}
	cmd.AddCommand(&cobra.Command{
		Use:   "verify",
		Short: "Verify the local event chain",
		RunE: func(*cobra.Command, []string) error {
			var out struct {
				Events int64  `json:"events"`
				Head   string `json:"head"`
				OK     bool   `json:"ok"`
				Error  string `json:"error"`
			}
			if err := newClient().get("/v1/ledger/verify", &out); err != nil {
				return err
			}
			if !out.OK {
				return errors.New("event chain INVALID: " + out.Error)
			}
			fmt.Printf("Event chain OK: %d events, head %s\n", out.Events, out.Head)
			return nil
		},
	})
	return cmd
}

func resultSummary(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var r qeparse.Result
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Converged:     %v (%d SCF steps)\n", r.Converged, r.SCFSteps)
	if r.Energy != nil {
		fmt.Fprintf(&b, "Total energy:  %.6f %s\n", r.Energy.Value, r.Energy.Unit)
	}
	if r.FermiEnergy != nil {
		fmt.Fprintf(&b, "Fermi energy:  %.4f %s\n", r.FermiEnergy.Value, r.FermiEnergy.Unit)
	}
	return b.String(), nil
}
