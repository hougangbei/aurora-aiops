package server

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/auth"
	"github.com/heihuzicity-tech/kubejojo/server/internal/buildinfo"
	"github.com/heihuzicity-tech/kubejojo/server/internal/cluster"
	"github.com/heihuzicity-tech/kubejojo/server/internal/config"
	"github.com/heihuzicity-tech/kubejojo/server/internal/kube"
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

	aiopsService := aiops.NewService(aiops.NewRepository(db))
	authService := auth.NewService(auth.NewRepository(db), sessionTTL, time.Now)
	if err := bootstrapAdmin(cfg, authService); err != nil {
		return err
	}

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
		info,
	)
	return router.Run(cfg.HTTPAddr)
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
