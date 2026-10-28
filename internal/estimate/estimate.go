// Package estimate turns a cluster snapshot plus usage history into a cost report: what the cluster costs today
// at list prices, what it would cost on UpCloud in three scenarios, and what to change to pay less anywhere.
package estimate

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/collect"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/pricing"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/usage"
)

// Options come from the Helm values.
type Options struct {
	Zone                string             // UpCloud zone to price, e.g. de-fra1
	MinNodes            int                // never plan fewer worker nodes than this (high availability)
	CPUHeadroom         float64            // right-sized CPU = p95 usage * (1 + CPUHeadroom)
	MemoryHeadroom      float64            // right-sized memory = max usage * (1 + MemoryHeadroom)
	NodeReserve         float64            // share of each server kept for the OS and kubelet (0.1 = 10%)
	InstancePrices      map[string]float64 // instance type -> price per hour, overrides list prices (GCP, DigitalOcean, discounts)
	CurrentDiscount     float64            // 0.2 = you pay 20% below list price (savings plans, EDP); applied to compute
	PriceSpotAsOnDemand bool               // count spot nodes at on-demand prices instead of leaving them out
}

func (o *Options) defaults() {
	if o.Zone == "" {
		o.Zone = "de-fra1"
	}
	if o.MinNodes == 0 {
		o.MinNodes = 3
	}
	if o.CPUHeadroom == 0 {
		o.CPUHeadroom = 0.25
	}
	if o.MemoryHeadroom == 0 {
		o.MemoryHeadroom = 0.15
	}
	if o.NodeReserve == 0 {
		o.NodeReserve = 0.1
	}
}

type Line struct {
	Item    string  `json:"item"`
	Detail  string  `json:"detail,omitempty"`
	Monthly float64 `json:"monthly"`
}

type Current struct {
	Provider string   `json:"provider"`
	Region   string   `json:"region"`
	Currency string   `json:"currency"`
	Lines    []Line   `json:"lines"`
	Total    float64  `json:"total"`
	TotalEUR float64  `json:"totalEUR"`
	Missing  []string `json:"missing"` // what could not be priced, so the total is a lower bound
}

type NodeGroup struct {
	Plan    string  `json:"plan"`
	Count   int     `json:"count"`
	Monthly float64 `json:"monthly"` // for all nodes in the group
}

type Scenario struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Nodes       []NodeGroup `json:"nodes"`
	Lines       []Line      `json:"lines"`
	TotalEUR    float64     `json:"totalEUR"`
	SavingsEUR  float64     `json:"savingsEUR"` // vs current, positive = cheaper on UpCloud
	SavingsPct  float64     `json:"savingsPct"`
	Error       string      `json:"error,omitempty"`
}

type Recommendation struct {
	Kind       string  `json:"kind"`
	Target     string  `json:"target"`
	Detail     string  `json:"detail"`
	SavingsEUR float64 `json:"savingsEUR,omitempty"` // monthly, on UpCloud prices
}

type WorkloadRow struct {
	collect.Workload
	UsageCPUP95Milli int64  `json:"usageCpuP95Milli"`
	UsageMemMaxBytes int64  `json:"usageMemoryMaxBytes"`
	Samples          uint32 `json:"samples"`
	SuggestCPUMilli  int64  `json:"suggestCpuMilli,omitempty"`
	SuggestMemBytes  int64  `json:"suggestMemoryBytes,omitempty"`
}

type Totals struct {
	Nodes, SpotNodes, ServerlessNodes int
	CPUMilli, MemoryBytes             int64 // node capacity
	ReqCPUMilli, ReqMemoryBytes       int64
	UseCPUMilli, UseMemoryBytes       int64 // sum of per-workload p95 CPU / max memory
	VolumeGB                          float64
	LoadBalancers                     int
}

type Report struct {
	Generated       time.Time         `json:"generated"`
	Since           time.Time         `json:"usageSince"`
	Samples         int               `json:"usageSamples"`
	CoverageHours   float64           `json:"usageHours"`
	Zone            string            `json:"upcloudZone"`
	Totals          Totals            `json:"totals"`
	Current         Current           `json:"current"`
	Scenarios       []Scenario        `json:"scenarios"`
	Recommendations []Recommendation  `json:"recommendations"`
	Workloads       []WorkloadRow     `json:"workloads"`
	Notes           []string          `json:"notes"`
	Warning         string            `json:"warning,omitempty"` // shown on top while usage data is thin
	PriceDates      map[string]string `json:"priceDates"`
}

