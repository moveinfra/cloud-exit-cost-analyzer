// Package collect reads what a cluster runs (nodes, workloads, volumes, load balancers) and how much
// CPU and memory the workloads use right now (metrics-server). Read-only: it only lists and gets.
package collect

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Node is one worker node and where it runs.
type Node struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"` // aws, azure, gcp, digitalocean, upcloud, or "" when unknown
	Region        string `json:"region"`
	InstanceType  string `json:"instanceType"`
	Pool          string `json:"pool,omitempty"`
	Spot          bool   `json:"spot,omitempty"`
	Serverless    bool   `json:"serverless,omitempty"` // EKS Fargate, AKS virtual nodes: not priced as servers
	CPUMilli      int64  `json:"cpuMilli"`
	MemoryBytes   int64  `json:"memoryBytes"`
	AllocCPUMilli int64  `json:"allocatableCpuMilli"`
	AllocMemory   int64  `json:"allocatableMemoryBytes"`
}

// Workload groups the running pods of one Deployment, StatefulSet, DaemonSet, Job or bare pod.
type Workload struct {
	Key         string `json:"key"` // namespace/Kind/name
	Namespace   string `json:"namespace"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Pods        int    `json:"pods"`
	ReqCPUMilli int64  `json:"requestCpuMilli"`    // sum over its pods
	ReqMemory   int64  `json:"requestMemoryBytes"` // sum over its pods
	MaxPodCPU   int64  `json:"maxPodRequestCpuMilli"`
	MaxPodMem   int64  `json:"maxPodRequestMemoryBytes"`
}

// Volume is one persistent volume.
type Volume struct {
	Name         string  `json:"name"`
	Claim        string  `json:"claim,omitempty"` // namespace/name when bound
	StorageClass string  `json:"storageClass,omitempty"`
	Type         string  `json:"type,omitempty"` // e.g. gp3, from the storage class parameters
	GB           float64 `json:"gb"`
	Phase        string  `json:"phase"` // Bound, Released, Available, ...
}

// Snapshot is the cluster inventory at one point in time.
type Snapshot struct {
	Time          time.Time  `json:"time"`
	Nodes         []Node     `json:"nodes"`
	Workloads     []Workload `json:"workloads"`
	Volumes       []Volume   `json:"volumes"`
	LoadBalancers []string   `json:"loadBalancers"` // namespace/name of Services of type LoadBalancer
	podOwner      map[string]string
}

// Usage is CPU and memory in use, keyed by workload key.
type Usage map[string]struct{ CPUMilli, MemoryBytes int64 }

type Collector struct {
	Kube    kubernetes.Interface
	Metrics metrics.Interface
}

func (c *Collector) Snapshot(ctx context.Context) (*Snapshot, error) {
	s := &Snapshot{Time: time.Now().UTC(), podOwner: map[string]string{}}
	nodes, err := c.Kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	for i := range nodes.Items {
		s.Nodes = append(s.Nodes, toNode(&nodes.Items[i]))
	}

	pods, err := c.Kube.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	byKey := map[string]*Workload{}
	var order []string
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		kind, name := owner(p)
		key := p.Namespace + "/" + kind + "/" + name
		s.podOwner[p.Namespace+"/"+p.Name] = key
		w := byKey[key]
		if w == nil {
			w = &Workload{Key: key, Namespace: p.Namespace, Kind: kind, Name: name}
			byKey[key] = w
			order = append(order, key)
		}
		cpu, mem := podRequests(p)
		w.Pods++
		w.ReqCPUMilli += cpu
		w.ReqMemory += mem
		w.MaxPodCPU = max(w.MaxPodCPU, cpu)
		w.MaxPodMem = max(w.MaxPodMem, mem)
	}
	for _, k := range order {
		s.Workloads = append(s.Workloads, *byKey[k])
	}

	classes := map[string]string{}
	if scs, err := c.Kube.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{}); err == nil {
		for _, sc := range scs.Items {
			t := sc.Parameters["type"]
			if t == "" {
				t = sc.Parameters["skuName"] // Azure disk CSI
			}
			if t == "" {
				t = sc.Parameters["tier"] // UpCloud CSI: maxiops, standard, hdd
			}
			classes[sc.Name] = t
		}
	}
	pvs, err := c.Kube.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list persistent volumes: %w", err)
	}
	for _, pv := range pvs.Items {
		q := pv.Spec.Capacity[corev1.ResourceStorage]
		v := Volume{Name: pv.Name, StorageClass: pv.Spec.StorageClassName, Type: classes[pv.Spec.StorageClassName],
			GB: float64(q.Value()) / 1e9, Phase: string(pv.Status.Phase)}
		if ref := pv.Spec.ClaimRef; ref != nil && pv.Status.Phase == corev1.VolumeBound {
			v.Claim = ref.Namespace + "/" + ref.Name
		}
		if v.Type == "" && pv.Spec.AWSElasticBlockStore != nil {
			v.Type = "gp2" // in-tree EBS default
		}
		s.Volumes = append(s.Volumes, v)
	}

	svcs, err := c.Kube.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	for _, svc := range svcs.Items {
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
			s.LoadBalancers = append(s.LoadBalancers, svc.Namespace+"/"+svc.Name)
		}
	}
	return s, nil
}

// Usage reads current pod usage from metrics-server and sums it per workload of the snapshot.
func (c *Collector) Usage(ctx context.Context, s *Snapshot) (Usage, error) {
	list, err := c.Metrics.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("metrics-server (metrics.k8s.io): %w", err)
	}
	u := Usage{}
	for _, pm := range list.Items {
		key, ok := s.podOwner[pm.Namespace+"/"+pm.Name]
		if !ok {
			continue
		}
		e := u[key]
		for _, ctr := range pm.Containers {
			e.CPUMilli += ctr.Usage.Cpu().MilliValue()
			e.MemoryBytes += ctr.Usage.Memory().Value()
		}
		u[key] = e
	}
	return u, nil
}

func toNode(n *corev1.Node) Node {
	l := n.Labels
	out := Node{
		Name:          n.Name,
		Provider:      provider(n),
		Region:        first(l, "topology.kubernetes.io/region", "failure-domain.beta.kubernetes.io/region"),
		InstanceType:  first(l, "node.kubernetes.io/instance-type", "beta.kubernetes.io/instance-type"),
		Pool:          first(l, "eks.amazonaws.com/nodegroup", "karpenter.sh/nodepool", "kubernetes.azure.com/agentpool", "agentpool", "cloud.google.com/gke-nodepool", "doks.digitalocean.com/node-pool"),
		CPUMilli:      n.Status.Capacity.Cpu().MilliValue(),
		MemoryBytes:   n.Status.Capacity.Memory().Value(),
		AllocCPUMilli: n.Status.Allocatable.Cpu().MilliValue(),
		AllocMemory:   n.Status.Allocatable.Memory().Value(),
	}
	out.Spot = strings.EqualFold(l["eks.amazonaws.com/capacityType"], "SPOT") || l["karpenter.sh/capacity-type"] == "spot" ||
		l["kubernetes.azure.com/scalesetpriority"] == "spot" || l["cloud.google.com/gke-spot"] == "true" || l["cloud.google.com/gke-preemptible"] == "true"
	out.Serverless = l["eks.amazonaws.com/compute-type"] == "fargate" || l["type"] == "virtual-kubelet"
	return out
}

func provider(n *corev1.Node) string {
	id := n.Spec.ProviderID
	for prefix, p := range map[string]string{"aws://": "aws", "azure://": "azure", "gce://": "gcp", "digitalocean://": "digitalocean", "upcloud://": "upcloud"} {
		if strings.HasPrefix(id, prefix) {
			return p
		}
	}
	switch {
	case n.Labels["eks.amazonaws.com/nodegroup"] != "" || n.Labels["eks.amazonaws.com/compute-type"] != "":
		return "aws"
	case n.Labels["kubernetes.azure.com/cluster"] != "":
		return "azure"
	case n.Labels["cloud.google.com/gke-nodepool"] != "":
		return "gcp"
	case n.Labels["doks.digitalocean.com/node-pool"] != "":
		return "digitalocean"
	}
	return ""
}

var hashSuffix = regexp.MustCompile(`-[0-9]{8,}$`) // Jobs created by a CronJob: <cronjob>-<scheduled minute>

// owner names the controller that keeps the pod running, so restarts and rollouts land on the same workload.
func owner(p *corev1.Pod) (kind, name string) {
	for _, ref := range p.OwnerReferences {
		if ref.Controller == nil || !*ref.Controller {
			continue
		}
		switch ref.Kind {
		case "ReplicaSet":
			if h := p.Labels["pod-template-hash"]; h != "" && strings.HasSuffix(ref.Name, "-"+h) {
				return "Deployment", strings.TrimSuffix(ref.Name, "-"+h)
			}
		case "Job":
			if hashSuffix.MatchString(ref.Name) {
				return "CronJob", hashSuffix.ReplaceAllString(ref.Name, "")
			}
		}
		return ref.Kind, ref.Name
	}
	return "Pod", p.Name
}

// podRequests is what the scheduler reserves: the larger of the sum of app containers and the largest init container.
func podRequests(p *corev1.Pod) (cpu, mem int64) {
	for _, c := range p.Spec.Containers {
		cpu += c.Resources.Requests.Cpu().MilliValue()
		mem += c.Resources.Requests.Memory().Value()
	}
	for _, c := range p.Spec.InitContainers {
		cpu = max(cpu, c.Resources.Requests.Cpu().MilliValue())
		mem = max(mem, c.Resources.Requests.Memory().Value())
	}
	if o := p.Spec.Overhead; o != nil {
		cpu += o.Cpu().MilliValue()
		mem += o.Memory().Value()
	}
	return cpu, mem
}

func first(l map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := l[k]; v != "" {
			return v
		}
	}
	return ""
}
