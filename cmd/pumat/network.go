package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/chaeeundad/PFCN/internal/agent"
)

func networkCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "network", Short: "Peer-to-peer network diagnostics"}
	cmd.AddCommand(&cobra.Command{
		Use:   "diagnose",
		Short: "Show reachability, DHT and bootstrap connectivity",
		RunE: func(*cobra.Command, []string) error {
			var v agent.NetworkView
			if err := newClient().get("/v1/network", &v); err != nil {
				return err
			}
			fmt.Println("Peer ID:        ", v.PeerID)
			fmt.Println("Reachability:   ", v.Reachability, "(Public = others can dial you; Private = relay/hole punching needed)")
			fmt.Println("DHT mode:       ", v.DHTMode)
			fmt.Println("DHT routing:    ", v.RoutingTable, "peers")
			fmt.Printf("Bootstrap:       %d configured, %d connected\n", len(v.Bootstrap), len(v.BootstrapUp))
			if len(v.Bootstrap) == 0 {
				fmt.Println("                 (none configured: only mDNS on the LAN and explicit --peer work)")
			}
			fmt.Println("LAN peers:      ", v.LANPeers, "(mDNS)")
			fmt.Println("Connected peers:", len(v.ConnectedPeer))
			return nil
		},
	})
	return cmd
}

func peersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "peers",
		Short: "List connected peers",
		RunE: func(*cobra.Command, []string) error {
			var v agent.NetworkView
			if err := newClient().get("/v1/network", &v); err != nil {
				return err
			}
			for _, p := range v.ConnectedPeer {
				fmt.Println(p)
			}
			return nil
		},
	}
}
