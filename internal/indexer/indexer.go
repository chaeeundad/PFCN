// Package indexer is the disposable public explorer (spec §25). It indexes
// verified public records into SQLite and serves search pages and a REST API.
// Deleting its database loses nothing: Rebuild re-indexes every record the
// node mirrors, and records can be re-fetched from the network.
package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	_ "modernc.org/sqlite"

	"github.com/chaeeundad/PFCN/internal/agent"
	"github.com/chaeeundad/PFCN/internal/envelope"
	"github.com/chaeeundad/PFCN/internal/job"
	"github.com/chaeeundad/PFCN/internal/record"
	"github.com/chaeeundad/PFCN/pkg/contentid"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS records (
  record_id         TEXT PRIMARY KEY,
  calc_id           TEXT NOT NULL,
  exec_id           TEXT NOT NULL,
  solver            TEXT NOT NULL,
  solver_version    TEXT NOT NULL,
  formula           TEXT NOT NULL,
  elements          TEXT NOT NULL,
  calculation       TEXT NOT NULL,
  converged         INTEGER NOT NULL,
  energy_ev         REAL,
  fermi_ev          REAL,
  ecutwfc           TEXT NOT NULL,
  kpoints           TEXT NOT NULL,
  license           TEXT NOT NULL,
  publisher         TEXT NOT NULL,
  worker            TEXT NOT NULL,
  created_at        TEXT NOT NULL,
  reproduces        TEXT NOT NULL,
  comparison_status TEXT NOT NULL,
  energy_diff_ev    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS records_calc ON records(calc_id);
CREATE INDEX IF NOT EXISTS records_formula ON records(formula);
`

// Indexer indexes records announced on the network.
type Indexer struct {
	a     *agent.Agent
	db    *sql.DB
	log   *slog.Logger
	queue chan pending
	mu    sync.Mutex
	seen  map[string]bool
}

type pending struct {
	recordID string
	from     []peer.ID
}

// Open opens the index database.
func Open(a *agent.Agent, dbPath string, log *slog.Logger) (*Indexer, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, err
	}
	return &Indexer{a: a, db: db, log: log, queue: make(chan pending, 1024), seen: map[string]bool{}}, nil
}

func (x *Indexer) Close() error { return x.db.Close() }

// Start subscribes to announcements, indexes local records, and processes
// the fetch queue until ctx is done.
func (x *Indexer) Start(ctx context.Context) {
	x.a.OnAnnouncement(func(ann *record.Announcement, _ *envelope.Envelope, from peer.ID) {
		if ann.Visibility != job.VisibilityPublic {
			return
		}
		hints := []peer.ID{from}
		if p, err := peer.Decode(ann.PublisherPeerID); err == nil && p != from {
			hints = append(hints, p)
		}
		select {
		case x.queue <- pending{recordID: ann.RecordID, from: hints}:
		default:
			x.log.Warn("indexer queue full; dropping announcement", "record", ann.RecordID)
		}
	})
	go func() {
		if n, err := x.Rebuild(); err != nil {
			x.log.Warn("initial index", "err", err)
		} else {
			x.log.Info("indexed local records", "count", n)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-x.queue:
				x.mu.Lock()
				done := x.seen[p.recordID]
				x.mu.Unlock()
				if done {
					continue
				}
				fctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
				_, err := x.a.FetchRecord(fctx, p.recordID, p.from...)
				cancel()
				if err != nil {
					x.log.Warn("record fetch failed", "record", p.recordID, "err", err)
					continue
				}
				if err := x.Index(p.recordID); err != nil {
					x.log.Warn("index failed", "record", p.recordID, "err", err)
				}
			}
		}
	}()
}

// Rebuild drops the index and re-indexes every locally held record.
func (x *Indexer) Rebuild() (int, error) {
	if _, err := x.db.Exec(`DELETE FROM records`); err != nil {
		return 0, err
	}
	x.mu.Lock()
	x.seen = map[string]bool{}
	x.mu.Unlock()
	entries, err := os.ReadDir(x.a.RecordsDir())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), ".partial") {
			continue
		}
		id := contentid.Typed(contentid.KindRecord, "blake3:"+e.Name())
		if err := x.Index(id); err != nil {
			x.log.Warn("skipping record", "record", id, "err", err)
			continue
		}
		n++
	}
	return n, nil
}

// Index verifies a local record and upserts it. Unlisted records are never
// indexed (§22.3).
func (x *Indexer) Index(recordID string) error {
	_, m, err := x.a.LocalRecord(recordID)
	if err != nil {
		return err
	}
	dir, _ := x.a.RecordDir(recordID)
	if err := record.VerifyFiles(m, dir); err != nil {
		return err
	}
	if m.Visibility != job.VisibilityPublic {
		return nil
	}
	s := m.Summary
	status, diff := "", ""
	if m.Comparison != nil {
		status, diff = m.Comparison.Status, m.Comparison.EnergyDiffEV
	}
	_, err = x.db.Exec(`INSERT OR REPLACE INTO records VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		recordID, m.CalcID, m.ExecID, m.Solver, m.SolverVersion, s.Formula, ","+strings.Join(s.Elements, ",")+",",
		s.Calculation, s.Converged, nullFloat(s.EnergyEV), nullFloat(s.FermiEV), s.Ecutwfc, s.Kpoints, m.License,
		m.PublisherPeerID, m.WorkerPeerID, m.CreatedAt, m.Reproduces, status, diff)
	if err == nil {
		x.mu.Lock()
		x.seen[recordID] = true
		x.mu.Unlock()
		x.log.Info("indexed record", "record", recordID, "formula", s.Formula)
	}
	return err
}

