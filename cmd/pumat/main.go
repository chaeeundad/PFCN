// Command pumat is the Pumat Federated Computing Network node and CLI
// (spec §32).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// Set at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

var homeFlag string

func home() string {
	if homeFlag != "" {
		return homeFlag
	}
	if h := os.Getenv("PUMAT_HOME"); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ".pumat"
	}
	return filepath.Join(h, ".pumat")
}

func main() {
	root := &cobra.Command{
		Use:           "pumat",
		Short:         "Pumat: share idle compute, run science, share the results",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&homeFlag, "home", "", "Pumat home directory (default $PUMAT_HOME or ~/.pumat)")
	root.AddCommand(
		versionCmd(), initCmd(), agentCmd(), statusCmd(), idCmd(),
		modeCmd("on", "Start accepting work", "available"),
		modeCmd("off", "Stop accepting new work (running jobs finish)", "paused"),
		modeCmd("pause", "Alias of off", "paused"),
		submitCmd(), jobCmd(), receiptsCmd(), receiptCmd(), ledgerCmd(),
		solverCmd(), resourcesCmd(), doctorCmd(), networkCmd(), peersCmd(), revocationsCmd(), reproduceCmd(), recordCmd(), updateCmd(),
	)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(*cobra.Command, []string) {
			fmt.Println("pumat", version)
		},
	}
}
