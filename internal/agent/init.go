package agent

import (
	"fmt"
	"os"

	"github.com/chaeeundad/PFCN/internal/capability"
	"github.com/chaeeundad/PFCN/internal/config"
	"github.com/chaeeundad/PFCN/internal/identity"
)

// InitResult reports what Init created.
type InitResult struct {
	PeerID   string
	Hardware capability.Hardware
	Config   *config.Config
	Created  bool
}

// Init creates the home layout, identity and default config (§5.2). It is
// idempotent: an existing identity and config are kept.
func Init(home string) (*InitResult, error) {
	p := Paths{Home: home}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	for _, d := range []string{p.Work(), p.Keys(), p.Solvers()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	res := &InitResult{Hardware: capability.Detect()}
	var id *identity.Identity
	var err error
	if identity.Exists(p.Identity()) {
		id, err = identity.Load(p.Identity())
	} else {
		id, err = identity.Generate(p.Identity())
		res.Created = true
	}
	if err != nil {
		return nil, err
	}
	res.PeerID = id.PeerID.String()
	if _, err := os.Stat(p.Config()); err == nil {
		if res.Config, err = config.Load(p.Config()); err != nil {
			return nil, err
		}
		return res, nil
	}
	res.Config = config.Default(res.Hardware.Cores, res.Hardware.MemoryBytes)
	if err := res.Config.Save(p.Config()); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}
	return res, nil
}
