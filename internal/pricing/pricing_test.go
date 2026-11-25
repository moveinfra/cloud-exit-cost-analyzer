package pricing

import "testing"

// The embedded data must load and give every provider usable prices.
func TestLoadAndTargets(t *testing.T) {
	b, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if ids := b.TargetIDs(); len(ids) != 4 || ids[0] != "hetzner" || ids[3] != "upcloud" {
		t.Fatalf("targets: %v", ids)
	}
	for _, id := range b.TargetIDs() {
		tg, err := b.Target(id, "de-fra1")
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if len(tg.Plans) < 5 || tg.StorageGBMonth <= 0 || tg.LoadBalancerMonthly <= 0 || tg.Name == "" || tg.Zone == "" || tg.Generated == "" {
			t.Errorf("%s: incomplete prices: %d plans, storage %v, lb %v", id, len(tg.Plans), tg.StorageGBMonth, tg.LoadBalancerMonthly)
		}
		if (tg.ControlPlaneMonthly > 0) == tg.SelfManaged {
			t.Errorf("%s: control plane %.2f but selfManaged=%v", id, tg.ControlPlaneMonthly, tg.SelfManaged)
		}
		for i, p := range tg.Plans {
			if p.Cores < 1 || p.MemoryMiB < 512 || p.Monthly <= 0 || (i > 0 && p.Monthly < tg.Plans[i-1].Monthly) {
				t.Errorf("%s: bad or unsorted plan %+v", id, p)
			}
			if f := p.Family(); id != "upcloud" && f != "general" && f != "compute" && f != "memory" {
				t.Errorf("%s: %s is %s, not a dedicated-CPU family", id, p.Name, f)
			}
		}
	}
	if _, err := b.Target("nowhere", ""); err == nil {
		t.Fatal("unknown provider should fail")
	}
}

func TestFamilyAndZoneFilter(t *testing.T) {
	for name, want := range map[string]string{"2xCPU-4GB": "general", "HIMEM-4xCPU-32GB": "HIMEM", "DEV-1xCPU-1GB-10GB": "DEV",
		"CLOUDNATIVE-4xCPU-8GB": "CLOUDNATIVE", "STARTER-1xCPU-1GB": "STARTER"} {
		if got := (Plan{Name: name}).Family(); got != want {
			t.Errorf("%s: family %q, want %q", name, got, want)
		}
	}
	u := &UpCloud{Zones: map[string]*UpCloudZone{"de-fra1": {Plans: []Plan{
		{Name: "DEV-1xCPU-1GB", Monthly: 4.5}, {Name: "2xCPU-4GB", Monthly: 20}, {Name: "HIMEM-2xCPU-8GB", Monthly: 30}}}}}
	z, err := u.Zone("de-fra1", "general", "HIMEM")
	if err != nil || len(z.Plans) != 2 || z.Plans[0].Name != "2xCPU-4GB" {
		t.Fatalf("filtered zone: %+v %v", z, err)
	}
	if len(u.Zones["de-fra1"].Plans) != 3 {
		t.Fatal("Zone must not modify the price book")
	}
	if _, err := u.Zone("xx-nowhere1"); err == nil {
		t.Fatal("unknown zone should fail")
	}
}
