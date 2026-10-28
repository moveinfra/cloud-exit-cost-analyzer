package main

import (
	"encoding/xml"
	"strconv"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/pricing"
)

const ecbDaily = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"

func genFX() error {
	body, err := get(ecbDaily, nil)
	if err != nil {
		return err
	}
	defer body.Close()
	var doc struct {
		Cube struct {
			Cube struct {
				Time  string `xml:"time,attr"`
				Rates []struct {
					Currency string `xml:"currency,attr"`
					Rate     string `xml:"rate,attr"`
				} `xml:"Cube"`
			} `xml:"Cube"`
		} `xml:"Cube"`
	}
	if err := xml.NewDecoder(body).Decode(&doc); err != nil {
		return err
	}
	fx := &pricing.FX{Date: doc.Cube.Cube.Time, Rates: map[string]float64{}}
	for _, r := range doc.Cube.Cube.Rates {
		if v, err := strconv.ParseFloat(r.Rate, 64); err == nil {
			fx.Rates[r.Currency] = v
		}
	}
	return writeJSON("fx.json", fx)
}
