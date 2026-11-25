package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/pricing"
)

const indexURL = "https://raw.githubusercontent.com/moveinfra/eu-cloud-prices/main/data/eu-cloud-prices.json"

// genEU writes hetzner.json, ovhcloud.json and scaleway.json from the open EU Cloud Price Index
// (github.com/moveinfra/eu-cloud-prices, CC BY 4.0), which collects them monthly from each provider's own price API
// or price page and records the source of every figure. -from reads a local copy instead.
//
// Only x86 servers with whole vCPUs are kept. A provider with no managed Kubernetes in the index (Hetzner) gets no
// control plane price, and the estimator adds self-managed control plane servers instead.
func genEU(args []string) error {
	fs := flag.NewFlagSet("eu", flag.ExitOnError)
	from := fs.String("from", indexURL, "URL or file path of eu-cloud-prices.json")
	fs.Parse(args)

	var idx struct {
		Generated string `json:"generated"`
		Providers []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Region       string `json:"region"`
			Location     string `json:"location"`
			ServerSource string `json:"serverSource"`
			Note         string `json:"note"`
		} `json:"providers"`
		Servers map[string][]struct {
			Name      string  `json:"name"`
			VCPU      float64 `json:"vcpu"`
			MemoryGiB float64 `json:"memoryGiB"`
			Monthly   float64 `json:"monthly"`
			Category  string  `json:"category"`
			Arch      string  `json:"arch"`
		} `json:"servers"`
		Services map[string]map[string]*struct {
			Price   float64 `json:"price"`
			Product string  `json:"product"`
		} `json:"services"`
	}
	if strings.HasPrefix(*from, "http") {
		if err := getJSON(*from, nil, &idx); err != nil {
			return err
		}
	} else {
		raw, err := os.ReadFile(*from)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &idx); err != nil {
			return fmt.Errorf("%s: %w", *from, err)
		}
	}

	for _, id := range pricing.ProviderIDs {
		out := &pricing.Provider{ID: id, Currency: "EUR", Generated: idx.Generated}
		for _, p := range idx.Providers {
			if p.ID == id {
				out.Name, out.Region, out.Location, out.Source, out.Note = p.Name, p.Region, p.Location, p.ServerSource, p.Note
			}
		}
		for _, s := range idx.Servers[id] {
			if s.Arch != "x86" || s.VCPU != math.Trunc(s.VCPU) || s.VCPU < 1 || s.Monthly <= 0 {
				continue
			}
			out.Plans = append(out.Plans, pricing.Plan{Name: s.Name, Fam: s.Category, Cores: int(s.VCPU),
				MemoryMiB: int(math.Round(s.MemoryGiB * 1024)), Monthly: round2(s.Monthly)})
		}
		svc := idx.Services[id]
		if v := svc["blockStorageGBMonth"]; v != nil {
			out.StorageGBMonth, out.StorageProduct = v.Price, v.Product
		}
		if v := svc["loadBalancerMonthly"]; v != nil {
			out.LoadBalancerMonthly, out.LoadBalancerProduct = v.Price, v.Product
		}
		if v := svc["kubernetesMonthly"]; v != nil {
			out.ControlPlaneMonthly, out.ControlPlaneProduct = v.Price, v.Product
		}
		if out.Name == "" || len(out.Plans) == 0 || out.StorageGBMonth == 0 || out.LoadBalancerMonthly == 0 {
			return fmt.Errorf("%s: the index has no servers, block storage or load balancer price for it", id)
		}
		cp := "self-managed"
		if out.ControlPlaneProduct != "" {
			cp = fmt.Sprintf("%s %.2f", out.ControlPlaneProduct, out.ControlPlaneMonthly)
		}
		fmt.Printf("%-9s %3d plans, storage %.4f/GB (%s), load balancer %.2f (%s), control plane: %s\n", id, len(out.Plans),
			out.StorageGBMonth, out.StorageProduct, out.LoadBalancerMonthly, out.LoadBalancerProduct, cp)
		if err := writeJSON(id+".json", out); err != nil {
			return err
		}
	}
	return nil
}