const gib = 1 << 30

// Build computes the report.
func Build(snap *collect.Snapshot, store *usage.Store, book *pricing.Book, o Options) (*Report, error) {
	o.defaults()
	zone, err := book.UpCloud.Zone(o.Zone)
	if err != nil {
		return nil, err
	}
	first, last, samples := store.Coverage()
	r := &Report{Generated: time.Now().UTC(), Since: first, Samples: samples, CoverageHours: last.Sub(first).Hours(), Zone: o.Zone,
		PriceDates: map[string]string{"upcloud": book.UpCloud.Generated, "fx": book.FX.Date}}

	rows, t := workloads(snap, store, o)
	r.Workloads = rows
	for _, n := range snap.Nodes {
		t.Nodes++
		if n.Spot {
			t.SpotNodes++
		}
		if n.Serverless {
			t.ServerlessNodes++
		}
		t.CPUMilli += n.CPUMilli
		t.MemoryBytes += n.MemoryBytes
	}
	for _, v := range snap.Volumes {
		t.VolumeGB += v.GB
	}
	t.LoadBalancers = len(snap.LoadBalancers)
	r.Totals = t

	r.Current = current(snap, book, o, &r.PriceDates)
	r.Scenarios = scenarios(snap, rows, zone, o)
	for i := range r.Scenarios {
		s := &r.Scenarios[i]
		if s.Error == "" && r.Current.TotalEUR > 0 {
			s.SavingsEUR = r.Current.TotalEUR - s.TotalEUR
			s.SavingsPct = 100 * s.SavingsEUR / r.Current.TotalEUR
		}
	}
	r.Recommendations = recommendations(snap, rows, zone, r.Scenarios)
	r.Notes = notes(r, o)
	if r.Samples == 0 || r.CoverageHours < 24 {
		r.Warning = r.Notes[0]
	}
	return r, nil
}

func workloads(snap *collect.Snapshot, store *usage.Store, o Options) ([]WorkloadRow, Totals) {
	var t Totals
	rows := make([]WorkloadRow, 0, len(snap.Workloads))
	for _, w := range snap.Workloads {
		row := WorkloadRow{Workload: w}
		t.ReqCPUMilli += w.ReqCPUMilli
		t.ReqMemoryBytes += w.ReqMemory
		if st, ok := store.Stats(w.Key); ok {
			row.UsageCPUP95Milli, row.UsageMemMaxBytes, row.Samples = st.CPUP95Milli, st.MemMaxBytes, st.Samples
			t.UseCPUMilli += st.CPUP95Milli
			t.UseMemoryBytes += st.MemMaxBytes
			if st.Samples >= 60 && w.Pods > 0 {
				// per-pod suggestion, rounded up to 10m / 16Mi
				row.SuggestCPUMilli = roundUp(int64(float64(st.CPUP95Milli)*(1+o.CPUHeadroom))/int64(w.Pods), 10)
				row.SuggestCPUMilli = max(row.SuggestCPUMilli, 10)
				row.SuggestMemBytes = roundUp(int64(float64(st.MemMaxBytes)*(1+o.MemoryHeadroom))/int64(w.Pods), 16<<20)
			}
		} else {
			t.UseCPUMilli += w.ReqCPUMilli
			t.UseMemoryBytes += w.ReqMemory
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].ReqCPUMilli+rows[i].ReqMemory/(4<<20) > rows[j].ReqCPUMilli+rows[j].ReqMemory/(4<<20)
	})
	return rows, t
}

