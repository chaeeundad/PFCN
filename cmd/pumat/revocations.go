package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/chaeeundad/PFCN/internal/config"
	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/internal/solver"
)

// solverRevokeCmd creates or extends a signed revocation list (§13.5).
func solverRevokeCmd() *cobra.Command {
	var key, prev, out, kind, digest, signer, reason, namespace string
	var validFor time.Duration
	cmd := &cobra.Command{
		Use:   "revoke",
		Short: "Add an entry to a signed revocation list (sequence is incremented)",
		RunE: func(*cobra.Command, []string) error {
			if key == "" || out == "" || kind == "" || reason == "" {
				return errors.New("--key, --out, --kind and --reason are required")
			}
			k, err := identity.LoadKeyFile(key)
			if err != nil {
				return err
			}
			rl := &solver.RevocationList{Schema: solver.RevocationSchema, Namespace: namespace, Revoked: []solver.Revoked{}}
			if prev != "" {
				raw, err := os.ReadFile(prev)
				if err != nil {
					return err
				}
				env, err := envelope.ParseFile(raw)
				if err != nil {
					return err
				}
				// The previous list must be authentic before we extend it.
				if rl, err = (solver.Policy{TrustedSigners: signersOf(env)}).VerifyRevocations(env); err != nil {
					return err
				}
			}
			now := time.Now().UTC()
			rl.Sequence++
			rl.IssuedAt = now.Format(time.RFC3339)
			rl.NextUpdateBefore = now.Add(validFor).Format(time.RFC3339)
			rl.Revoked = append(rl.Revoked, solver.Revoked{Kind: kind, Digest: digest, Identity: signer, Reason: reason})
			env, err := solver.SignRevocations(rl, k)
			if err != nil {
				return err
			}
			raw, _ := env.MarshalFile()
			if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
				return err
			}
			fmt.Printf("Revocation list sequence %d with %d entries written to %s\n", rl.Sequence, len(rl.Revoked), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "solver signing key")
	cmd.Flags().StringVar(&prev, "from", "", "previous signed list to extend")
	cmd.Flags().StringVar(&out, "out", "", "output file")
	cmd.Flags().StringVar(&kind, "kind", "", "solver_manifest | artifact | signer")
	cmd.Flags().StringVar(&digest, "digest", "", "revoked manifest or artifact digest")
	cmd.Flags().StringVar(&signer, "signer", "", "revoked signer ID")
	cmd.Flags().StringVar(&reason, "reason", "", "critical_vulnerability | malicious_artifact | broken_scientific_output | compromised_signing_identity")
	cmd.Flags().StringVar(&namespace, "namespace", config.DefaultNamespace, "namespace the list applies to")
	cmd.Flags().DurationVar(&validFor, "valid-for", 7*24*time.Hour, "time until next_update_before")
	return cmd
}

func signersOf(env *envelope.Envelope) []string {
	var s []string
	for _, sig := range env.Signatures {
		s = append(s, sig.Signer)
	}
	return s
}

func revocationsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "revocations", Short: "Solver revocation lists"}
	cmd.AddCommand(&cobra.Command{
		Use:   "add <revocation.json>",
		Short: "Verify and install a newer signed revocation list",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			var rl solver.RevocationList
			if err := newClient().post("/v1/revocations", raw, &rl); err != nil {
				return err
			}
			fmt.Printf("Installed revocation list sequence %d (%d entries)\n", rl.Sequence, len(rl.Revoked))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the installed revocation list",
		RunE: func(*cobra.Command, []string) error {
			var rl solver.RevocationList
			if err := newClient().get("/v1/revocations", &rl); err != nil {
				return err
			}
			b, _ := json.MarshalIndent(rl, "", "  ")
			fmt.Println(string(b))
			return nil
		},
	})
	return cmd
}
