// Package web serves the report: an HTML page, the same page as a download, and JSON.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/estimate"
)

//go:embed report.html
var files embed.FS

var page = template.Must(template.New("report.html").Funcs(template.FuncMap{
	"money": func(v float64) string { return thousands(v) },
	"cpu": func(m int64) string {
		if m >= 1000 {
			return fmt.Sprintf("%.1f", float64(m)/1000)
		}
		return fmt.Sprintf("%dm", m)
	},
	"mem": func(b int64) string {
		switch {
		case b >= 1<<30:
			return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
		case b > 0:
			return fmt.Sprintf("%d MiB", b>>20)
		}
		return "–"
	},
	"neg":  func(v float64) float64 { return -v },
	"pct":  func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
	"date": func(t time.Time) string { return t.Format("2 Jan 2006 15:04 UTC") },
	"bar": func(v, maxv float64) string {
		if maxv <= 0 {
			return "0"
		}
		return fmt.Sprintf("%.1f", 100*v/maxv)
	},
	"maxTotal": func(r *estimate.Report) float64 {
		m := r.Current.TotalEUR
		for _, s := range r.Scenarios {
			m = max(m, s.TotalEUR)
		}
		return m
	},
	"best":  best,
	"upper": strings.ToUpper,
	"limit": func(n int, rows []estimate.WorkloadRow) []estimate.WorkloadRow { return rows[:min(n, len(rows))] },
}).ParseFS(files, "report.html"))

// best is the cheapest scenario that needs no app changes, the honest headline number.
func best(r *estimate.Report) *estimate.Scenario {
	var b *estimate.Scenario
	for i := range r.Scenarios {
		s := &r.Scenarios[i]
		if s.Error == "" && s.ID != "rightsized" && (b == nil || s.TotalEUR < b.TotalEUR) {
			b = s
		}
	}
	return b
}

// Handler serves the report built by build on every request.
func Handler(build func() (*estimate.Report, error)) http.Handler {
	mux := http.NewServeMux()
	render := func(w http.ResponseWriter, download bool) {
		r, err := build()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if download {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="upcloud-cost-report-%s.html"`, r.Generated.Format("2006-01-02")))
		}
		if err := page.Execute(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) { render(w, false) })
	mux.HandleFunc("GET /report.html", func(w http.ResponseWriter, _ *http.Request) { render(w, true) })
	mux.HandleFunc("GET /api/report", func(w http.ResponseWriter, _ *http.Request) {
		r, err := build()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(r)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return mux
}

func thousands(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