func current(snap *collect.Snapshot, book *pricing.Book, o Options, dates *map[string]string) Current {
	c := Current{Currency: "USD"}
	providers, regions := map[string]int{}, map[string]int{}
	for _, n := range snap.Nodes {
		providers[n.Provider]++
		regions[n.Region]++
	}
	c.Provider, c.Region = top(providers), top(regions)
	cloud := book.Clouds[c.Provider]
	var region *pricing.Region
	if cloud != nil {
		region = cloud.Regions[c.Region]
		(*dates)[c.Provider] = cloud.Generated
		c.Currency = cloud.Currency
	}
	if region == nil {
		region = &pricing.Region{}
		if len(o.InstancePrices) == 0 {
			c.Missing = append(c.Missing, fmt.Sprintf("no built-in prices for %s %s: set instancePrices in the Helm values", or(c.Provider, "this provider"), c.Region))
		}
	}

	type group struct {
		count int
		price float64
	}
	groups := map[string]*group{}
	var unpriced, spot, serverless int
	for _, n := range snap.Nodes {
		switch {
		case n.Serverless:
			serverless++
			continue
		case n.Spot && !o.PriceSpotAsOnDemand:
			spot++
			continue
		}
		price, ok := o.InstancePrices[n.InstanceType]
		if !ok {
			price, ok = region.Instances[n.InstanceType]
		}
		if !ok {
			unpriced++
			continue
		}
		g := groups[n.InstanceType]
		if g == nil {
			g = &group{price: price}
			groups[n.InstanceType] = g
		}
		g.count++
	}
	names := make([]string, 0, len(groups))
	for k := range groups {
		names = append(names, k)
	}
	sort.Strings(names)
	discount := 1 - o.CurrentDiscount
	for _, k := range names {
		g := groups[k]
		c.Lines = append(c.Lines, Line{Item: fmt.Sprintf("%d × %s", g.count, k),
			Detail: fmt.Sprintf("%.4f/h list", g.price), Monthly: float64(g.count) * g.price * pricing.HoursPerMonth * discount})
	}
	if o.CurrentDiscount > 0 {
		c.Lines = append(c.Lines, Line{Item: "Discount on compute", Detail: fmt.Sprintf("%.0f%% below list, from your settings", 100*o.CurrentDiscount)})
	}
	if region.ControlPlaneHourly > 0 {
		c.Lines = append(c.Lines, Line{Item: "Kubernetes control plane", Detail: fmt.Sprintf("%.2f/h", region.ControlPlaneHourly), Monthly: region.ControlPlaneHourly * pricing.HoursPerMonth})
	}

	var gb float64
	byType := map[string]float64{}
	var unknownGB float64
	for _, v := range snap.Volumes {
		if p, ok := region.StorageGBMonth[v.Type]; ok {
			byType[v.Type] += v.GB
			gb += v.GB * p
		} else {
			unknownGB += v.GB
		}
	}
	if gb > 0 {
		parts := []string{}
		for k, v := range byType {
			parts = append(parts, fmt.Sprintf("%.0f GB %s", v, k))
		}
		sort.Strings(parts)
		c.Lines = append(c.Lines, Line{Item: "Persistent volumes", Detail: strings.Join(parts, ", "), Monthly: gb})
	}
	if n := len(snap.LoadBalancers); n > 0 && region.LoadBalancerHourly > 0 {
		c.Lines = append(c.Lines, Line{Item: fmt.Sprintf("%d × load balancer", n), Detail: "hourly part only, traffic (LCU) not included",
			Monthly: float64(n) * region.LoadBalancerHourly * pricing.HoursPerMonth})
	} else if n > 0 {
		c.Missing = append(c.Missing, fmt.Sprintf("%d load balancer(s)", n))
	}
	if unpriced > 0 {
		c.Missing = append(c.Missing, fmt.Sprintf("%d node(s) with an instance type not in the price list", unpriced))
	}
	if spot > 0 {
		c.Missing = append(c.Missing, fmt.Sprintf("%d spot node(s): spot prices change hourly (set priceSpotAsOnDemand to count them)", spot))
	}
	if serverless > 0 {
		c.Missing = append(c.Missing, fmt.Sprintf("%d serverless node(s) (Fargate / virtual nodes)", serverless))
	}
	if unknownGB > 0 {
		c.Missing = append(c.Missing, fmt.Sprintf("%.0f GB of volumes of an unpriced type", unknownGB))
	}
	c.Missing = append(c.Missing, "data transfer (egress), backups, snapshots, NAT gateways and other services outside Kubernetes")

	for _, l := range c.Lines {
		c.Total += l.Monthly
	}
	if eur, ok := book.FX.ToEUR(c.Total, c.Currency); ok {
		c.TotalEUR = eur
	}
	return c
}

