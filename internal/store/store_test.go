package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/chaeeundad/PFCN/internal/identity"
)

func TestTransitionsAndChain(t *testing.T) {
	dir := t.TempDir()
	id, _ := identity.Generate(filepath.Join(dir, "id"))
	s, err := Open(filepath.Join(dir, "db", "pumat.sqlite"), id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	e := &Execution{ExecID: "pumat:exec:blake3:aaaa", Role: RoleWorker, State: StateLeased, CalcID: "c", PeerID: "p"}
	if err := s.Insert(e, "lease", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(e.ExecID, []string{StateLeased}, StateRunning, Update{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(e.ExecID, []string{StateLeased}, StateSealed, Update{}, nil); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("expected state conflict, got %v", err)
	}
	got, err := s.Find("pumat:exec:blake3:aa")
	if err != nil || got.State != StateRunning {
		t.Fatalf("%+v %v", got, err)
	}
	active, _ := s.List(RoleWorker, true)
	if len(active) != 1 {
		t.Fatal("expected one active execution")
	}
	if err := s.AppendEvent("mode", map[string]any{"mode": "available"}); err != nil {
		t.Fatal(err)
	}
	n, head, err := s.VerifyChain()
	if err != nil || n != 3 || head == "" {
		t.Fatalf("chain: %d %s %v", n, head, err)
	}

	// Tamper with an event: verification must fail.
	if _, err := s.db.Exec(`UPDATE events SET hash = 'blake3:00' WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.VerifyChain(); err == nil {
		t.Fatal("tampered chain must fail verification")
	}

	a, _ := s.NextSequence("cap")
	b, _ := s.NextSequence("cap")
	if a != 1 || b != 2 {
		t.Fatalf("sequence %d %d", a, b)
	}
}
