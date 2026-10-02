// Package store persists execution state and the signed local event chain in
// SQLite (spec §7.3, §15.2, §20.4).
//
// Every state transition is written together with its event in one
// transaction, before the corresponding network message is sent.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/identity"
	"github.com/chaeeundad/PFCN/internal/protocol"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS executions (
  exec_id            TEXT PRIMARY KEY,
  role               TEXT NOT NULL,
  state              TEXT NOT NULL,
  calc_id            TEXT NOT NULL,
  peer_id            TEXT NOT NULL,
  peer_addrs         TEXT NOT NULL DEFAULT '',
  job_name           TEXT NOT NULL DEFAULT '',
  mode               TEXT NOT NULL DEFAULT '',
  lease              BLOB,
  completion         BLOB,
  acceptance         BLOB,
  result_dir         TEXT NOT NULL DEFAULT '',
  error              TEXT NOT NULL DEFAULT '',
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL,
  input_deadline     INTEGER NOT NULL DEFAULT 0,
  sealed_at          INTEGER NOT NULL DEFAULT 0,
  retention_deadline INTEGER NOT NULL DEFAULT 0,
  last_confirmed_at  INTEGER NOT NULL DEFAULT 0,
  elapsed_seconds    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS executions_role_state ON executions(role, state);
CREATE TABLE IF NOT EXISTS events (
  seq      INTEGER PRIMARY KEY,
  hash     TEXT NOT NULL,
  envelope BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS kv (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);
`

// Roles.
const (
	RoleRequester = "requester"
	RoleWorker    = "worker"
)

// Execution states (§15.2).
const (
	StateRequested     = "REQUESTED"
	StateLeased        = "LEASED"
	StateInputTransfer = "INPUT_TRANSFER"
	StateRunning       = "RUNNING"
	StateSealed        = "SEALED"
	StateHeld          = "HELD"
	StateDelivered     = "DELIVERED"
	StateAcknowledged  = "ACKNOWLEDGED"
	StateCompleted     = "COMPLETED"
	StateRejected      = "REJECTED"
	StateExpired       = "EXPIRED"
	StateCancelled     = "CANCELLED"
	StateFailed        = "FAILED"
	StateWorkerLost    = "WORKER_LOST"
	StateResultExpired = "RESULT_EXPIRED"
)

// Terminal reports whether a state is final.
func Terminal(s string) bool {
	switch s {
	case StateCompleted, StateRejected, StateExpired, StateCancelled, StateFailed, StateWorkerLost, StateResultExpired:
		return true
	}
	return false
}

// Execution is one execution row.
type Execution struct {
	ExecID            string
	Role              string
	State             string
	CalcID            string
	PeerID            string
	PeerAddrs         []string
	JobName           string
	Mode              string
	Lease             *envelope.Envelope
	Completion        *envelope.Envelope
	Acceptance        *envelope.Envelope
	ResultDir         string
	Error             string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	InputDeadline     time.Time
	SealedAt          time.Time
	RetentionDeadline time.Time
	LastConfirmedAt   time.Time
	ElapsedSeconds    int64
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
	id *identity.Identity
	mu sync.Mutex // serializes event chain appends
}

// Open opens (and migrates) the database at path.
func Open(path string, id *identity.Identity) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db, id: id}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Event is one entry of the local event chain.
type Event struct {
	Schema string         `json:"schema"`
	Seq    int64          `json:"seq"`
	Prev   string         `json:"prev"`
	Kind   string         `json:"kind"`
	ExecID string         `json:"exec_id"`
	Data   map[string]any `json:"data"`
	At     string         `json:"at"`
}

// Insert creates a new execution with its first event.
func (s *Store) Insert(e *Execution, eventKind string, data map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	addrs, _ := json.Marshal(e.PeerAddrs)
	_, err = tx.Exec(`INSERT INTO executions (exec_id, role, state, calc_id, peer_id, peer_addrs, job_name, mode,
		lease, result_dir, created_at, updated_at, input_deadline) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ExecID, e.Role, e.State, e.CalcID, e.PeerID, string(addrs), e.JobName, e.Mode,
		envBytes(e.Lease), e.ResultDir, now, now, unix(e.InputDeadline))
	if err != nil {
		return fmt.Errorf("store: insert: %w", err)
	}
	if err := s.appendEvent(tx, eventKind, e.ExecID, withState(data, e.State)); err != nil {
		return err
	}
	return tx.Commit()
}

// Update describes field changes applied with a transition.
type Update struct {
	Lease             *envelope.Envelope
	Completion        *envelope.Envelope
	Acceptance        *envelope.Envelope
	ResultDir         *string
	Error             *string
	InputDeadline     *time.Time
	SealedAt          *time.Time
	RetentionDeadline *time.Time
	LastConfirmedAt   *time.Time
	ElapsedSeconds    *int64
}

