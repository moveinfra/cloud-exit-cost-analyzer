package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/pricing"
)

const azureAPI = "https://prices.azure.com/api/retail/prices"

var azureRegions = []string{
	"westeurope", "northeurope", "germanywestcentral", "francecentral", "swedencentral", "switzerlandnorth",
	"italynorth", "polandcentral", "spaincentral", "uksouth", "eastus", "eastus2", "westus2",
}

type azureItem struct {
	ArmSkuName    string  `json:"armSkuName"`
	SkuName       string  `json:"skuName"`
	ProductName   string  `json:"productName"`
	MeterName     string  `json:"meterName"`
	UnitPrice     float64 `json:"unitPrice"`
	UnitOfMeasure string  `json:"unitOfMeasure"`
}

func genAzure() error {
	out := &pricing.Cloud{Provider: "azure", Currency: "USD", Generated: today(), Source: azureAPI, Regions: map[string]*pricing.Region{}}
	for _, region := range azureRegions {
		r := &pricing.Region{Instances: map[string]float64{}}
		err := azureQuery(fmt.Sprintf("serviceName eq 'Virtual Machines' and armRegionName eq '%s' and priceType eq 'Consumption'", region), func(it azureItem) {
			name := strings.ToLower(it.SkuName + " " + it.ProductName)
			if it.UnitOfMeasure != "1 Hour" || it.UnitPrice <= 0 || strings.Contains(name, "spot") ||
				strings.Contains(name, "low priority") || strings.Contains(name, "windows") {
				return
			}
			if old, ok := r.Instances[it.ArmSkuName]; !ok || it.UnitPrice < old {
				r.Instances[it.ArmSkuName] = it.UnitPrice
			}
		})
		if err != nil {
			return err
		}
		// AKS Standard tier (uptime SLA), the tier production clusters use. The Free tier has no control plane fee.
		err = azureQuery(fmt.Sprintf("serviceName eq 'Azure Kubernetes Service' and armRegionName eq '%s' and priceType eq 'Consumption'", region), func(it azureItem) {
			if it.ProductName == "Azure Kubernetes Service" && it.MeterName == "Standard Uptime SLA" && it.UnitOfMeasure == "1 Hour" {
				r.ControlPlaneHourly = it.UnitPrice
			}
		})
		if err != nil {
			return err
		}
		fmt.Printf("azure %s: %d VM sizes, AKS %.2f/h\n", region, len(r.Instances), r.ControlPlaneHourly)
		out.Regions[region] = r
	}
	return writeJSON("azure.json", out)
}

func azureQuery(filter string, fn func(azureItem)) error {
	next := azureAPI + "?" + url.Values{"$filter": {filter}}.Encode()
	for next != "" {
		body, err := get(next, nil)
		if err != nil {
			return err
		}
		var page struct {
			Items        []azureItem `json:"Items"`
			NextPageLink string      `json:"NextPageLink"`
		}
		err = json.NewDecoder(body).Decode(&page)
		body.Close()
		if err != nil {
			return err
		}
		for _, it := range page.Items {
			fn(it)
		}
		next = page.NextPageLink
	}
	return nil
}
