// Command analyzer runs inside a Kubernetes cluster, samples what it runs and uses, and serves a report of what
// the cluster costs today and would cost on UpCloud. Configuration is by environment variable (see the Helm chart).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/collect"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/estimate"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/pricing"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/usage"
	"github.com/moveinfra/cloud-exit-cost-analyzer/internal/web"
)

var version = "dev" // set at build time

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	opts := estimate.Options{
		Zone:                env("UPCLOUD_ZONE", "de-fra1"),
		MinNodes:            int(num("MIN_NODES", 3)),
		CPUHeadroom:         num("CPU_HEADROOM", 0.25),
		MemoryHeadroom:      num("MEMORY_HEADROOM", 0.15),
		NodeReserve:         num("NODE_RESERVE", 0.1),
		CurrentDiscount:     num("CURRENT_DISCOUNT", 0),
		PriceSpotAsOnDemand: env("PRICE_SPOT_AS_ON_DEMAND", "false") == "true",
	}
	if f := os.Getenv("PLAN_FAMILIES"); f != "" {
		opts.PlanFamilies = strings.Split(f, ",")
	}
	if raw := os.Getenv("INSTANCE_PRICES"); raw != "" {
		// numbers from a values file, strings from helm --set
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return errors.New("INSTANCE_PRICES must be a JSON object of instance type to price per hour")
		}
		opts.InstancePrices = map[string]float64{}
		for k, v := range m {
			f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
			if err != nil {
				return fmt.Errorf("INSTANCE_PRICES: price for %s is not a number: %v", k, v)
			}
			opts.InstancePrices[k] = f
		}
	}
	interval, err := time.ParseDuration(env("SAMPLE_INTERVAL", "60s"))
	if err != nil || interval < 15*time.Second {
		return errors.New("SAMPLE_INTERVAL must be a duration of at least 15s")
	}
	dataFile := filepath.Join(env("DATA_DIR", "/data"), "usage.json")

	book, err := pricing.Load()
	if err != nil {
		return err
	}
	if _, err := book.UpCloud.Zone(opts.Zone, opts.PlanFamilies...); err != nil {
		return err
	}

	cfg, err := rest.InClusterConfig()
	if err != nil { // local development: use the current kubeconfig context
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), nil).ClientConfig()
		if err != nil {
			return err
		}
	}
	cfg.UserAgent = "cloud-exit-cost-analyzer/" + version
	c := &collect.Collector{Kube: kubernetes.NewForConfigOrDie(cfg), Metrics: metrics.NewForConfigOrDie(cfg)}

	store, err := usage.Load(dataFile)
	if err != nil {
		log.Warn("starting with empty usage history", "file", dataFile, "err", err)
		store = usage.New()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var mu sync.Mutex
	var snap *collect.Snapshot
	sample := func() {
		ctx, cancel := context.WithTimeout(ctx, interval)
		defer cancel()
		s, err := c.Snapshot(ctx)
		if err != nil {
			log.Error("snapshot", "err", err)
			return
		}
		mu.Lock()
		snap = s
		mu.Unlock()
		u, err := c.Usage(ctx, s)
		if err != nil {
			log.Warn("usage", "err", err)
			return
		}
		store.Add(s.Time, u)
	}

	srv := &http.Server{Addr: env("LISTEN_ADDR", ":8080"), ReadHeaderTimeout: 10 * time.Second,
		Handler: web.Handler(func() (*estimate.Report, error) {
			mu.Lock()
			s := snap
			mu.Unlock()
			if s == nil {
				return nil, errors.New("collecting the first snapshot, reload in a few seconds")
			}
			return estimate.Build(s, store, book, opts)
		})}
	go func() {
		log.Info("serving", "addr", srv.Addr, "version", version, "zone", opts.Zone)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
			stop()
		}
	}()

	save := func() {
		store.Prune(time.Now(), 14*24*time.Hour)
		if err := store.Save(dataFile); err != nil {
			log.Warn("save usage history", "file", dataFile, "err", err)
		}
	}
	sample()
	tick, saveTick := time.NewTicker(interval), time.NewTicker(5*time.Minute)
	defer tick.Stop()
	defer saveTick.Stop()
	for {
		select {
		case <-tick.C:
			sample()
		case <-saveTick.C:
			save()
		case <-ctx.Done():
			save()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(shutdown)
		}
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func num(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}
