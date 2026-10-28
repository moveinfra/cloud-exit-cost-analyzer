package web

import (
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/collect"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/estimate"
)

func sample() *estimate.Report {
	return &estimate.Report{
		Generated: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC), Since: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
		Samples: 10080, CoverageHours: 168, Zone: "de-fra1",
		Totals: estimate.Totals{Nodes: 6, SpotNodes: 1, CPUMilli: 24000, MemoryBytes: 96 << 30, ReqCPUMilli: 9800, ReqMemoryBytes: 30 << 30,
			UseCPUMilli: 3100, UseMemoryBytes: 14 << 30, VolumeGB: 420, LoadBalancers: 3},
		Current: estimate.Current{Provider: "aws", Region: "eu-central-1", Currency: "USD", Total: 1290.5, TotalEUR: 1157.4,
			Lines:   []estimate.Line{{Item: "5 × m5.xlarge", Detail: "0.2300/h list", Monthly: 839.5}, {Item: "Kubernetes control plane", Detail: "0.10/h", Monthly: 73}},
			Missing: []string{"1 spot node(s)", "data transfer (egress)"}},
		Scenarios: []estimate.Scenario{
			{ID: "same", Name: "Same servers", Description: "Like for like.", Nodes: []estimate.NodeGroup{{Plan: "4xCPU-16GB", Count: 6, Monthly: 330}},
				Lines: []estimate.Line{{Item: "Block storage (MaxIOPS)", Detail: "420 GB", Monthly: 92.4}}, TotalEUR: 452.4, SavingsEUR: 705, SavingsPct: 60.9},
			{ID: "requests", Name: "Current requests", Description: "Packed.", Nodes: []estimate.NodeGroup{{Plan: "4xCPU-16GB", Count: 3, Monthly: 165}}, TotalEUR: 287.4, SavingsEUR: 870, SavingsPct: 75.2},
			{ID: "rightsized", Name: "Right-sized", Description: "Lower requests.", Nodes: []estimate.NodeGroup{{Plan: "2xCPU-4GB", Count: 3, Monthly: 60}}, TotalEUR: 160, SavingsEUR: 997.4, SavingsPct: 86.2},
		},
		Recommendations: []estimate.Recommendation{{Kind: "Lower requests", Target: "shop/Deployment/web", Detail: "requests cpu 1000m, uses 120m.", SavingsEUR: 41.2}},
		Workloads: []estimate.WorkloadRow{{Workload: collect.Workload{Key: "shop/Deployment/web", Pods: 3, ReqCPUMilli: 3000, ReqMemory: 6 << 30},
			UsageCPUP95Milli: 330, UsageMemMaxBytes: 900 << 20, Samples: 10080}},
		Notes:      []string{"UpCloud zone de-fra1, list prices in EUR excluding VAT."},
		PriceDates: map[string]string{"upcloud": "2026-10-28", "aws": "2026-10-28", "fx": "2026-10-27"},
	}
}

func TestReport(t *testing.T) {
	h := Handler(func() (*estimate.Report, error) { return sample(), nil })
	for _, path := range []string{"/", "/report.html", "/api/report", "/healthz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, body)
		}
		if path == "/" {
			for _, want := range []string{"€1,157", "€287", "870 less per month", "4xCPU-16GB", "shop/Deployment/web"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("page lacks %q", want)
				}
			}
			if out := os.Getenv("REPORT_OUT"); out != "" { // REPORT_OUT=/tmp/r.html go test ./internal/web to look at it
				os.WriteFile(out, body, 0o644)
			}
		}
		if path == "/report.html" && !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
			t.Error("download is not an attachment")
		}
	}
}
