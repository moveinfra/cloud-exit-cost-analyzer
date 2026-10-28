package pricing

import "testing"

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