// demand is what has to fit on the new nodes.
type demand struct {
	cpu, mem       int64 // non-DaemonSet total
	dsCPU, dsMem   int64 // DaemonSet per node
	maxCPU, maxMem int64 // largest single pod
}

func scenarios(snap *collect.Snapshot, rows []WorkloadRow, z *pricing.UpCloudZone, o Options) []Scenario {
	var boundGB, allGB float64
	for _, v := range snap.Volumes {
		allGB += v.GB
		if v.Phase == "Bound" {
			boundGB += v.GB
		}
	}
	shared := func(gb float64, lbs int) []Line {
		lines := []Line{}
		if z.ControlPlaneMonthly > 0 {
			lines = append(lines, Line{Item: "Managed Kubernetes control plane", Monthly: z.ControlPlaneMonthly})
		}
		if gb > 0 {
			lines = append(lines, Line{Item: "Block storage (MaxIOPS)", Detail: fmt.Sprintf("%.0f GB", gb), Monthly: gb * z.StorageMaxIOPSMonth})
		}
		if lbs > 0 && z.LoadBalancerMonthly > 0 {
			lines = append(lines, Line{Item: fmt.Sprintf("%d × managed load balancer", lbs), Monthly: float64(lbs) * z.LoadBalancerMonthly})
		}
		return lines
	}
	lbs := len(snap.LoadBalancers)

	// 1. Same servers: each node gets the cheapest plan at least as big.
	same := Scenario{ID: "same", Name: "Same servers", Description: "Each node replaced by the closest UpCloud plan with at least the same vCPU and memory. No changes to your apps."}
	counts := map[string]int{}
	price := map[string]float64{}
	for _, n := range snap.Nodes {
		if n.Serverless {
			continue
		}
		p, ok := fit(z.Plans, n.CPUMilli, n.MemoryBytes)
		if !ok {
			same.Error = fmt.Sprintf("no UpCloud plan as large as node %s", n.Name)
			break
		}
		counts[p.Name]++
		price[p.Name] = p.Monthly
	}
	for name, c := range counts {
		same.Nodes = append(same.Nodes, NodeGroup{Plan: name, Count: c, Monthly: float64(c) * price[name]})
	}
	sort.Slice(same.Nodes, func(i, j int) bool { return same.Nodes[i].Monthly > same.Nodes[j].Monthly })
	same.Lines = shared(allGB, lbs)
	finish(&same)

	// 2. Same requests, packed onto the cheapest set of equal nodes.
	var req, use demand
	for _, w := range rows {
		cpu, mem := w.ReqCPUMilli, w.ReqMemory
		ucpu, umem := cpu, mem
		if w.Samples >= 60 {
			ucpu = w.SuggestCPUMilli * int64(w.Pods)
			umem = w.SuggestMemBytes * int64(w.Pods)
		}
		if w.Kind == "DaemonSet" {
			per := int64(max(w.Pods, 1))
			req.dsCPU, req.dsMem = req.dsCPU+cpu/per, req.dsMem+mem/per
			use.dsCPU, use.dsMem = use.dsCPU+ucpu/per, use.dsMem+umem/per
			continue
		}
		req.cpu, req.mem = req.cpu+cpu, req.mem+mem
		use.cpu, use.mem = use.cpu+ucpu, use.mem+umem
		req.maxCPU, req.maxMem = max(req.maxCPU, w.MaxPodCPU), max(req.maxMem, w.MaxPodMem)
		if w.Pods > 0 {
			use.maxCPU, use.maxMem = max(use.maxCPU, ucpu/int64(w.Pods)), max(use.maxMem, umem/int64(w.Pods))
		}
	}
	packed := pack(Scenario{ID: "requests", Name: "Current requests",
		Description: "Your pods' current CPU and memory requests, packed onto the cheapest set of UpCloud servers. No changes to your apps."}, req, z, o)
	packed.Lines = append(packed.Lines, shared(boundGB, lbs)...)
	finish(&packed)

	right := pack(Scenario{ID: "rightsized", Name: "Right-sized",
		Description: fmt.Sprintf("Requests lowered to measured usage (p95 CPU +%.0f%%, peak memory +%.0f%%), one load balancer with an ingress controller, unused volumes removed.", 100*o.CPUHeadroom, 100*o.MemoryHeadroom)}, use, z, o)
	right.Lines = append(right.Lines, shared(boundGB, min(lbs, 1))...)
	finish(&right)
	return []Scenario{same, packed, right}
}

