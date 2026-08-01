package server

import (
	"database/sql"
	"fmt"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/buildinfo"
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

	clusterFactory, err := kube.NewFactory(cfg.KubeconfigPath)
	if err != nil {
		return fmt.Errorf("initialize kubernetes client factory: %w", err)
	}

	if info.IsRelease() && !web.HasEmbeddedFrontend() {
		return fmt.Errorf("release build requires embedded frontend assets under server/internal/web/dist/app")
	}

	db, aiopsService, err := setupAIOps(cfg.AIOps.DBPath)
	if err != nil {
		return fmt.Errorf("initialize aiops store: %w", err)
	}
	defer db.Close()

	updateService := service.NewUpdateService(info, cfg.Update, web.HasEmbeddedFrontend())
	systemLockService := service.NewSystemOperationLockService()
	router := newRouter(clusterFactory, updateService, systemLockService, aiopsService, info)
	return router.Run(cfg.HTTPAddr)
}

// setupAIOps opens the SQLite database at dbPath and returns a ready-to-use
// aiops Service. The caller is responsible for closing the returned db.
func setupAIOps(dbPath string) (*sql.DB, *aiops.Service, error) {
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	repo := aiops.NewRepository(db)
	svc := aiops.NewService(repo)
	return db, svc, nil
}
