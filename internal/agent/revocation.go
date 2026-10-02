package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/solver"
)

// Revocation lists (§13.5): the newest verified list is kept in
// <home>/revocation.json. Lower sequences are ignored (rollback protection).

const (
	revocationRefresh = time.Hour
	revocationGrace   = 48 * time.Hour
)

func (p Paths) Revocations() string { return filepath.Join(p.Home, "revocation.json") }

// loadRevocations reads the persisted list, if any.
func (a *Agent) loadRevocations() error {
	raw, err := os.ReadFile(a.paths.Revocations())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	env, err := envelope.ParseFile(raw)
	if err != nil {
		return err
	}
	rl, err := a.Trust().VerifyRevocations(env)
	if err != nil {
		return fmt.Errorf("stored revocation list: %w", err)
	}
	a.mu.Lock()
	a.revocations = rl
	a.mu.Unlock()
	return nil
}

// InstallRevocations verifies and installs a newer revocation list.
func (a *Agent) InstallRevocations(env *envelope.Envelope) (*solver.RevocationList, error) {
	rl, err := a.Trust().VerifyRevocations(env)
	if err != nil {
		return nil, err
	}
	if rl.Namespace != "" && rl.Namespace != a.cfg.Namespace {
		return nil, fmt.Errorf("revocation list is for namespace %s", rl.Namespace)
	}
	a.mu.Lock()
	cur := a.revocations
	a.mu.Unlock()
	if cur != nil && rl.Sequence <= cur.Sequence {
		if rl.Sequence == cur.Sequence {
			return cur, nil
		}
		return nil, fmt.Errorf("revocation list sequence %d is older than installed %d", rl.Sequence, cur.Sequence)
	}
	raw, err := env.MarshalFile()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(a.paths.Revocations(), raw, 0o644); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.revocations = rl
	a.mu.Unlock()
	a.store.AppendEvent("revocation_list", map[string]any{"sequence": rl.Sequence, "entries": len(rl.Revoked)})
	a.log.Info("revocation list installed", "sequence", rl.Sequence, "entries", len(rl.Revoked))
	return rl, nil
}

// Revocations returns the installed list (nil if none).
func (a *Agent) Revocations() *solver.RevocationList {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.revocations
}

// checkRevocation enforces the installed list and the staleness policy.
func (a *Agent) checkRevocation(v *solver.Verified) error {
	rl := a.Revocations()
	if a.cfg.Trust.RequireRevocationList && rl.Stale(time.Now(), revocationGrace) {
		return errors.New("revocation list missing or stale; refusing new public work until it is refreshed (§13.5)")
	}
	return rl.Check(v)
}

// revocationLoop refreshes the list from trust.revocationURL.
func (a *Agent) revocationLoop(ctx context.Context) {
	defer a.wg.Done()
	url := a.cfg.Trust.RevocationURL
	if url == "" {
		return
	}
	t := time.NewTicker(revocationRefresh)
	defer t.Stop()
	for {
		if err := a.fetchRevocations(ctx, url); err != nil {
			a.log.Warn("revocation refresh failed", "url", url, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *Agent) fetchRevocations(ctx context.Context, url string) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.New("trust.revocationURL must be https")
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	env, err := envelope.ParseFile(raw)
	if err != nil {
		return err
	}
	_, err = a.InstallRevocations(env)
	return err
}
