package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// updateCmd checks for a newer release (§42.2). Installing is left to the
// verifying installer: pumat never downloads and runs a binary by itself.
func updateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "update", Short: "Check for updates"}
	cmd.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Compare this version with the latest release",
		RunE: func(*cobra.Command, []string) error {
			c := &http.Client{Timeout: 15 * time.Second}
			resp, err := c.Get("https://api.github.com/repos/chaeeundad/PFCN/releases?per_page=1")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			var rel []struct {
				Tag string `json:"tag_name"`
				URL string `json:"html_url"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
				return err
			}
			if len(rel) == 0 {
				fmt.Println("No releases published yet. Current:", version)
				return nil
			}
			fmt.Println("Current:", version)
			fmt.Println("Latest: ", rel[0].Tag, rel[0].URL)
			if rel[0].Tag != version {
				fmt.Println("Update by re-running the verifying installer:")
				fmt.Println("  curl -fsSL https://raw.githubusercontent.com/chaeeundad/PFCN/main/scripts/install.sh | sh")
			}
			return nil
		},
	})
	return cmd
}
