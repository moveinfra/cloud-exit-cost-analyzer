package usage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/collect"
)

func TestPercentile(t *testing.T) {
	var h Histogram
	for v := int64(1); v <= 1000; v++ {
		h.Add(v)
	}
	p95 := h.Percentile(0.95)
	if p95 < 950 || p95 > 998 { // at most one 5% bucket above
		t.Fatalf("p95 = %d, want 950..998", p95)
	}
	if h.Percentile(1) != 1000 || h.Max != 1000 {
		t.Fatalf("p100 = %d, max = %d", h.Percentile(1), h.Max)
	}
	var empty Histogram
	if empty.Percentile(0.95) != 0 {
		t.Fatal("empty histogram")
	}
}

func TestSaveLoadPrune(t *testing.T) {
	s := New()
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s.Add(t0, collect.Usage{"ns/Deployment/a": {CPUMilli: 200, MemoryBytes: 512 << 20}})
	s.Add(t0.Add(time.Hour), collect.Usage{"ns/Deployment/b": {CPUMilli: 50, MemoryBytes: 64 << 20}})

	path := filepath.Join(t.TempDir(), "usage.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := l.Stats("ns/Deployment/a")
	if !ok || st.CPUMaxMilli != 200 || st.MemMaxBytes != 512<<20 || l.Samples != 2 {
		t.Fatalf("loaded %+v ok=%v samples=%d", st, ok, l.Samples)
	}
	l.Prune(t0.Add(90*time.Minute), time.Hour)
	if _, ok := l.Stats("ns/Deployment/a"); ok {
		t.Fatal("a should be pruned")
	}
	if _, ok := l.Stats("ns/Deployment/b"); !ok {
		t.Fatal("b should stay")
	}
	if m, err := Load(filepath.Join(t.TempDir(), "missing.json")); err != nil || m.Samples != 0 {
		t.Fatalf("missing file: %v %v", m, err)
	}
}
