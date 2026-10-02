package indexer

import (
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chaeeundad/PFCN/internal/record"
)

//go:embed templates/*.html
var templateFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"short": func(s string) string {
		if i := strings.LastIndex(s, ":"); i >= 0 && len(s)-i > 13 {
			return s[i+1 : i+13]
		}
		return s
	},
	"peer": func(s string) string {
		if len(s) > 12 {
			return s[:6] + "…" + s[len(s)-4:]
		}
		return s
	},
	"energy": func(f *float64) string {
		if f == nil {
			return "—"
		}
		return strconv.FormatFloat(*f, 'f', 6, 64)
	},
	"status": func(s string) string {
		if s == "" {
			return record.StatusUnverified
		}
		return s
	},
}).ParseFS(templateFS, "templates/*.html"))

// Handler serves the explorer pages, REST API and record downloads.
func (x *Indexer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", x.pageSearch)
	mux.HandleFunc("GET /record/{id}", x.pageRecord)
	mux.HandleFunc("GET /record/{id}/files/{path...}", x.download)
	mux.HandleFunc("GET /api/records", x.apiSearch)
	mux.HandleFunc("GET /api/records/{id}", x.apiRecord)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}

func queryFrom(r *http.Request) Query {
	v := r.URL.Query()
	q := Query{Solver: v.Get("solver"), Element: v.Get("element"), Formula: v.Get("formula"), Calculation: v.Get("calculation"), CalcID: v.Get("calc_id")}
	if c := v.Get("converged"); c == "true" || c == "false" {
		b := c == "true"
		q.Converged = &b
	}
	q.Limit, _ = strconv.Atoi(v.Get("limit"))
	return q
}

func (x *Indexer) pageSearch(w http.ResponseWriter, r *http.Request) {
	q := queryFrom(r)
	rows, err := x.Search(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.ExecuteTemplate(w, "search.html", map[string]any{"Rows": rows, "Q": r.URL.Query()})
}

func (x *Indexer) pageRecord(w http.ResponseWriter, r *http.Request) {
	d, err := x.Detail(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	var m record.Manifest
	json.Unmarshal(d.Manifest, &m)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.ExecuteTemplate(w, "record.html", map[string]any{"D": d, "M": m})
}

func (x *Indexer) download(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := x.Detail(id); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	var p string
	if r.PathValue("path") == "manifest.json" || r.PathValue("path") == "checksums.txt" {
		p = r.PathValue("path")
	} else {
		clean, err := record.CleanPath(r.PathValue("path"))
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		p = clean
	}
	dir, _ := x.a.RecordDir(id)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filepath.Base(p)))
	http.ServeFile(w, r, filepath.Join(dir, filepath.FromSlash(p)))
}

func (x *Indexer) apiSearch(w http.ResponseWriter, r *http.Request) {
	rows, err := x.Search(queryFrom(r))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rows)
}

func (x *Indexer) apiRecord(w http.ResponseWriter, r *http.Request) {
	d, err := x.Detail(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(d)
}
