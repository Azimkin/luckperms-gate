// Package luckperms is a read-only port of the LuckPerms Velocity module for the
// Gate proxy. It loads permission data from a shared PostgreSQL database, resolves
// permissions (inheritance, contexts, expiry, wildcard/regex processors) the same
// way the Velocity module does, and stays in sync with the rest of a LuckPerms
// network via Postgres LISTEN/NOTIFY.
package luckperms

import (
	"context"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	"github.com/robinbraemer/event"
	"go.minekube.com/gate/pkg/edition/java/proxy"
)

// configPathEnv overrides the default config location.
const configPathEnv = "LUCKPERMS_CONFIG"

// defaultConfigPath is relative to the proxy's working directory.
const defaultConfigPath = "luckperms/config.yml"

// Plugin is the Gate plugin registration handle.
var Plugin = proxy.Plugin{
	Name: "LuckPerms",
	Init: func(ctx context.Context, p *proxy.Proxy) error {
		log := logr.FromContextOrDiscard(ctx).WithName("luckperms")

		path := os.Getenv(configPathEnv)
		if path == "" {
			path = defaultConfigPath
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			if werr := writeDefaultConfig(path); werr != nil {
				log.Error(werr, "could not write default config", "path", path)
			} else {
				log.Info("wrote default config, please edit it with your database credentials", "path", path)
			}
		}

		cfg, err := LoadConfig(path)
		if err != nil {
			return err
		}

		storage, err := newStorage(ctx, cfg)
		if err != nil {
			return err
		}
		log.Info("storage connected", "storage-method", cfg.StorageMethod, "server-context", cfg.Server)

		manager := NewManager(storage, cfg, log)
		if err := manager.LoadGroups(ctx); err != nil {
			storage.Close()
			return err
		}

		pl := &plugin{proxy: p, cfg: cfg, storage: storage, manager: manager, log: log}

		event.Subscribe(p.Event(), 0, pl.onPermissionsSetup)
		event.Subscribe(p.Event(), 0, pl.onDisconnect)

		messenger, err := newMessenger(ctx, cfg, storage, manager, log)
		if err != nil {
			storage.Close()
			return err
		}
		if messenger != nil {
			go messenger.Run(ctx)
		} else {
			log.Info("messaging disabled; cross-network sync off", "messaging-service", cfg.MessagingService)
		}

		// Release the pool when the proxy shuts down.
		go func() {
			<-ctx.Done()
			storage.Close()
		}()

		log.Info("LuckPerms plugin initialized")
		return nil
	},
}

type plugin struct {
	proxy   *proxy.Proxy
	cfg     *Config
	storage StorageProvider
	manager *Manager
	log     logr.Logger
}

func writeDefaultConfig(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(defaultConfigYAML), 0o644)
}
