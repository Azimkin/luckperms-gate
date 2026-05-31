package luckperms

import (
	"strings"
	"sync"
	"time"

	"go.minekube.com/gate/pkg/util/permission"
	"go.minekube.com/gate/pkg/util/uuid"
)

// Tristate mirrors LuckPerms' three-valued logic.
type Tristate uint8

const (
	Undefined Tristate = iota
	True
	False
)

// toGate converts to Gate's permission.TriState.
func (t Tristate) toGate() permission.TriState {
	switch t {
	case True:
		return permission.True
	case False:
		return permission.False
	default:
		return permission.Undefined
	}
}

func tristateOf(value bool) Tristate {
	if value {
		return True
	}
	return False
}

const globalContext = "global"

// node marker constants, matching LuckPerms.
const (
	inheritanceMarker = "group." // me.lucko...node.types.Inheritance NODE_MARKER
	regexMarker1      = "r="     // RegexPermission MARKER_1
	regexMarker2      = "R="     // RegexPermission MARKER_2
	wildcardSuffix    = ".*"     // WildcardProcessor
	rootWildcard      = "*"
	rootWildcardQuote = "'*'"
)

// Node is a single permission/inheritance entry, as stored in the database.
//
// In the LuckPerms SQL schema, server and world are dedicated columns (defaulting
// to "global"), while the contexts column holds any additional contexts as JSON.
type Node struct {
	Key      string
	Value    bool
	Server   string
	World    string
	Contexts map[string][]string // extra contexts beyond server/world
	Expiry   int64               // unix seconds, 0 = permanent
}

// HasExpired reports whether the node is temporary and already expired.
func (n *Node) HasExpired(now time.Time) bool {
	return n.Expiry > 0 && n.Expiry <= now.Unix()
}

// InheritedGroup returns the lower-cased group name if this node grants group
// membership (key "group.<name>" with a true value), else ("", false).
func (n *Node) InheritedGroup() (string, bool) {
	if !n.Value {
		return "", false
	}
	key := strings.ToLower(n.Key)
	if !strings.HasPrefix(key, inheritanceMarker) {
		return "", false
	}
	return key[len(inheritanceMarker):], true
}

// Weight returns the weight encoded by a "weight.<n>" node, if any.
func (n *Node) Weight() (int, bool) {
	const marker = "weight."
	if !strings.HasPrefix(n.Key, marker) {
		return 0, false
	}
	return atoiSafe(n.Key[len(marker):])
}

func atoiSafe(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// PermissionHolder is the common interface for User and Group.
type PermissionHolder interface {
	getNodes() []Node
}

// Group is a loaded permission group.
type Group struct {
	Name  string
	Nodes []Node
}

func (g *Group) getNodes() []Node { return g.Nodes }

// weight resolves the group's weight from its nodes (default 0).
func (g *Group) weight() int {
	w := 0
	for i := range g.Nodes {
		if v, ok := g.Nodes[i].Weight(); ok && v > w {
			w = v
		}
	}
	return w
}

// User is a loaded player.
type User struct {
	UUID         uuid.UUID
	Username     string
	PrimaryGroup string
	Nodes        []Node

	cacheMu sync.Mutex
	cache   map[string]*resolvedData // keyed by query server context
}

func (u *User) getNodes() []Node { return u.Nodes }
