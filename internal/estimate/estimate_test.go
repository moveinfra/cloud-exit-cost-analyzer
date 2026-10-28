package estimate_test

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kfake "k8s.io/client-go/kubernetes/fake"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	mfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/collect"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/estimate"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/pricing"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/usage"
)

func q(s string) resource.Quantity { return resource.MustParse(s) }

func node(name, typ string, cpu, mem string, spot bool) *corev1.Node {
	l := map[string]string{"node.kubernetes.io/instance-type": typ, "topology.kubernetes.io/region": "eu-central-1", "eks.amazonaws.com/nodegroup": "main"}
	if spot {
		l["eks.amazonaws.com/capacityType"] = "SPOT"
	}
	res := corev1.ResourceList{corev1.ResourceCPU: q(cpu), corev1.ResourceMemory: q(mem)}
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: l},
		Spec:   corev1.NodeSpec{ProviderID: "aws:///eu-central-1a/i-" + name},
		Status: corev1.NodeStatus{Capacity: res, Allocatable: res}}
}

func pod(ns, name, ownerKind, ownerName, hash, cpu, mem string) *corev1.Pod {
	yes := true
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: map[string]string{},
		OwnerReferences: []metav1.OwnerReference{{Kind: ownerKind, Name: ownerName, Controller: &yes}}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: q(cpu), corev1.ResourceMemory: q(mem)}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if hash != "" {
		p.Labels["pod-template-hash"] = hash
	}
	return p
}

func podMetrics(ns, name, cpu, mem string) *metricsv1beta1.PodMetrics {
	return &metricsv1beta1.PodMetrics{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Containers: []metricsv1beta1.ContainerMetrics{{Name: "app", Usage: corev1.ResourceList{corev1.ResourceCPU: q(cpu), corev1.ResourceMemory: q(mem)}}}}
}

func book() *pricing.Book {
	return &pricing.Book{
		Clouds: map[string]*pricing.Cloud{"aws": {Provider: "aws", Currency: "USD", Generated: "2026-10-28", Regions: map[string]*pricing.Region{
			"eu-central-1": {Instances: map[string]float64{"m5.xlarge": 0.23}, StorageGBMonth: map[string]float64{"gp3": 0.0952},
				ControlPlaneHourly: 0.10, LoadBalancerHourly: 0.027}}}},
		UpCloud: &pricing.UpCloud{Currency: "EUR", Generated: "2026-10-28", Zones: map[string]*pricing.UpCloudZone{"de-fra1": {
			StorageMaxIOPSMonth: 0.22, LoadBalancerMonthly: 10, ControlPlaneMonthly: 30,
			Plans: []pricing.Plan{
				{Name: "2xCPU-4GB", Cores: 2, MemoryMiB: 4096, Monthly: 20},
				{Name: "4xCPU-8GB", Cores: 4, MemoryMiB: 8192, Monthly: 40},
				{Name: "4xCPU-16GB", Cores: 4, MemoryMiB: 16384, Monthly: 55},
				{Name: "8xCPU-32GB", Cores: 8, MemoryMiB: 32768, Monthly: 110},
			}}}},
		FX: &pricing.FX{Date: "2026-10-28", Rates: map[string]float64{"USD": 1.10}},
	}
}

func cluster(t *testing.T) (*collect.Snapshot, *usage.Store) {
	t.Helper()
	gp3 := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "gp3"}, Provisioner: "ebs.csi.aws.com", Parameters: map[string]string{"type": "gp3"}}
	pv := func(name string, gb string, phase corev1.PersistentVolumePhase) *corev1.PersistentVolume {
		return &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: corev1.PersistentVolumeSpec{StorageClassName: "gp3", Capacity: corev1.ResourceList{corev1.ResourceStorage: q(gb)},
				ClaimRef: &corev1.ObjectReference{Namespace: "shop", Name: "data-" + name}},
			Status: corev1.PersistentVolumeStatus{Phase: phase}}
	}
	lb := func(name string) *corev1.Service {
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: name}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer}}
	}
	objs := []runtime.Object{
		node("a", "m5.xlarge", "4", "16Gi", false), node("b", "m5.xlarge", "4", "16Gi", false),
		node("c", "m5.xlarge", "4", "16Gi", false), node("d", "m5.xlarge", "4", "16Gi", true),
		// web: 3 pods requesting 1 CPU / 2Gi each, using ~100m / 300Mi
		pod("shop", "web-7d9f8-a", "ReplicaSet", "web-7d9f8", "7d9f8", "1", "2Gi"),
		pod("shop", "web-7d9f8-b", "ReplicaSet", "web-7d9f8", "7d9f8", "1", "2Gi"),
		pod("shop", "web-7d9f8-c", "ReplicaSet", "web-7d9f8", "7d9f8", "1", "2Gi"),
		// db: under-requested memory
		pod("shop", "db-0", "StatefulSet", "db", "", "500m", "1Gi"),
		// agent: DaemonSet on every node
		pod("kube-system", "agent-1", "DaemonSet", "agent", "", "100m", "128Mi"),
		pod("kube-system", "agent-2", "DaemonSet", "agent", "", "100m", "128Mi"),
		pod("kube-system", "agent-3", "DaemonSet", "agent", "", "100m", "128Mi"),
		pod("kube-system", "agent-4", "DaemonSet", "agent", "", "100m", "128Mi"),
		gp3, pv("pv-1", "100Gi", corev1.VolumeBound), pv("pv-2", "50Gi", corev1.VolumeReleased),
		lb("web"), lb("admin"),
	}
	done := pod("shop", "migrate-x", "Job", "migrate", "", "2", "4Gi")
	done.Status.Phase = corev1.PodSucceeded
	objs = append(objs, done)

	m := mfake.NewSimpleClientset()
	for _, pm := range []*metricsv1beta1.PodMetrics{
		podMetrics("shop", "web-7d9f8-a", "100m", "300Mi"), podMetrics("shop", "web-7d9f8-b", "90m", "280Mi"),
		podMetrics("shop", "web-7d9f8-c", "110m", "310Mi"), podMetrics("shop", "db-0", "300m", "1500Mi"),
		podMetrics("kube-system", "agent-1", "20m", "60Mi"),
	} {
		gvr := metricsv1beta1.SchemeGroupVersion.WithResource("pods")
		if err := m.Tracker().Create(gvr, pm, pm.Namespace); err != nil {
			t.Fatal(err)
		}
	}
	c := &collect.Collector{Kube: kfake.NewSimpleClientset(objs...), Metrics: m}
	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := usage.New()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 24*60; i++ { // one day of one-minute samples
		u, err := c.Usage(context.Background(), snap)
		if err != nil {
			t.Fatal(err)
		}
		store.Add(start.Add(time.Duration(i)*time.Minute), u)
	}
	return snap, store
}

