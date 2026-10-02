package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/chaeeundad/PFCN/internal/agent"
	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/indexer"
	"github.com/chaeeundad/PFCN/internal/record"
)

func startIndexer(ctx context.Context, a *agent.Agent) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("component", "indexer")
	x, err := indexer.Open(a, filepath.Join(a.Home(), "indexer", "index.sqlite"), log)
	if err != nil {
		return err
	}
	x.Start(ctx)
	a.RegisterAPI(func(mux *http.ServeMux) {
		mux.HandleFunc("POST /v1/indexer/rebuild", func(w http.ResponseWriter, _ *http.Request) {
			n, err := x.Rebuild()
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			json.NewEncoder(w).Encode(map[string]int{"indexed": n})
		})
	})
	addr := a.Config().Indexer.HTTP
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	srv := &http.Server{Addr: addr, Handler: x.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
		x.Close()
	}()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("explorer http", "err", err)
		}
	}()
	log.Info("explorer listening", "addr", "http://"+addr)
	return nil
}

func publishCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "publish <exec-id>",
		Short: "Publish a completed execution as a public scientific record",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var res agent.PublishResult
			if err := newClient().post("/v1/jobs/"+args[0]+"/publish", nil, &res); err != nil {
				return err
			}
			fmt.Println("Record:   ", res.RecordID)
			fmt.Println("Bundle:   ", res.Dir)
			fmt.Println("Announced to", res.Announced, "configured indexer(s); also gossiped on the records topic.")
			return nil
		},
	}
}

func reproduceCmd() *cobra.Command {
	var peers []string
	var detach bool
	var etol, ftol float64
	cmd := &cobra.Command{
		Use:   "reproduce <record-id>",
		Short: "Re-run a public record on a different worker and compare results (§26)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := newClient()
			b, _ := json.Marshal(agent.ReproduceRequest{RecordID: args[0], Peer: peers, Detach: detach, EnergyTolEV: etol, ForceTolEVA: ftol})
			res, err := c.stream("/v1/reproduce", b, func(s string) { fmt.Println(s) })
			if err != nil {
				return err
			}
			if detach {
				fmt.Println("Detached; the comparison is written when the result arrives:", res.ResultDir+"/reproduction.json")
				return nil
			}
			if err := waitJob(c, res.ExecID); err != nil {
				return err
			}
			raw, err := os.ReadFile(filepath.Join(res.ResultDir, "reproduction.json"))
			if err != nil {
				return err
			}
			var cmp record.Comparison
			json.Unmarshal(raw, &cmp)
			fmt.Println()
			fmt.Println("Reproduction:", cmp.Status)
			fmt.Printf("ΔE = %s eV (tolerance %s), max ΔF = %s eV/Å (tolerance %s)\n", cmp.EnergyDiffEV, cmp.EnergyTolEV, orDash(cmp.MaxForceDiffEVA), cmp.ForceTolEVA)
			fmt.Println("Publish it with: pumat job publish", res.ExecID)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&peers, "peer", nil, "worker multiaddr (default: discover, excluding the original worker)")
	cmd.Flags().BoolVar(&detach, "detach", false, "return after the input upload")
	cmd.Flags().Float64Var(&etol, "energy-tol", agent.DefaultEnergyTolEV, "total energy tolerance in eV")
	cmd.Flags().Float64Var(&ftol, "force-tol", agent.DefaultForceTolEVA, "force tolerance in eV/Å")
	return cmd
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func recordCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "record", Short: "Public scientific records"}
	cmd.AddCommand(&cobra.Command{
		Use:   "fetch <record-id>",
		Short: "Download and verify a record from the network (this node becomes a mirror)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var out struct {
				Manifest record.Manifest `json:"manifest"`
				Dir      string          `json:"dir"`
			}
			if err := newClient().post("/v1/records/"+args[0]+"/fetch", nil, &out); err != nil {
				return err
			}
			fmt.Println("Verified record stored in", out.Dir)
			printManifest(&out.Manifest)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "verify <record-dir>",
		Short: "Verify a downloaded record bundle offline (signature, every hash, receipt chain)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			raw, err := os.ReadFile(filepath.Join(args[0], "manifest.json"))
			if err != nil {
				return err
			}
			env, err := envelope.ParseFile(raw)
			if err != nil {
				return err
			}
			m, err := record.VerifyManifest(env)
			if err != nil {
				return err
			}
			if err := record.VerifyFiles(m, args[0]); err != nil {
				return fmt.Errorf("record INVALID: %w", err)
			}
			fmt.Println("Record:", record.ID(env))
			fmt.Printf("OK: publisher signature, %d file digests and the receipt chain verified\n", len(m.Files))
			printManifest(m)
			return nil
		},
	})
	return cmd
}

func printManifest(m *record.Manifest) {
	s := m.Summary
	fmt.Printf("Solver:      %s %s\nCalculation: %s %s (converged: %v)\nEnergy:      %s eV\nCalc ID:     %s\nPublisher:   %s\nWorker:      %s\n",
		m.Solver, m.SolverVersion, s.Formula, s.Calculation, s.Converged, orDash(s.EnergyEV), m.CalcID, m.PublisherPeerID, m.WorkerPeerID)
	if m.Comparison != nil {
		fmt.Printf("Reproduces:  %s (%s)\n", m.Comparison.Against, m.Comparison.Status)
	}
}
