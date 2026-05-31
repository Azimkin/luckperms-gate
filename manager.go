package luckperms

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"go.minekube.com/gate/pkg/util/uuid"
)

// Manager owns the in-memory caches of groups and online users, and performs
// permission resolution. It is safe for concurrent use.
type Manager struct {
	storage StorageProvider
	cfg     *Config
	log     logr.Logger

	mu     sync.RWMutex
	groups map[string]*Group // keyed by lower-cased name
	users  map[uuid.UUID]*User
}

func NewManager(storage StorageProvider, cfg *Config, log logr.Logger) *Manager {
	return &Manager{
		storage: storage,
		cfg:     cfg,
		log:     log,
		groups:  map[string]*Group{},
		users:   map[uuid.UUID]*User{},
	}
}

// LoadGroups (re)loads every registered group from the database.
func (m *Manager) LoadGroups(ctx context.Context) error {
	names, err := m.storage.ListGroups(ctx)
	if err != nil {
		return err
	}
	loaded := make(map[string]*Group, len(names))
	for _, name := range names {
		g, err := m.storage.LoadGroup(ctx, name)
		if err != nil {
			return err
		}
		g.Name = strings.ToLower(name)
		loaded[g.Name] = g
	}

	m.mu.Lock()
	m.groups = loaded
	// invalidate resolution caches of all online users, group data changed.
	for _, u := range m.users {
		u.invalidate()
	}
	m.mu.Unlock()
	return nil
}

// group resolves a group by name for the resolver (read-locked).
func (m *Manager) group(name string) *Group {
	m.mu.RLock()
	g := m.groups[strings.ToLower(name)]
	m.mu.RUnlock()
	return g
}

// LoadUser loads (or reloads) a user from the database and caches it.
func (m *Manager) LoadUser(ctx context.Context, id uuid.UUID, username string) (*User, error) {
	u, err := m.storage.LoadUser(ctx, id, username)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.users[id] = u
	m.mu.Unlock()
	return u, nil
}

// Unload drops a user from the cache (on disconnect).
func (m *Manager) Unload(id uuid.UUID) {
	m.mu.Lock()
	delete(m.users, id)
	m.mu.Unlock()
}

// getUser returns the cached user, if loaded.
func (m *Manager) getUser(id uuid.UUID) *User {
	m.mu.RLock()
	u := m.users[id]
	m.mu.RUnlock()
	return u
}

// InvalidateUser reloads a single user from the database if currently online.
func (m *Manager) InvalidateUser(ctx context.Context, id uuid.UUID) {
	if m.getUser(id) == nil {
		return // not online, nothing to refresh
	}
	if _, err := m.LoadUser(ctx, id, ""); err != nil {
		m.log.Error(err, "failed to reload user after update message", "uuid", id)
	}
}

// InvalidateAll reloads all groups and online users from the database.
func (m *Manager) InvalidateAll(ctx context.Context) {
	if err := m.LoadGroups(ctx); err != nil {
		m.log.Error(err, "failed to reload groups after update message")
	}

	m.mu.RLock()
	ids := make([]uuid.UUID, 0, len(m.users))
	for id := range m.users {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		if _, err := m.LoadUser(ctx, id, ""); err != nil {
			m.log.Error(err, "failed to reload user after update message", "uuid", id)
		}
	}
}

// Check resolves the permission tri-state for a user in the configured context.
func (m *Manager) Check(u *User, permission string) Tristate {
	q := queryContext{
		server:   m.cfg.Server,
		world:    globalContext,
		contexts: nil,
	}
	rd := u.resolved(q.server, func() *resolvedData {
		return resolve(u, q, m.cfg, m.group, time.Now())
	})

	res := rd.check(permission, m.cfg)

	// Operator fallback: a player holding the configured op-permission resolves
	// otherwise-undefined checks to true. Explicit grants/denials are untouched.
	if res == Undefined && m.cfg.OpPermission != "" && permission != m.cfg.OpPermission {
		if rd.check(m.cfg.OpPermission, m.cfg) == True {
			return True
		}
	}
	return res
}

// resolved returns the cached resolvedData for a server context, computing it
// lazily via build on first use.
func (u *User) resolved(server string, build func() *resolvedData) *resolvedData {
	u.cacheMu.Lock()
	defer u.cacheMu.Unlock()
	if u.cache == nil {
		u.cache = map[string]*resolvedData{}
	}
	if rd, ok := u.cache[server]; ok {
		return rd
	}
	rd := build()
	u.cache[server] = rd
	return rd
}

func (u *User) invalidate() {
	u.cacheMu.Lock()
	u.cache = nil
	u.cacheMu.Unlock()
}
