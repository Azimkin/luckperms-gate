package luckperms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.minekube.com/gate/pkg/util/uuid"
)

// register the built-in PostgreSQL storage backend.
func init() {
	RegisterStorage(func(ctx context.Context, cfg *Config) (StorageProvider, error) {
		return NewStorage(ctx, cfg)
	}, "postgresql", "postgres")
}

// Storage is a read-only PostgreSQL accessor for LuckPerms data.
type Storage struct {
	pool   *pgxpool.Pool
	prefix string
	dsn    string
}

// NewStorage opens a connection pool to the configured PostgreSQL database.
func NewStorage(ctx context.Context, cfg *Config) (*Storage, error) {
	host, port := cfg.host()
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
		url.QueryEscape(cfg.Data.Username),
		url.QueryEscape(cfg.Data.Password),
		host, port,
		url.PathEscape(cfg.Data.Database),
	)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	if n := cfg.Data.PoolSettings.MaximumPoolSize; n > 0 {
		poolCfg.MaxConns = int32(n)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &Storage{pool: pool, prefix: cfg.Data.TablePrefix, dsn: dsn}, nil
}

// Close releases the pool.
func (s *Storage) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

// DSN exposes the connection string (used by the messenger's LISTEN connection).
func (s *Storage) DSN() string { return s.dsn }

func (s *Storage) table(name string) string { return s.prefix + name }

// LoadUser loads a user's row and permission nodes. Returns a User even if the
// player row is absent (a never-seen player simply has no nodes/primary group).
func (s *Storage) LoadUser(ctx context.Context, id uuid.UUID, username string) (*User, error) {
	u := &User{UUID: id, Username: username, PrimaryGroup: "default"}

	row := s.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT username, primary_group FROM %s WHERE uuid=$1", s.table("players")),
		id.String(),
	)
	var dbName, primary string
	switch err := row.Scan(&dbName, &primary); err {
	case nil:
		if dbName != "" {
			u.Username = dbName
		}
		if primary != "" {
			u.PrimaryGroup = primary
		}
	case pgx.ErrNoRows:
		// new player, keep defaults
	default:
		return nil, fmt.Errorf("load player %s: %w", id, err)
	}

	nodes, err := s.queryNodes(ctx,
		fmt.Sprintf("SELECT permission, value, server, world, expiry, contexts FROM %s WHERE uuid=$1", s.table("user_permissions")),
		id.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("load user permissions %s: %w", id, err)
	}
	u.Nodes = nodes
	return u, nil
}

// LoadGroup loads a single group's nodes. Returns nil if the group has no rows
// and is not registered.
func (s *Storage) LoadGroup(ctx context.Context, name string) (*Group, error) {
	nodes, err := s.queryNodes(ctx,
		fmt.Sprintf("SELECT permission, value, server, world, expiry, contexts FROM %s WHERE name=$1", s.table("group_permissions")),
		name,
	)
	if err != nil {
		return nil, fmt.Errorf("load group permissions %s: %w", name, err)
	}
	return &Group{Name: name, Nodes: nodes}, nil
}

// ListGroups returns all registered group names.
func (s *Storage) ListGroups(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf("SELECT name FROM %s", s.table("groups")))
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (s *Storage) queryNodes(ctx context.Context, sql string, arg any) ([]Node, error) {
	rows, err := s.pool.Query(ctx, sql, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var (
			n        Node
			contexts string
		)
		if err := rows.Scan(&n.Key, &n.Value, &n.Server, &n.World, &n.Expiry, &contexts); err != nil {
			return nil, err
		}
		if n.Server == "" {
			n.Server = globalContext
		}
		if n.World == "" {
			n.World = globalContext
		}
		n.Contexts = parseContexts(contexts)
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// parseContexts decodes the LuckPerms contexts JSON column. Each value is either
// a string or an array of strings (see ContextSetJsonSerializer).
func parseContexts(raw string) map[string][]string {
	if raw == "" || raw == "{}" {
		return nil
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return nil
	}
	out := make(map[string][]string, len(generic))
	for k, v := range generic {
		var single string
		if err := json.Unmarshal(v, &single); err == nil {
			out[k] = []string{single}
			continue
		}
		var multi []string
		if err := json.Unmarshal(v, &multi); err == nil {
			out[k] = multi
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
