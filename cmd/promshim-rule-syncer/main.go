package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/rulesync"
	monitoringclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	var (
		kubeconfig      = flag.String("kubeconfig", getenv("KUBECONFIG", ""), "Path to kubeconfig. Defaults to in-cluster config when empty.")
		namespaces      = flag.String("namespaces", getenv("PROM_SHIM_RULE_SYNC_NAMESPACES", ""), "Comma-separated PrometheusRule namespaces. Empty means all namespaces.")
		ruleSelectorRaw = flag.String("rule-selector", getenv("PROM_SHIM_RULE_SYNC_SELECTOR", ""), "Kubernetes label selector for PrometheusRule objects.")
		outDir          = flag.String("output-dir", getenv("PROM_SHIM_RULE_SYNC_OUTPUT_DIR", rulesync.DefaultOutputDir), "Directory for rendered rule files.")
		promVersion     = flag.String("prometheus-version", getenv("PROM_SHIM_RULE_SYNC_PROMETHEUS_VERSION", rulesync.DefaultPrometheusVer), "Prometheus version used for rule validation compatibility.")
		syncInterval    = flag.Duration("sync-interval", getenvDuration("PROM_SHIM_RULE_SYNC_INTERVAL", 30*time.Second), "Periodic sync interval. Ignored with --once.")
		listenAddr      = flag.String("listen-addr", getenv("PROM_SHIM_RULE_SYNC_LISTEN_ADDR", ":9091"), "HTTP listen address for /metrics and /health. Empty disables HTTP serving.")
		once            = flag.Bool("once", getenvBool("PROM_SHIM_RULE_SYNC_ONCE", false), "Run one sync and exit.")
	)
	flag.Parse()

	if *outDir == "" {
		logger.Error("missing required --output-dir")
		os.Exit(2)
	}
	ruleSelector := labels.Everything()
	if strings.TrimSpace(*ruleSelectorRaw) != "" {
		parsed, err := labels.Parse(*ruleSelectorRaw)
		if err != nil {
			logger.Error("parse rule selector", "err", err)
			os.Exit(2)
		}
		ruleSelector = parsed
	}

	config, err := kubernetesConfig(*kubeconfig)
	if err != nil {
		logger.Error("load Kubernetes config", "err", err)
		os.Exit(1)
	}
	monitoring, err := monitoringclient.NewForConfig(config)
	if err != nil {
		logger.Error("create monitoring client", "err", err)
		os.Exit(1)
	}
	syncer, err := rulesync.New(monitoring, nil, logger, rulesync.Options{
		Namespaces:    rulesync.SplitCSV(*namespaces),
		RuleSelector:  ruleSelector,
		OutputDir:     *outDir,
		PrometheusVer: *promVersion,
		SyncInterval:  *syncInterval,
		Once:          *once,
	})
	if err != nil {
		logger.Error("configure syncer", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrCh := make(chan error, 1)
	if *listenAddr != "" && !*once {
		server := &http.Server{Addr: *listenAddr, ReadHeaderTimeout: 10 * time.Second}
		mux := http.NewServeMux()
		mux.Handle("/metrics", syncer.MetricsHandler())
		mux.Handle("/health", syncer.HealthHandler())
		mux.Handle("/-/healthy", syncer.HealthHandler())
		mux.Handle("/-/ready", syncer.HealthHandler())
		server.Handler = mux
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
		go func() {
			logger.Info("serving rule syncer diagnostics", "addr", *listenAddr)
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				select {
				case serverErrCh <- err:
				default:
				}
				stop()
			}
		}()
	}
	runErr := syncer.Run(ctx)
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		logger.Error("run syncer", "err", runErr)
		os.Exit(1)
	}
	select {
	case err := <-serverErrCh:
		logger.Error("serve diagnostics", "err", err)
		os.Exit(1)
	default:
	}
}

func kubernetesConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	case "":
		return fallback
	default:
		return fallback
	}
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil && parsed >= 0 {
		return parsed
	}
	return fallback
}