// ErrStateConflict means the execution was not in an expected state.
var ErrStateConflict = errors.New("store: unexpected execution state")

// Transition moves an execution from one of from (any if empty) to to,
// applying u and appending an event atomically.
func (s *Store) Transition(execID string, from []string, to string, u Update, data map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cur string
	if err := tx.QueryRow(`SELECT state FROM executions WHERE exec_id = ?`, execID).Scan(&cur); err != nil {
		return fmt.Errorf("store: %s: %w", execID, err)
	}
	if len(from) > 0 && !contains(from, cur) {
		return fmt.Errorf("%w: %s is %s, expected %s", ErrStateConflict, execID, cur, strings.Join(from, "|"))
	}
	sets := []string{"state = ?", "updated_at = ?"}
	args := []any{to, time.Now().Unix()}
	add := func(col string, v any) { sets = append(sets, col+" = ?"); args = append(args, v) }
	if u.Lease != nil {
		add("lease", envBytes(u.Lease))
	}
	if u.Completion != nil {
		add("completion", envBytes(u.Completion))
	}
	if u.Acceptance != nil {
		add("acceptance", envBytes(u.Acceptance))
	}
	if u.ResultDir != nil {
		add("result_dir", *u.ResultDir)
	}
	if u.Error != nil {
		add("error", *u.Error)
	}
	if u.InputDeadline != nil {
		add("input_deadline", unix(*u.InputDeadline))
	}
	if u.SealedAt != nil {
		add("sealed_at", unix(*u.SealedAt))
	}
	if u.RetentionDeadline != nil {
		add("retention_deadline", unix(*u.RetentionDeadline))
	}
	if u.LastConfirmedAt != nil {
		add("last_confirmed_at", unix(*u.LastConfirmedAt))
	}
	if u.ElapsedSeconds != nil {
		add("elapsed_seconds", *u.ElapsedSeconds)
	}
	args = append(args, execID)
	if _, err := tx.Exec(`UPDATE executions SET `+strings.Join(sets, ", ")+` WHERE exec_id = ?`, args...); err != nil {
		return err
	}
	if cur != to || data != nil {
		if err := s.appendEvent(tx, "transition", execID, withState(withFrom(data, cur), to)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Touch updates observation fields without a state change or event.
func (s *Store) Touch(execID string, confirmed time.Time, elapsed int64) error {
	_, err := s.db.Exec(`UPDATE executions SET last_confirmed_at = ?, elapsed_seconds = ? WHERE exec_id = ?`,
		confirmed.Unix(), elapsed, execID)
	return err
}

const cols = `exec_id, role, state, calc_id, peer_id, peer_addrs, job_name, mode, lease, completion, acceptance,
	result_dir, error, created_at, updated_at, input_deadline, sealed_at, retention_deadline, last_confirmed_at, elapsed_seconds`

// Get returns one execution.
func (s *Store) Get(execID string) (*Execution, error) {
	rows, err := s.db.Query(`SELECT `+cols+` FROM executions WHERE exec_id = ?`, execID)
	if err != nil {
		return nil, err
	}
	list, err := scan(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("store: execution %s not found", execID)
	}
	return list[0], nil
}

// Find resolves an exact ID or a unique prefix. A bare hex prefix is matched
// against the digest part ("269cfa" finds "pumat:exec:blake3:269cfa...").
func (s *Store) Find(idOrPrefix string) (*Execution, error) {
	if !strings.Contains(idOrPrefix, ":") {
		idOrPrefix = "pumat:exec:blake3:" + idOrPrefix
	}
	rows, err := s.db.Query(`SELECT `+cols+` FROM executions WHERE exec_id = ? OR exec_id LIKE ? ORDER BY created_at DESC LIMIT 2`,
		idOrPrefix, idOrPrefix+"%")
	if err != nil {
		return nil, err
	}
	list, err := scan(rows)
	if err != nil {
		return nil, err
	}
	switch {
	case len(list) == 0:
		return nil, fmt.Errorf("execution %s not found", idOrPrefix)
	case len(list) > 1 && list[0].ExecID != idOrPrefix:
		return nil, fmt.Errorf("execution prefix %s is ambiguous", idOrPrefix)
	}
	return list[0], nil
}

// List returns executions for role ("" = all), newest first. If active, only
// non-terminal ones.
func (s *Store) List(role string, active bool) ([]*Execution, error) {
	q := `SELECT ` + cols + ` FROM executions WHERE 1=1`
	var args []any
	if role != "" {
		q += ` AND role = ?`
		args = append(args, role)
	}
	if active {
		q += ` AND state NOT IN ('COMPLETED','REJECTED','EXPIRED','CANCELLED','FAILED','WORKER_LOST','RESULT_EXPIRED')`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

func scan(rows *sql.Rows) ([]*Execution, error) {
	defer rows.Close()
	var out []*Execution
	for rows.Next() {
		var e Execution
		var addrs string
		var lease, comp, acc []byte
		var created, updated, inDL, sealed, retDL, conf int64
		if err := rows.Scan(&e.ExecID, &e.Role, &e.State, &e.CalcID, &e.PeerID, &addrs, &e.JobName, &e.Mode,
			&lease, &comp, &acc, &e.ResultDir, &e.Error, &created, &updated, &inDL, &sealed, &retDL, &conf, &e.ElapsedSeconds); err != nil {
			return nil, err
		}
		if addrs != "" {
			json.Unmarshal([]byte(addrs), &e.PeerAddrs)
		}
		var err error
		if e.Lease, err = parseEnv(lease); err != nil {
			return nil, err
		}
		if e.Completion, err = parseEnv(comp); err != nil {
			return nil, err
		}
		if e.Acceptance, err = parseEnv(acc); err != nil {
			return nil, err
		}
		e.CreatedAt, e.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
		e.InputDeadline, e.SealedAt = fromUnix(inDL), fromUnix(sealed)
		e.RetentionDeadline, e.LastConfirmedAt = fromUnix(retDL), fromUnix(conf)
		out = append(out, &e)
	}
	return out, rows.Err()
}

// --- event chain ------------------------------------------------------------

func (s *Store) appendEvent(tx *sql.Tx, kind, execID string, data map[string]any) error {
	var seq int64
	var prev string
	err := tx.QueryRow(`SELECT seq, hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&seq, &prev)
	if errors.Is(err, sql.ErrNoRows) {
		seq, prev = 0, ""
	} else if err != nil {
		return err
	}
	if data == nil {
		data = map[string]any{}
	}
	ev := Event{Schema: protocol.SchemaEvent, Seq: seq + 1, Prev: prev, Kind: kind, ExecID: execID, Data: data, At: protocol.Now(time.Now())}
	env, err := envelope.Sign(s.id, protocol.SchemaEvent, ev)
	if err != nil {
		return fmt.Errorf("store: event: %w", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO events (seq, hash, envelope) VALUES (?,?,?)`, ev.Seq, env.Digest(), raw)
	return err
}

// AppendEvent records a node-level event (e.g. mode changes).
func (s *Store) AppendEvent(kind string, data map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.appendEvent(tx, kind, "", data); err != nil {
		return err
	}
	return tx.Commit()
}

// VerifyChain checks every event signature, sequence and hash link. It
// returns the number of events and the head hash.
func (s *Store) VerifyChain() (int64, string, error) {
	rows, err := s.db.Query(`SELECT seq, hash, envelope FROM events ORDER BY seq`)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	var n int64
	prev := ""
	for rows.Next() {
		var seq int64
		var hash string
		var raw []byte
		if err := rows.Scan(&seq, &hash, &raw); err != nil {
			return n, prev, err
		}
		var env envelope.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return n, prev, fmt.Errorf("event %d: %w", seq, err)
		}
		if err := env.VerifySigners(protocol.SchemaEvent, s.id.PeerID.String()); err != nil {
			return n, prev, fmt.Errorf("event %d: %w", seq, err)
		}
		var ev Event
		if err := env.Decode(&ev); err != nil {
			return n, prev, fmt.Errorf("event %d: %w", seq, err)
		}
		if ev.Seq != n+1 || ev.Seq != seq {
			return n, prev, fmt.Errorf("event %d: sequence gap", seq)
		}
		if ev.Prev != prev {
			return n, prev, fmt.Errorf("event %d: broken hash link", seq)
		}
		if env.Digest() != hash {
			return n, prev, fmt.Errorf("event %d: stored hash mismatch", seq)
		}
		prev = hash
		n++
	}
	return n, prev, rows.Err()
}

// --- kv -------------------------------------------------------------------

func (s *Store) GetKV(k string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT v FROM kv WHERE k = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *Store) SetKV(k, v string) error {
	_, err := s.db.Exec(`INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	return err
}

// NextSequence atomically increments a counter in kv.
func (s *Store) NextSequence(k string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	err := s.db.QueryRow(`INSERT INTO kv (k, v) VALUES (?, '1') ON CONFLICT(k) DO UPDATE SET v = CAST(v AS INTEGER) + 1 RETURNING CAST(v AS INTEGER)`, k).Scan(&n)
	return n, err
}

// --- helpers --------------------------------------------------------------

func envBytes(e *envelope.Envelope) []byte {
	if e == nil {
		return nil
	}
	b, _ := json.Marshal(e)
	return b
}

func parseEnv(b []byte) (*envelope.Envelope, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var e envelope.Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromUnix(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(n, 0)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func withState(d map[string]any, state string) map[string]any {
	out := map[string]any{}
	for k, v := range d {
		out[k] = v
	}
	out["state"] = state
	return out
}

func withFrom(d map[string]any, from string) map[string]any {
	out := map[string]any{}
	for k, v := range d {
		out[k] = v
	}
	out["from"] = from
	return out
}
