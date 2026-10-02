package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/chaeeundad/PFCN/internal/agent"
	"github.com/chaeeundad/PFCN/internal/config"
	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/internal/solver"
)

func solverCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "solver", Short: "Solver manifests and signing"}

	var out string
	keygen := &cobra.Command{
		Use:   "keygen",
		Short: "Create a solver signing key (keep it offline; its peer ID is a trust root)",
		RunE: func(*cobra.Command, []string) error {
			if out == "" {
				return errors.New("--out is required")
			}
			id, err := identity.WriteKeyFile(out)
			if err != nil {
				return err
			}
			fmt.Println("Signing key:", out)
			fmt.Println("Signer ID:  ", id.PeerID)
			fmt.Println("Add the signer ID to trust.solverSigners on nodes that should trust it.")
			return nil
		},
	}
	keygen.Flags().StringVar(&out, "out", "", "key file to create (mode 0600)")
	cmd.AddCommand(keygen)

	var key, signOut string
	sign := &cobra.Command{
		Use:   "sign <manifest.yaml>",
		Short: "Validate and sign a solver manifest",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if key == "" || signOut == "" {
				return errors.New("--key and --out are required")
			}
			src, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			m, err := solver.FromYAML(src, time.Now())
			if err != nil {
				return err
			}
			k, err := identity.LoadKeyFile(key)
			if err != nil {
				return err
			}
			env, err := solver.Sign(m, k)
			if err != nil {
				return err
			}
			raw, err := env.MarshalFile()
			if err != nil {
				return err
			}
			if err := os.WriteFile(signOut, append(raw, '\n'), 0o644); err != nil {
				return err
			}
			fmt.Println("Signed manifest:", signOut)
			fmt.Println("Signer:         ", k.PeerID)
			fmt.Println("Manifest digest:", env.SHA256())
			fmt.Println("Use this digest as solver.manifestDigest in job files.")
			return nil
		},
	}
	sign.Flags().StringVar(&key, "key", "", "solver signing key file")
	sign.Flags().StringVar(&signOut, "out", "", "output file for the signed manifest")
	cmd.AddCommand(sign)

	cmd.AddCommand(&cobra.Command{
		Use:   "add <manifest.signed.json>",
		Short: "Verify a signed manifest against local trust roots and install it",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			var res map[string]string
			if err := newClient().post("/v1/solvers", raw, &res); err != nil {
				// Allow installation while the agent is stopped.
				if !isAgentDown(err) {
					return err
				}
				v, ierr := installOffline(raw)
				if ierr != nil {
					return ierr
				}
				res = map[string]string{"name": v.Manifest.Name, "version": v.Manifest.Version, "digest": v.Digest}
			}
			fmt.Printf("Installed %s %s\nManifest digest: %s\n", res["name"], res["version"], res["digest"])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List installed, trusted solver manifests",
		RunE: func(*cobra.Command, []string) error {
			var list []struct {
				Name      string   `json:"name"`
				Version   string   `json:"version"`
				Digest    string   `json:"digest"`
				Platforms []string `json:"platforms"`
				Cached    bool     `json:"cached"`
			}
			if err := newClient().get("/v1/solvers", &list); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tVERSION\tMANIFEST DIGEST\tPLATFORMS\tIMAGE CACHED")
			for _, s := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%v\n", s.Name, s.Version, s.Digest, s.Platforms, s.Cached)
			}
			return tw.Flush()
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "verify <manifest.signed.json>",
		Short: "Verify a signed manifest against this node's trust roots",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			v, err := verifyFile(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("OK: %s %s signed by a trusted signer\nManifest digest: %s\n", v.Manifest.Name, v.Manifest.Version, v.Digest)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "inspect <manifest.signed.json>",
		Short: "Print a signed manifest's contents",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			env, err := solver.LoadFile(args[0])
			if err != nil {
				return err
			}
			if err := env.Verify(solver.Schema); err != nil {
				return err
			}
			var m solver.Manifest
			if err := env.Decode(&m); err != nil {
				return err
			}
			b, _ := json.MarshalIndent(m, "", "  ")
			fmt.Println(string(b))
			for _, s := range env.Signatures {
				fmt.Println("signed by:", s.Signer)
			}
			fmt.Println("manifest digest:", env.SHA256())
			return nil
		},
	})
	return cmd
}

func trustPolicy() (solver.Policy, error) {
	c, err := config.Load(agent.Paths{Home: home()}.Config())
	if err != nil {
		return solver.Policy{}, err
	}
	return solver.Policy{TrustedSigners: c.Trust.SolverSigners, AllowSolvers: c.Solvers.Allow}, nil
}

func verifyFile(path string) (*solver.Verified, error) {
	env, err := solver.LoadFile(path)
	if err != nil {
		return nil, err
	}
	pol, err := trustPolicy()
	if err != nil {
		return nil, err
	}
	return pol.Verify(env)
}

func installOffline(raw []byte) (*solver.Verified, error) {
	env, err := envelope.ParseFile(raw)
	if err != nil {
		return nil, err
	}
	pol, err := trustPolicy()
	if err != nil {
		return nil, err
	}
	return agent.InstallSolver(agent.Paths{Home: home()}, pol, env)
}

func isAgentDown(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cannot reach the local agent")
}
