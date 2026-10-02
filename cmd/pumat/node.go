package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/chaeeundad/PFCN/internal/agent"
	"github.com/chaeeundad/PFCN/internal/config"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/internal/job"
	"github.com/chaeeundad/PFCN/internal/sandbox"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the node identity and default configuration",
		RunE: func(*cobra.Command, []string) error {
			res, err := agent.Init(home())
			if err != nil {
				return err
			}
			if res.Created {
				fmt.Println("Pumat node initialized")
			} else {
				fmt.Println("Pumat node already initialized")
			}
			fmt.Println()
			fmt.Println("Home:    ", home())
			fmt.Println("Peer ID: ", res.PeerID)
			fmt.Printf("CPU:      %s / %d cores\n", res.Hardware.CPUModel, res.Hardware.Cores)
			fmt.Printf("RAM:      %s\n", humanBytes(res.Hardware.MemoryBytes))
			if rt, err := sandbox.Detect(res.Config.Runtime.Engine); err == nil {
				fmt.Printf("Runtime:  %s available\n", rt.Name())
			} else {
				fmt.Println("Runtime:  none (this node can submit jobs but not run them)")
			}
			fmt.Println()
			fmt.Println("Default contribution:")
			fmt.Printf("CPU: %d cores\nRAM: %s\nGPU: 0\nStatus: paused\n", res.Config.Resources.CPU, res.Config.Resources.Memory)
			fmt.Println()
			fmt.Println("Start the agent with `pumat agent`, then run `pumat on` to begin accepting work.")
			return nil
		},
	}
}

func agentCmd() *cobra.Command {
	var debug bool
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run the node agent in the foreground (use a systemd unit in production)",
		RunE: func(*cobra.Command, []string) error {
			level := slog.LevelInfo
			if debug {
				level = slog.LevelDebug
			}
			a, err := agent.Open(agent.Options{Home: home(), Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))})
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := a.Start(ctx); err != nil {
				a.Close()
				return err
			}
			apiErr := make(chan error, 1)
			go func() { apiErr <- a.ServeAPI(ctx) }()
			select {
			case <-ctx.Done():
			case err = <-apiErr:
			}
			// SIGTERM stops new work immediately; leases stay in the store and
			// running containers are interrupted (recorded as FAILED on restart).
			a.SetMode(agent.ModePaused)
			cerr := a.Close()
			return errors.Join(err, cerr)
		},
	}
	cmd.Flags().BoolVar(&debug, "debug", false, "debug logging")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show node status",
		RunE: func(*cobra.Command, []string) error {
			var s agent.StatusView
			if err := newClient().get("/v1/status", &s); err != nil {
				return err
			}
			fmt.Println("Peer ID:   ", s.PeerID)
			fmt.Println("Mode:      ", s.Mode)
			fmt.Println("Namespace: ", s.Namespace)
			fmt.Printf("Runtime:    %s %s\n", s.Runtime, s.Platform)
			fmt.Printf("CPU:        %d cores offered (%s)\n", s.CPUOffered, s.CPUModel)
			fmt.Printf("Memory:     %s offered\n", humanBytes(s.MemOffered))
			fmt.Printf("Jobs:       %d active\n", s.RunningJobs)
			fmt.Printf("Peers:      %d connected\n", s.Peers)
			fmt.Println("Addresses (give one to requesters as --peer):")
			for _, a := range s.Addrs {
				fmt.Println("  " + a)
			}
			return nil
		},
	}
}

func idCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "id",
		Short: "Print this node's peer ID",
		RunE: func(*cobra.Command, []string) error {
			id, err := identity.Load(agent.Paths{Home: home()}.Identity())
			if err != nil {
				return err
			}
			fmt.Println(id.PeerID)
			return nil
		},
	}
}

func modeCmd(use, short, mode string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(*cobra.Command, []string) error {
			var out map[string]string
			if err := newClient().post("/v1/mode", map[string]string{"mode": mode}, &out); err != nil {
				return err
			}
			fmt.Println("Mode:", out["mode"])
			return nil
		},
	}
}

func resourcesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "resources", Short: "Show or change offered resources"}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the configured contribution limits",
		RunE: func(*cobra.Command, []string) error {
			c, err := config.Load(agent.Paths{Home: home()}.Config())
			if err != nil {
				return err
			}
			fmt.Printf("CPU:            %d cores\n", c.Resources.CPU)
			fmt.Printf("Memory:         %s\n", c.Resources.Memory)
			fmt.Printf("Disk:           %s\n", c.Resources.Disk)
			fmt.Printf("Max walltime:   %s\n", c.Jobs.MaxWalltime)
			fmt.Printf("Max concurrent: %d\n", c.Jobs.MaxConcurrent)
			fmt.Printf("Max retention:  %s\n", c.Jobs.MaxResultRetention)
			return nil
		},
	})
	var cpu int
	var mem string
	set := &cobra.Command{
		Use:   "set",
		Short: "Change contribution limits (restart the agent to apply)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := agent.Paths{Home: home()}.Config()
			c, err := config.Load(p)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("cpu") {
				c.Resources.CPU = cpu
			}
			if cmd.Flags().Changed("memory") {
				if _, err := job.ParseSize(mem); err != nil {
					return err
				}
				c.Resources.Memory = mem
			}
			if _, err := c.Policy(); err != nil {
				return err
			}
			if err := c.Save(p); err != nil {
				return err
			}
			fmt.Println("Saved. Restart the agent to apply.")
			return nil
		},
	}
	set.Flags().IntVar(&cpu, "cpu", 0, "CPU cores to offer")
	set.Flags().StringVar(&mem, "memory", "", "memory to offer, e.g. 32GiB")
	cmd.AddCommand(set)
	return cmd
}

func doctorCmd() *cobra.Command {
	var image string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check identity, configuration and the sandbox runtime",
		RunE: func(*cobra.Command, []string) error {
			p := agent.Paths{Home: home()}
			ok := true
			check := func(name string, err error, detail string) {
				if err != nil {
					ok = false
					fmt.Printf("FAIL  %-22s %v\n", name, err)
					return
				}
				fmt.Printf("ok    %-22s %s\n", name, detail)
			}
			id, err := identity.Load(p.Identity())
			check("identity", err, func() string {
				if id != nil {
					return id.PeerID.String()
				}
				return ""
			}())
			cfg, err := config.Load(p.Config())
			check("config", err, p.Config())
			if cfg == nil {
				return errors.New("fix the configuration first")
			}
			check("solver trust roots", func() error {
				if len(cfg.Trust.SolverSigners) == 0 {
					return errors.New("no trusted solver signers: this node trusts nothing")
				}
				return nil
			}(), strings.Join(cfg.Trust.SolverSigners, ", "))
			if os.Getuid() == 0 {
				check("non-root", errors.New("running as root; use a dedicated unprivileged user"), "")
			} else {
				check("non-root", nil, fmt.Sprintf("uid %d", os.Getuid()))
			}
			rt, err := sandbox.Detect(cfg.Runtime.Engine)
			check("container engine", err, func() string {
				if rt != nil {
					return rt.Name()
				}
				return ""
			}())
			if rt != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				plat, err := rt.Platform(ctx)
				check("runtime platform", err, plat)
				if image != "" {
					out, err := exec.CommandContext(ctx, rt.Name(), "run", "--rm", "--network", "none", "--read-only",
						"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--entrypoint", "/bin/sh", image,
						"-c", "id -u; (getent hosts example.com || echo no-network)").CombinedOutput()
					res := strings.Fields(string(out))
					switch {
					case err != nil:
						check("sandbox smoke test", fmt.Errorf("%v: %s", err, out), "")
					case len(res) < 2 || res[len(res)-1] != "no-network":
						check("sandbox smoke test", errors.New("container could resolve names: network is not isolated"), "")
					default:
						check("sandbox smoke test", nil, "network denied, uid "+res[0])
					}
				}
			}
			if !ok {
				return errors.New("some checks failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "also run a sandbox smoke test with this image")
	return cmd
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