func TestSnapshot(t *testing.T) {
	snap, _ := cluster(t)
	if len(snap.Nodes) != 4 || snap.Nodes[0].Provider != "aws" || snap.Nodes[0].InstanceType != "m5.xlarge" || !snap.Nodes[3].Spot {
		t.Fatalf("nodes: %+v", snap.Nodes)
	}
	keys := map[string]collect.Workload{}
	for _, w := range snap.Workloads {
		keys[w.Key] = w
	}
	web := keys["shop/Deployment/web"]
	if web.Pods != 3 || web.ReqCPUMilli != 3000 {
		t.Fatalf("web workload: %+v (all: %v)", web, keys)
	}
	if _, ok := keys["shop/Job/migrate"]; ok {
		t.Fatal("finished pods must not count")
	}
	if len(snap.Volumes) != 2 || snap.Volumes[0].Type != "gp3" || len(snap.LoadBalancers) != 2 {
		t.Fatalf("volumes %+v, lbs %v", snap.Volumes, snap.LoadBalancers)
	}
}

func TestBuild(t *testing.T) {
	snap, store := cluster(t)
	r, err := estimate.Build(snap, store, book(), estimate.Options{})
	if err != nil {
		t.Fatal(err)
	}

	// Today: 3 on-demand m5.xlarge (spot left out) + EKS + 150 GB gp3 + 2 NLB.
	want := 3*0.23*730 + 0.10*730 + 150*1.073741824*0.0952 + 2*0.027*730
	if d := r.Current.Total - want; d > 0.5 || d < -0.5 {
		t.Fatalf("current total %.2f, want %.2f; lines %+v", r.Current.Total, want, r.Current.Lines)
	}
	if !strings.Contains(strings.Join(r.Current.Missing, ";"), "1 spot node") {
		t.Fatalf("spot node not reported as missing: %v", r.Current.Missing)
	}

	byID := map[string]estimate.Scenario{}
	for _, s := range r.Scenarios {
		if s.Error != "" {
			t.Fatalf("scenario %s: %s", s.ID, s.Error)
		}
		byID[s.ID] = s
	}
	if n := byID["same"].Nodes; len(n) != 1 || n[0].Plan != "4xCPU-16GB" || n[0].Count != 4 {
		t.Fatalf("same servers: %+v", n)
	}
	if byID["rightsized"].TotalEUR >= byID["requests"].TotalEUR {
		t.Fatalf("right-sizing should cost less: %.2f vs %.2f", byID["rightsized"].TotalEUR, byID["requests"].TotalEUR)
	}
	if byID["requests"].Nodes[0].Count < 3 {
		t.Fatalf("min nodes not respected: %+v", byID["requests"].Nodes)
	}

	kinds := map[string]string{}
	for _, rec := range r.Recommendations {
		kinds[rec.Kind+" "+rec.Target] = rec.Detail
	}
	for _, k := range []string{"Lower requests shop/Deployment/web", "Raise memory request shop/StatefulSet/db", "Delete unused volumes pv-2", "Share one load balancer shop/admin, shop/web"} {
		if _, ok := kinds[k]; !ok {
			t.Errorf("missing recommendation %q; got %v", k, kinds)
		}
	}
	if r.Warning == "" || !strings.Contains(r.Warning, "hours") {
		t.Errorf("expected a coverage warning for one day of data, got %q", r.Warning)
	}
}

func TestAlreadyOnUpCloud(t *testing.T) {
	snap := &collect.Snapshot{
		Nodes: []collect.Node{
			{Name: "n1", Provider: "upcloud", Region: "de-fra1", CPUMilli: 4000, MemoryBytes: 16 << 30},
			{Name: "n2", Provider: "upcloud", Region: "de-fra1", CPUMilli: 4000, MemoryBytes: 16 << 30},
		},
		Volumes:       []collect.Volume{{Name: "pv", Type: "maxiops", GB: 100, Phase: "Bound"}},
		LoadBalancers: []string{"ns/web"},
	}
	r, err := estimate.Build(snap, usage.New(), book(), estimate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 2 × 4xCPU-16GB (55) + UKS 30 + 100 GB × 0.22 + LB 10
	if want := 2*55.0 + 30 + 22 + 10; r.Current.Currency != "EUR" || r.Current.TotalEUR < want-0.01 || r.Current.TotalEUR > want+0.01 {
		t.Fatalf("current %s %.2f, want EUR %.2f: %+v", r.Current.Currency, r.Current.TotalEUR, want, r.Current.Lines)
	}
	if r.Recommendations == nil || r.Scenarios[0].Lines == nil {
		t.Fatal("lists must be empty, not nil, so the JSON has [] instead of null")
	}
}
