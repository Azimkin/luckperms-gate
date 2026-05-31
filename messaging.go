package luckperms

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"go.minekube.com/gate/pkg/util/uuid"
)

// updateChannel is the LuckPerms Postgres LISTEN/NOTIFY channel.
const updateChannel = "luckperms:update"

// message types we understand (matching LuckPerms message impls).
const (
	typeUpdate     = "update"
	typeUserUpdate = "userupdate"
)

// wireMessage is the JSON envelope produced by LuckPermsMessagingService.
type wireMessage struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
}

// register the built-in PostgreSQL LISTEN/NOTIFY messenger.
func init() {
	RegisterMessenger(func(ctx context.Context, cfg *Config, store StorageProvider, manager *Manager, log logr.Logger) (Messenger, error) {
		dsner, ok := store.(interface{ DSN() string })
		if !ok {
			return nil, fmt.Errorf("postgres messenger requires a storage backend exposing DSN(); got %T", store)
		}
		return NewPostgresMessenger(dsner.DSN(), manager, log), nil
	}, "postgresql", "postgres")
}

// PostgresMessenger consumes LuckPerms update notifications over Postgres
// LISTEN/NOTIFY and refreshes the Manager's caches. It is read-only (never publishes).
type PostgresMessenger struct {
	dsn     string
	manager *Manager
	log     logr.Logger

	seenMu sync.Mutex
	seen   map[string]struct{} // de-dup of message ids
}

func NewPostgresMessenger(dsn string, manager *Manager, log logr.Logger) *PostgresMessenger {
	return &PostgresMessenger{
		dsn:     dsn,
		manager: manager,
		log:     log,
		seen:    map[string]struct{}{},
	}
}

// Run blocks listening for notifications until ctx is cancelled, reconnecting
// on failure (mirrors PostgresMessenger's connection-keepalive behaviour).
func (m *PostgresMessenger) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := m.listen(ctx); err != nil && ctx.Err() == nil {
			m.log.Error(err, "postgres listen/notify connection dropped, reconnecting in 5s")
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (m *PostgresMessenger) listen(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, m.dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, `LISTEN "`+updateChannel+`"`); err != nil {
		return err
	}
	m.log.Info("listening for LuckPerms updates", "channel", updateChannel)

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if notification.Channel != updateChannel {
			continue
		}
		m.handle(ctx, notification.Payload)
	}
}

func (m *PostgresMessenger) handle(ctx context.Context, payload string) {
	var msg wireMessage
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		m.log.Error(err, "unable to decode incoming messaging service message", "payload", payload)
		return
	}
	if msg.ID == "" || !m.markSeen(msg.ID) {
		return // missing id or already handled
	}

	switch msg.Type {
	case typeUpdate:
		m.log.Info("received full update message, reloading all data")
		m.manager.InvalidateAll(ctx)
	case typeUserUpdate:
		var content struct {
			UserUUID string `json:"userUuid"`
		}
		if err := json.Unmarshal(msg.Content, &content); err != nil {
			m.log.Error(err, "invalid userupdate content", "payload", payload)
			return
		}
		id, err := uuid.Parse(content.UserUUID)
		if err != nil {
			m.log.Error(err, "invalid user uuid in update message", "value", content.UserUUID)
			return
		}
		m.manager.InvalidateUser(ctx, id)
	default:
		// gracefully ignore types we don't handle (actionlog, custom, future types)
	}
}

// markSeen records a message id, returning false if it was already seen.
func (m *PostgresMessenger) markSeen(id string) bool {
	m.seenMu.Lock()
	defer m.seenMu.Unlock()
	if _, ok := m.seen[id]; ok {
		return false
	}
	// bound memory: LuckPerms uses a time-expiring cache; a simple cap suffices
	// for a read-only consumer that only de-dups bursts of duplicate notifies.
	if len(m.seen) > 10000 {
		m.seen = map[string]struct{}{}
	}
	m.seen[id] = struct{}{}
	return true
}
