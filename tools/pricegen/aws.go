package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/pricing"
)

const awsBase = "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws"

// AWS regions covered. EU first: that is where most people leaving AWS run today.
var awsRegions = []string{
	"eu-central-1", "eu-central-2", "eu-west-1", "eu-west-2", "eu-west-3", "eu-north-1", "eu-south-1", "eu-south-2",
	"us-east-1", "us-east-2", "us-west-2",
}

type awsProduct struct {
	ProductFamily string            `json:"productFamily"`
	Attributes    map[string]string `json:"attributes"`
}

type awsTerm struct {
	PriceDimensions map[string]struct {
		Unit         string            `json:"unit"`
		PricePerUnit map[string]string `json:"pricePerUnit"`
	} `json:"priceDimensions"`
}

func genAWS() error {
	out := &pricing.Cloud{Provider: "aws", Currency: "USD", Generated: today(), Source: awsBase, Regions: map[string]*pricing.Region{}}
	for _, region := range awsRegions {
		r := &pricing.Region{Instances: map[string]float64{}, StorageGBMonth: map[string]float64{}}

		// EC2 instances (Linux, shared tenancy, on-demand) and EBS volumes.
		err := awsOffer("AmazonEC2", region, func(p awsProduct) string {
			a := p.Attributes
			if a["locationType"] != "AWS Region" {
				return ""
			}
			switch {
			case p.ProductFamily == "Compute Instance" && a["operatingSystem"] == "Linux" && a["tenancy"] == "Shared" &&
				a["preInstalledSw"] == "NA" && a["capacitystatus"] == "Used" && a["operation"] == "RunInstances":
				return "i:" + a["instanceType"]
			case p.ProductFamily == "Storage" && a["volumeApiName"] != "":
				return "s:" + a["volumeApiName"]
			}
			return ""
		}, func(key string, price float64) {
			if k, ok := strings.CutPrefix(key, "i:"); ok {
				r.Instances[k] = price
			} else if k, ok := strings.CutPrefix(key, "s:"); ok {
				r.StorageGBMonth[k] = price
			}
		})
		if err != nil {
			return err
		}

		// EKS: standard support control plane, per cluster-hour.
		if err := awsOffer("AmazonEKS", region, func(p awsProduct) string {
			if strings.HasSuffix(p.Attributes["usagetype"], "AmazonEKS-Hours:perCluster") {
				return "cp"
			}
			return ""
		}, func(_ string, price float64) { r.ControlPlaneHourly = price }); err != nil {
			return err
		}

		// Network Load Balancer, hourly part (a Service of type LoadBalancer with the AWS Load Balancer Controller).
		if err := awsOffer("AWSELB", region, func(p awsProduct) string {
			// usagetype is "EUC1-LoadBalancerUsage"; us-east-1 has no region prefix
			if u := p.Attributes["usagetype"]; p.ProductFamily == "Load Balancer-Network" && (u == "LoadBalancerUsage" || strings.HasSuffix(u, "-LoadBalancerUsage")) &&
				!strings.Contains(u, "Outposts") {
				return "lb"
			}
			return ""
		}, func(_ string, price float64) { r.LoadBalancerHourly = price }); err != nil {
			return err
		}

		fmt.Printf("aws %s: %d instance types, %d volume types, EKS %.2f/h, NLB %.4f/h\n",
			region, len(r.Instances), len(r.StorageGBMonth), r.ControlPlaneHourly, r.LoadBalancerHourly)
		out.Regions[region] = r
	}
	return writeJSON("aws.json", out)
}

// awsOffer streams one region's bulk price file. keep returns a key for products to price ("" skips them),
// set receives the on-demand USD price of each kept product.
func awsOffer(offer, region string, keep func(awsProduct) string, set func(key string, price float64)) error {
	body, err := get(fmt.Sprintf("%s/%s/current/%s/index.json", awsBase, offer, region), nil)
	if err != nil {
		return err
	}
	defer body.Close()
	dec := json.NewDecoder(body)
	kept := map[string]string{} // sku -> key

	if _, err := dec.Token(); err != nil { // {
		return err
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case "products":
			err = eachEntry(dec, func(sku string) error {
				var p awsProduct
				if err := dec.Decode(&p); err != nil {
					return err
				}
				if k := keep(p); k != "" {
					kept[sku] = k
				}
				return nil
			})
		case "terms":
			err = eachEntry(dec, func(kind string) error {
				if kind != "OnDemand" {
					return skip(dec)
				}
				return eachEntry(dec, func(sku string) error {
					key, ok := kept[sku]
					if !ok {
						return skip(dec)
					}
					var terms map[string]awsTerm
					if err := dec.Decode(&terms); err != nil {
						return err
					}
					for _, t := range terms {
						for _, d := range t.PriceDimensions {
							if price, err := strconv.ParseFloat(d.PricePerUnit["USD"], 64); err == nil && price > 0 {
								set(key, price)
							}
						}
					}
					return nil
				})
			})
		default:
			err = skip(dec)
		}
		if err != nil {
			return fmt.Errorf("%s %s: %w", offer, region, err)
		}
	}
	return nil
}

// eachEntry walks a JSON object, calling fn with each key while the decoder sits on its value.
func eachEntry(dec *json.Decoder, fn func(key string) error) error {
	if _, err := dec.Token(); err != nil { // {
		return err
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("unexpected token %v", tok)
		}
		if err := fn(key); err != nil {
			return err
		}
	}
	_, err := dec.Token() // }
	return err
}

// skip consumes the next value token by token, so huge sections (Reserved terms) are never held in memory.
func skip(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}
