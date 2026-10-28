// Package usage keeps usage history per workload as compact histograms, so a cluster with hundreds of workloads
// sampled every minute for weeks still fits in a few MB. Percentiles are read from bucket upper bounds:
// rounded up by at most one bucket (5%), which errs on the safe side when sizing servers.
package usage

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/collect"
)

const growth = 1.05 // bucket width

// Histogram counts samples in log-spaced buckets. Bucket i covers (growth^(i-1), growth^i].
type Histogram struct {
	Buckets map[int]uint32 `json:"b"`
	Count   uint32         `json:"n"`
	Max     int64          `json:"max"`
}

func (h *Histogram) Add(v int64) {
	if h.Buckets == nil {
		h.Buckets = map[int]uint32{}
	}
	i := 0
	if v > 1 {
		i = int(math.Ceil(math.Log(float64(v)) / math.Log(growth)))
	}
	h.Buckets[i]++
	h.Count++
	h.Max = max(h.Max, v)
}

// Percentile returns the upper bound of the bucket holding the p-th percentile (0..1), capped at Max.
func (h *Histogram) Percentile(p float64) int64 {
	if h.Count == 0 {
		return 0
	}
	idx := make([]int, 0, len(h.Buckets))
	for i := range h.Buckets {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	target := uint32(math.Ceil(p * float64(h.Count)))
	var seen uint32
	for _, i := range idx {
		seen += h.Buckets[i]
		if seen >= target {
			return min(int64(math.Ceil(math.Pow(growth, float64(i)))), h.Max)
		}
	}
	return h.Max
}

// Series is the usage history of one workload.
type Series struct {
	CPU    Histogram `json:"cpu"` // millicores
	Memory Histogram `json:"mem"` // MiB
	Last   time.Time `json:"last"`
}

// Point is the cluster total at one sample, kept for the trend chart.
type Point struct {
	T           time.Time `json:"t"`
	CPUMilli    int64     `json:"cpu"`
	MemoryBytes int64     `json:"mem"`
}

// Store is safe for concurrent use.
type Store struct {
	mu        sync.Mutex
	First     time.Time          `json:"first"`
	Last      time.Time          `json:"last"`
	Samples   int                `json:"samples"`
	Workloads map[string]*Series `json:"workloads"`
	Trend     []Point            `json:"trend"`
}

const maxTrend = 7 * 24 * 12 // one point per 5 minutes for 7 days

func New() *Store { return &Store{Workloads: map[string]*Series{}} }

func (s *Store) Add(t time.Time, u collect.Usage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.First.IsZero() {
		s.First = t
	}
	s.Last = t
	s.Samples++
	var total Point
	total.T = t
	for key, v := range u {
		w := s.Workloads[key]
		if w == nil {
			w = &Series{}
			s.Workloads[key] = w
		}
		w.CPU.Add(v.CPUMilli)
		w.Memory.Add(v.MemoryBytes >> 20)
		w.Last = t
		total.CPUMilli += v.CPUMilli
		total.MemoryBytes += v.MemoryBytes
	}
	if n := len(s.Trend); n == 0 || t.Sub(s.Trend[n-1].T) >= 5*time.Minute {
		s.Trend = append(s.Trend, total)
		if len(s.Trend) > maxTrend {
			s.Trend = s.Trend[len(s.Trend)-maxTrend:]
		}
	}
}

// Stats is the usage summary of one workload.
type Stats struct {
	CPUP95Milli int64
	CPUMaxMilli int64
	MemP95Bytes int64
	MemMaxBytes int64
	Samples     uint32
}

func (s *Store) Stats(key string) (Stats, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.Workloads[key]
	if w == nil || w.CPU.Count == 0 {
		return Stats{}, false
	}
	return Stats{
		CPUP95Milli: w.CPU.Percentile(0.95),
		CPUMaxMilli: w.CPU.Max,
		MemP95Bytes: w.Memory.Percentile(0.95) << 20,
		MemMaxBytes: w.Memory.Max << 20,
		Samples:     w.CPU.Count,
	}, true
}

// Coverage tells how long usage has been collected.
func (s *Store) Coverage() (first, last time.Time, samples int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.First, s.Last, s.Samples
}

func (s *Store) TrendPoints() []Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Point(nil), s.Trend...)
}

// Prune drops workloads not seen for longer than d (deleted apps should not count forever).
func (s *Store) Prune(now time.Time, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, w := range s.Workloads {
		if now.Sub(w.Last) > d {
			delete(s.Workloads, k)
		}
	}
}

// Save writes the store atomically to path.
func (s *Store) Save(path string) error {
	s.mu.Lock()
	raw, err := json.Marshal(s)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".usage.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads a store saved by Save. A missing file gives an empty store.
func Load(path string) (*Store, error) {
	s := New()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, s); err != nil {
		return nil, err
	}
	if s.Workloads == nil {
		s.Workloads = map[string]*Series{}
	}
	return s, nil
}
