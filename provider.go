package luckperms

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"go.minekube.com/gate/pkg/util/uuid"
)

// StorageProvider is the read-only data source the permission engine resolves
// against. Implement this and register it with RegisterStorage to add support
// for a new backend (MySQL, MongoDB, a REST API, ...).
type StorageProvider interface {
	// LoadUser loads a player's row and permission nodes. It must return a
	// non-nil User even for an unknown player (with default primary group and
	// no nodes), and must not return an error in that case.
	LoadUser(ctx context.Context, id uuid.UUID, username string) (*User, error)
	// LoadGroup loads a single group's nodes by (lower-cased) name.
	LoadGroup(ctx context.Context, name string) (*Group, error)
	// ListGroups returns every registered group name.
	ListGroups(ctx context.Context) ([]string, error)
	// Close releases any held resources when the proxy shuts down.
	Close()
}

// Messenger keeps caches in sync with the rest of the network. Run blocks until
// ctx is cancelled and should reconnect on transient failures. A messenger
// signals cache changes by calling Manager.InvalidateUser / InvalidateAll.
type Messenger interface {
	Run(ctx context.Context)
}

// StorageFactory builds a StorageProvider from config.
type StorageFactory func(ctx context.Context, cfg *Config) (StorageProvider, error)

// MessengerFactory builds a Messenger. It receives the live StorageProvider so a
// messenger that shares the storage connection (e.g. Postgres LISTEN/NOTIFY) can
// reuse it via a type assertion.
type MessengerFactory func(ctx context.Context, cfg *Config, store StorageProvider, manager *Manager, log logr.Logger) (Messenger, error)

var (
	storageFactories   = map[string]StorageFactory{}
	messengerFactories = map[string]MessengerFactory{}
)

// RegisterStorage registers a storage backend under one or more names (matched
// against the config "storage-method", case-insensitively). Call from an init().
func RegisterStorage(factory StorageFactory, names ...string) {
	for _, n := range names {
		storageFactories[normalizeName(n)] = factory
	}
}

// RegisterMessenger registers a messaging backend under one or more names
// (matched against the config "messaging-service"). Call from an init().
func RegisterMessenger(factory MessengerFactory, names ...string) {
	for _, n := range names {
		messengerFactories[normalizeName(n)] = factory
	}
}

func normalizeName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// newStorage selects and builds the configured storage backend.
func newStorage(ctx context.Context, cfg *Config) (StorageProvider, error) {
	name := normalizeName(cfg.StorageMethod)
	factory, ok := storageFactories[name]
	if !ok {
		return nil, fmt.Errorf("unknown storage-method %q (registered: %s)", cfg.StorageMethod, registeredNames(storageFactories))
	}
	return factory(ctx, cfg)
}

// newMessenger selects and builds the configured messaging backend. It returns
// (nil, nil) when messaging is disabled.
func newMessenger(ctx context.Context, cfg *Config, store StorageProvider, manager *Manager, log logr.Logger) (Messenger, error) {
	name := resolveMessengerName(cfg)
	if name == "" {
		return nil, nil
	}
	factory, ok := messengerFactories[name]
	if !ok {
		log.Info("no messenger registered for messaging-service; cross-network sync disabled",
			"messaging-service", cfg.MessagingService, "registered", registeredNames(messengerFactories))
		return nil, nil
	}
	return factory(ctx, cfg, store, manager, log)
}

// resolveMessengerName maps the config value to a registered name. "auto"
// (or empty) follows the storage method; "none" disables messaging.
func resolveMessengerName(cfg *Config) string {
	switch name := normalizeName(cfg.MessagingService); name {
	case "", "auto":
		return normalizeName(cfg.StorageMethod)
	case "none":
		return ""
	default:
		return name
	}
}

func registeredNames[T any](m map[string]T) string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}
