package server

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/audit"
	"github.com/heihuzicity-tech/kubejojo/server/internal/auth"
	"github.com/heihuzicity-tech/kubejojo/server/internal/buildinfo"
	"github.com/heihuzicity-tech/kubejojo/server/internal/cluster"
	"github.com/heihuzicity-tech/kubejojo/server/internal/config"
	"github.com/heihuzicity-tech/kubejojo/server/internal/evidence"
	"github.com/heihuzicity-tech/kubejojo/server/internal/experiment"
	"github.com/heihuzicity-tech/kubejojo/server/internal/kube"
	"github.com/heihuzicity-tech/kubejojo/server/internal/llm"
	"github.com/heihuzicity-tech/kubejojo/server/internal/remediation"
	"github.com/heihuzicity-tech/kubejojo/server/internal/service"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
	"github.com/heihuzicity-tech/kubejojo/server/internal/web"
)

func Run(info buildinfo.Info) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if info.IsRelease() && !web.HasEmbeddedFrontend() {
		return fmt.Errorf("release build requires embedded frontend assets under server/internal/web/dist/app")
	}

	db, err := store.Open(cfg.AIOps.DBPath)
	if err != nil {
		return fmt.Errorf("initialize store: %w", err)
	}
	defer db.Close()

	// One shared Kubernetes client per process; every request reuses it.
	sharedClient, err := kube.NewSharedClient(cfg.KubeconfigPath, kube.Options{
		Timeout: cfg.Cluster.Timeout,
		QPS:     cfg.Cluster.QPS,
		Burst:   cfg.Cluster.Burst,
	})
	if err != nil {
		return fmt.Errorf("initialize shared kubernetes client: %w", err)
	}

	clusterService := service.NewClusterService(sharedClient)
	probe := cluster.NewProbe(
		sharedClient.Kubernetes.Discovery(),
		sharedClient.Kubernetes,
		sharedClient.Metrics,
		sharedClient.RawConfig.CurrentContext,
		sharedClient.RESTConfig.Host,
		cfg.Cluster.Timeout,
	)

	aiopsRepo := aiops.NewRepository(db)
	aiopsService := aiops.NewService(aiopsRepo)
	authService := auth.NewService(auth.NewRepository(db), sessionTTL, time.Now)
	if err := bootstrapAdmin(cfg, authService); err != nil {
		return err
	}

	llmClient, modelConfigured, err := buildLLMClient(cfg)
	if err != nil {
		return fmt.Errorf("initialize llm client: %w", err)
	}
	events := aiops.NewEventStore(db)
	runRepo := aiops.NewRunRepository(db)
	workflow := aiops.NewWorkflow(aiops.WorkflowOptions{
		DB:        db,
		Incidents: aiopsRepo,
		Runs:      runRepo,
		Evidence:  evidence.NewRepository(db),
		Collector: evidence.NewKubernetesCollector(
			evidence.KubernetesPodAPI{Kube: sharedClient.Kubernetes, Metrics: sharedClient.Metrics},
			cluster.Capabilities{Nodes: true, Events: true, PodLogs: true, Metrics: true},
			cfg.Cluster.Timeout,
		),
		LLM:             llmClient,
		ModelConfigured: modelConfigured,
		Model:           cfg.LLM.Model,
		Events:          events,
	})

	auditRepo := audit.NewRepository(db)
	snapshotStore := remediation.NewSnapshotStore(db)
	executor := remediation.NewExecutor(
		&remediation.KubeExecutorClient{Client: sharedClient.Kubernetes, RolloutTimeout: cfg.Cluster.Timeout},
		remediation.SnapshotterFunc(func(ctx context.Context, incidentID, ns, kind, name string) (remediation.Snapshot, error) {
			return remediation.SnapshotResource(ctx, sharedClient.Kubernetes, incidentID, ns, kind, name)
		}),
		snapshotStore,
		auditRepo,
	)
	remediationService := remediation.NewService(
		aiopsService,
		runRepo,
		sharedClient.Kubernetes,
		executor,
		auditRepo,
		snapshotStore,
	)

	updateService := service.NewUpdateService(info, cfg.Update, web.HasEmbeddedFrontend())
	systemLockService := service.NewSystemOperationLockService()
	router := newRouter(
		sharedClient,
		clusterService,
		probe,
		authService,
		updateService,
		systemLockService,
		aiopsService,
		workflow,
		events,
		remediationService,
		experiment.NewRunRepository(db),
		info,
	)
	return router.Run(cfg.HTTPAddr)
}

// buildLLMClient constructs the model client from config. An empty BaseURL
// disables model-backed roles (deterministic triage/collector still run);
// a configured but invalid endpoint fails startup so misconfiguration surfaces
// immediately.
func buildLLMClient(cfg config.Config) (aiops.LLMClient, bool, error) {
	if strings.TrimSpace(cfg.LLM.BaseURL) == "" {
		return nil, false, nil
	}
	style := llm.StyleChatCompletions
	if cfg.LLM.Style != "" {
		style = llm.APIStyle(cfg.LLM.Style)
	}
	client, err := llm.New(llm.Options{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
		Style:   style,
		Timeout: cfg.LLM.Timeout,
	})
	if err != nil {
		return nil, false, err
	}
	return client, true, nil
}

// setupAIOps opens the SQLite database at dbPath and returns a ready-to-use
// aiops Service backed by it. The caller is responsible for closing db.
func setupAIOps(dbPath string) (*sql.DB, *aiops.Service, error) {
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	svc := aiops.NewService(aiops.NewRepository(db))
	return db, svc, nil
}

// bootstrapAdmin creates the first admin account from environment variables
// when the users table is empty. Missing credentials on an empty database fail
// startup; once a user exists the environment variables are ignored.
func bootstrapAdmin(cfg config.Config, authService *auth.Service) error {
	username := os.Getenv("KUBEJOJO_BOOTSTRAP_ADMIN_USER")
	password := os.Getenv("KUBEJOJO_BOOTSTRAP_ADMIN_PASSWORD")
	if err := authService.BootstrapAdmin(context.Background(), username, password); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	return nil
}