// pack picks the plan whose cheapest node count fits the demand, keeping NodeReserve of each node free.
func pack(s Scenario, d demand, z *pricing.UpCloudZone, o Options) Scenario {
	best, bestCost, bestN := pricing.Plan{}, math.Inf(1), 0
	for _, p := range z.Plans {
		allocCPU := int64(float64(p.Cores*1000)*(1-o.NodeReserve)) - d.dsCPU
		allocMem := int64(float64(int64(p.MemoryMiB)<<20)*(1-o.NodeReserve)) - d.dsMem
		if allocCPU <= 0 || allocMem <= 0 || d.maxCPU > allocCPU || d.maxMem > allocMem {
			continue
		}
		n := max(o.MinNodes, ceilDiv(d.cpu, allocCPU), ceilDiv(d.mem, allocMem))
		if n > 500 {
			continue
		}
		cost := float64(n) * p.Monthly
		if cost < bestCost || (cost == bestCost && n < bestN) {
			best, bestCost, bestN = p, cost, n
		}
	}
	if bestN == 0 {
		s.Error = "no UpCloud plan fits the largest pod"
		return s
	}
	s.Nodes = []NodeGroup{{Plan: best.Name, Count: bestN, Monthly: bestCost}}
	return s
}

func finish(s *Scenario) {
	for _, n := range s.Nodes {
		s.TotalEUR += n.Monthly
	}
	for _, l := range s.Lines {
		s.TotalEUR += l.Monthly
	}
}

func fit(plans []pricing.Plan, cpuMilli, memBytes int64) (pricing.Plan, bool) {
	for _, p := range plans { // cheapest first
		// Cloud instances report slightly less memory than their nominal size; allow 5%.
		if int64(p.Cores)*1000 >= cpuMilli && float64(int64(p.MemoryMiB)<<20) >= float64(memBytes)*0.95 {
			return p, true
		}
	}
	return pricing.Plan{}, false
}

func recommendations(snap *collect.Snapshot, rows []WorkloadRow, z *pricing.UpCloudZone, sc []Scenario) []Recommendation {
	var out []Recommendation
	cpuEUR, memEUR := unitPrices(z)
	for _, w := range rows {
		if w.Samples < 60 || w.Pods == 0 {
			continue
		}
		perCPU, perMem := w.ReqCPUMilli/int64(w.Pods), w.ReqMemory/int64(w.Pods)
		switch {
		case perMem > 0 && w.UsageMemMaxBytes/int64(w.Pods) > perMem:
			out = append(out, Recommendation{Kind: "Raise memory request", Target: w.Key,
				Detail: fmt.Sprintf("uses up to %s per pod but requests %s: pods can be evicted or crowd out others. Set requests.memory: %s.",
					mib(w.UsageMemMaxBytes/int64(w.Pods)), mib(perMem), mib(w.SuggestMemBytes))})
		case perCPU == 0 && perMem == 0:
			out = append(out, Recommendation{Kind: "Set requests", Target: w.Key,
				Detail: fmt.Sprintf("no requests, so the scheduler cannot plan for it. Suggested: cpu %dm, memory %s.", w.SuggestCPUMilli, mib(w.SuggestMemBytes))})
		default:
			saveCPU := max(perCPU-w.SuggestCPUMilli, 0) * int64(w.Pods)
			saveMem := max(perMem-w.SuggestMemBytes, 0) * int64(w.Pods)
			if saveCPU >= 200 || saveMem >= 512<<20 {
				eur := float64(saveCPU)/1000*cpuEUR + float64(saveMem)/gib*memEUR
				out = append(out, Recommendation{Kind: "Lower requests", Target: w.Key, SavingsEUR: eur,
					Detail: fmt.Sprintf("requests cpu %dm / memory %s per pod, uses p95 %dm / peak %s. Set cpu %dm, memory %s.",
						perCPU, mib(perMem), w.UsageCPUP95Milli/int64(w.Pods), mib(w.UsageMemMaxBytes/int64(w.Pods)), w.SuggestCPUMilli, mib(w.SuggestMemBytes))})
			}
		}
	}
	var loose float64
	var names []string
	for _, v := range snap.Volumes {
		if v.Phase != "Bound" {
			loose += v.GB
			names = append(names, v.Name)
		}
	}
	if loose > 0 {
		out = append(out, Recommendation{Kind: "Delete unused volumes", Target: strings.Join(names, ", "), SavingsEUR: loose * z.StorageMaxIOPSMonth,
			Detail: fmt.Sprintf("%.0f GB in volumes no claim uses (Released or Available). Back up, then delete.", loose)})
	}
	if n := len(snap.LoadBalancers); n > 1 {
		out = append(out, Recommendation{Kind: "Share one load balancer", Target: strings.Join(snap.LoadBalancers, ", "), SavingsEUR: float64(n-1) * z.LoadBalancerMonthly,
			Detail: fmt.Sprintf("%d Services of type LoadBalancer. Route HTTP(S) through one ingress controller instead.", n)})
	}
	if len(sc) == 3 && sc[1].Error == "" && sc[2].Error == "" && sc[1].TotalEUR-sc[2].TotalEUR > 1 {
		from, to := sc[1].Nodes[0], sc[2].Nodes[0]
		detail := fmt.Sprintf("with the request changes above the cluster needs %d × %s instead of %d × %s", to.Count, to.Plan, from.Count, from.Plan)
		if len(snap.LoadBalancers) > 1 {
			detail += ", plus one shared load balancer"
		}
		out = append(out, Recommendation{Kind: "Right-size the cluster", Target: "all workloads", SavingsEUR: sc[1].TotalEUR - sc[2].TotalEUR, Detail: detail + "."})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SavingsEUR > out[j].SavingsEUR })
	return out
}

