package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/pricing"
)

const upcloudAPI = "https://api.upcloud.com/1.3"

// genUpCloud reads plans and per-zone prices from the UpCloud API (token in UPCLOUD_TOKEN).
//
// The API returns prices in the account's currency, per hour, with "amount" as the quantity the price applies to.
// Check the printed table against the UpCloud price list before committing; -unit and -hours exist because
// the docs do not state the unit.
// Managed Load Balancer and Managed Kubernetes are not in the price API, so their monthly prices come from flags
// (copy them from https://upcloud.com/pricing/).
func genUpCloud(args []string) error {
	fs := flag.NewFlagSet("upcloud", flag.ExitOnError)
	unit := fs.Float64("unit", 100, "divide API prices by this to get EUR (100 = prices are in cents)")
	hours := fs.Float64("hours", 672, "billable hours per month (UpCloud caps monthly cost at this many hours)")
	lb := fs.Float64("lb-monthly", 0, "Managed Load Balancer, smallest production plan, EUR per month")
	uks := fs.Float64("uks-monthly", 0, "Managed Kubernetes production control plane, EUR per month")
	fs.Parse(args)

	token := os.Getenv("UPCLOUD_TOKEN")
	if token == "" {
		return fmt.Errorf("set UPCLOUD_TOKEN (an UpCloud API token, read-only access is enough)")
	}
	h := http.Header{"Authorization": {"Bearer " + token}, "Accept": {"application/json"}}

	var plans struct {
		Plans struct {
			Plan []struct {
				Name        string `json:"name"`
				Cores       int    `json:"core_number"`
				Memory      int    `json:"memory_amount"` // MiB
				StorageSize int    `json:"storage_size"`
				GPUs        int    `json:"gpu_amount"`
			} `json:"plan"`
		} `json:"plans"`
	}
	if err := getJSON(upcloudAPI+"/plan", h, &plans); err != nil {
		return err
	}
	var prices struct {
		Prices struct {
			Zone []map[string]json.RawMessage `json:"zone"`
		} `json:"prices"`
	}
	if err := getJSON(upcloudAPI+"/price", h, &prices); err != nil {
		return err
	}

	type item struct {
		Amount float64 `json:"amount"`
		Price  float64 `json:"price"`
	}
	monthly := func(it item) float64 { return it.Price / *unit * *hours }

	out := &pricing.UpCloud{Currency: "EUR", Generated: today(), Source: upcloudAPI + "/price", Zones: map[string]*pricing.UpCloudZone{}}
	other := map[string]bool{}
	for _, zone := range prices.Prices.Zone {
		var name string
		if err := json.Unmarshal(zone["name"], &name); err != nil {
			return fmt.Errorf("zone without name: %w", err)
		}
		z := &pricing.UpCloudZone{LoadBalancerMonthly: *lb, ControlPlaneMonthly: *uks}
		for key, raw := range zone {
			var it item
			if key == "name" || json.Unmarshal(raw, &it) != nil || it.Amount == 0 {
				continue
			}
			switch {
			case strings.HasPrefix(key, "server_plan_"):
				// handled below, by plan name
			case key == "storage_maxiops":
				z.StorageMaxIOPSMonth = monthly(it) / it.Amount
			case key == "storage_standard":
				z.StorageStandardMonth = monthly(it) / it.Amount
			default:
				other[key] = true
			}
		}
		for _, p := range plans.Plans.Plan {
			raw, ok := zone["server_plan_"+p.Name]
			var it item
			if p.GPUs > 0 || !ok || json.Unmarshal(raw, &it) != nil || it.Price == 0 {
				continue
			}
			z.Plans = append(z.Plans, pricing.Plan{Name: p.Name, Cores: p.Cores, MemoryMiB: p.Memory, StorageGB: p.StorageSize, Monthly: round2(monthly(it))})
		}
		sort.Slice(z.Plans, func(i, j int) bool { return z.Plans[i].Monthly < z.Plans[j].Monthly })
		out.Zones[name] = z
	}

	if z := out.Zones["de-fra1"]; z != nil {
		fmt.Println("Check against https://upcloud.com/pricing/ (de-fra1, EUR/month):")
		for _, p := range z.Plans[:min(8, len(z.Plans))] {
			fmt.Printf("  %-22s %3d vCPU %6d MiB  %8.2f\n", p.Name, p.Cores, p.MemoryMiB, p.Monthly)
		}
		fmt.Printf("  MaxIOPS storage %.4f per GB-month\n", z.StorageMaxIOPSMonth)
	}
	keys := make([]string, 0, len(other))
	for k := range other {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("Other price keys (not used):", strings.Join(keys, ", "))
	return writeJSON("upcloud.json", out)
}

func getJSON(url string, h http.Header, v any) error {
	body, err := get(url, h)
	if err != nil {
		return err
	}
	defer body.Close()
	return json.NewDecoder(body).Decode(v)
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