func nullFloat(s string) any {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return nil
}

// Row is a search result.
type Row struct {
	RecordID     string   `json:"record_id"`
	CalcID       string   `json:"calc_id"`
	ExecID       string   `json:"exec_id"`
	Solver       string   `json:"solver"`
	Version      string   `json:"solver_version"`
	Formula      string   `json:"formula"`
	Elements     []string `json:"elements"`
	Calculation  string   `json:"calculation"`
	Converged    bool     `json:"converged"`
	EnergyEV     *float64 `json:"energy_ev"`
	FermiEV      *float64 `json:"fermi_ev"`
	Ecutwfc      string   `json:"ecutwfc_ry"`
	Kpoints      string   `json:"kpoints"`
	License      string   `json:"license"`
	Publisher    string   `json:"publisher"`
	Worker       string   `json:"worker"`
	CreatedAt    string   `json:"created_at"`
	Reproduces   string   `json:"reproduces,omitempty"`
	Comparison   string   `json:"comparison_status,omitempty"`
	EnergyDiffEV string   `json:"energy_diff_ev,omitempty"`
}

// Query filters records (§25.3).
type Query struct {
	Solver, Element, Formula, Calculation, CalcID string
	Converged                                     *bool
	Limit                                         int
}

func (x *Indexer) Search(q Query) ([]Row, error) {
	sqlq := `SELECT record_id, calc_id, exec_id, solver, solver_version, formula, elements, calculation, converged,
		energy_ev, fermi_ev, ecutwfc, kpoints, license, publisher, worker, created_at, reproduces, comparison_status, energy_diff_ev
		FROM records WHERE 1=1`
	var args []any
	add := func(cond string, v any) { sqlq += " AND " + cond; args = append(args, v) }
	if q.Solver != "" {
		add("solver = ?", q.Solver)
	}
	if q.Element != "" {
		add("elements LIKE ?", "%,"+q.Element+",%")
	}
	if q.Formula != "" {
		add("formula = ?", q.Formula)
	}
	if q.Calculation != "" {
		add("calculation = ?", q.Calculation)
	}
	if q.CalcID != "" {
		add("calc_id = ?", q.CalcID)
	}
	if q.Converged != nil {
		add("converged = ?", *q.Converged)
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	sqlq += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", q.Limit)
	rows, err := x.db.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		var els string
		var e, f sql.NullFloat64
		if err := rows.Scan(&r.RecordID, &r.CalcID, &r.ExecID, &r.Solver, &r.Version, &r.Formula, &els, &r.Calculation,
			&r.Converged, &e, &f, &r.Ecutwfc, &r.Kpoints, &r.License, &r.Publisher, &r.Worker, &r.CreatedAt,
			&r.Reproduces, &r.Comparison, &r.EnergyDiffEV); err != nil {
			return nil, err
		}
		r.Elements = strings.FieldsFunc(els, func(c rune) bool { return c == ',' })
		if e.Valid {
			r.EnergyEV = &e.Float64
		}
		if f.Valid {
			r.FermiEV = &f.Float64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Reproducibility summarizes all executions of one calculation (§26.3).
// Agreement is never presented as scientific correctness.
type Reproducibility struct {
	CalcID     string `json:"calc_id"`
	Executions int    `json:"executions"`
	Status     string `json:"status"`
}

func (x *Indexer) Reproducibility(calcID string) (Reproducibility, error) {
	rows, err := x.Search(Query{CalcID: calcID, Limit: 500})
	if err != nil {
		return Reproducibility{}, err
	}
	r := Reproducibility{CalcID: calcID, Executions: len(rows), Status: record.StatusUnverified}
	rank := map[string]int{record.StatusInsufficientMetadata: 1, record.StatusReproducedTolerance: 2, record.StatusReproducedExact: 3, record.StatusDivergent: 4}
	for _, row := range rows {
		if rank[row.Comparison] > rank[r.Status] {
			r.Status = row.Comparison
		}
	}
	return r, nil
}

// Detail is a record with its manifest and reproducibility.
type Detail struct {
	Row             Row                `json:"record"`
	Manifest        json.RawMessage    `json:"manifest"`
	Reproducibility Reproducibility    `json:"reproducibility"`
	Files           []record.FileEntry `json:"files"`
}

func (x *Indexer) Detail(recordID string) (*Detail, error) {
	rows, err := x.db.Query(`SELECT record_id FROM records WHERE record_id = ?`, recordID)
	if err != nil {
		return nil, err
	}
	found := rows.Next()
	rows.Close()
	if !found {
		return nil, fmt.Errorf("record %s is not indexed", recordID)
	}
	env, m, err := x.a.LocalRecord(recordID)
	if err != nil {
		return nil, err
	}
	all, err := x.Search(Query{CalcID: m.CalcID, Limit: 500})
	if err != nil {
		return nil, err
	}
	d := &Detail{Manifest: env.Payload, Files: m.Files}
	for _, r := range all {
		if r.RecordID == recordID {
			d.Row = r
		}
	}
	d.Reproducibility, err = x.Reproducibility(m.CalcID)
	return d, err
}