// unitPrices estimates EUR per vCPU-month and per GiB-month from the plan list (cheapest plan with 4 GiB per vCPU).
func unitPrices(z *pricing.UpCloudZone) (cpu, mem float64) {
	for _, p := range z.Plans {
		if p.Cores >= 2 && p.MemoryMiB == p.Cores*4096 {
			// split the price evenly between CPU and memory
			return p.Monthly / 2 / float64(p.Cores), p.Monthly / 2 / (float64(p.MemoryMiB) / 1024)
		}
	}
	return 0, 0
}

func notes(r *Report, o Options) []string {
	var n []string
	switch h := r.CoverageHours; {
	case r.Samples == 0:
		n = append(n, "No usage data yet (metrics-server not reachable or just started). Right-sized numbers use requests until usage comes in.")
	case h < 24:
		n = append(n, fmt.Sprintf("Usage collected for %.1f hours. Leave the analyzer running for at least 7 days to cover weekly peaks before acting on right-sizing.", h))
	case h < 24*7:
		n = append(n, fmt.Sprintf("Usage collected for %.1f days. A full week covers weekly peaks.", h/24))
	}
	n = append(n, fmt.Sprintf("UpCloud zone %s, list prices in EUR excluding VAT. Current costs are list prices converted to EUR with the ECB rate of %s.", r.Zone, r.PriceDates["fx"]))
	if r.Current.Provider == "azure" {
		n = append(n, "Azure: control plane counted at the AKS Standard tier. Clusters on the Free tier pay no control plane fee.")
	}
	if o.MinNodes > 0 {
		n = append(n, fmt.Sprintf("Packed scenarios keep at least %d nodes and %.0f%% of each node free for the system.", o.MinNodes, 100*o.NodeReserve))
	}
	return n
}

func ceilDiv(a, b int64) int {
	if a <= 0 {
		return 0
	}
	return int((a + b - 1) / b)
}

func roundUp(v, step int64) int64 { return (v + step - 1) / step * step }

func mib(b int64) string {
	if b >= gib && b%(gib/4) == 0 {
		return fmt.Sprintf("%.2gGi", float64(b)/gib)
	}
	return fmt.Sprintf("%dMi", b>>20)
}

func top(m map[string]int) string {
	best, n := "", -1
	for k, v := range m {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	return best
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
