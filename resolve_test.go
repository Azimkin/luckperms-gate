package luckperms

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
	"go.minekube.com/gate/pkg/util/uuid"
)

func testManager(cfg *Config, groups map[string]*Group) *Manager {
	if groups == nil {
		groups = map[string]*Group{}
	}
	return &Manager{
		cfg:    cfg,
		log:    logr.Discard(),
		groups: groups,
		users:  map[uuid.UUID]*User{},
	}
}

func TestOpPermissionFallback(t *testing.T) {
	cfg := testConfig() // op-permission = gate.operator (from defaults)
	op := &User{Nodes: []Node{perm("gate.operator", true)}}
	plain := &User{Nodes: []Node{perm("some.perm", true)}}
	m := testManager(cfg, nil)

	if got := m.Check(op, "anything.undefined"); got != True {
		t.Errorf("operator undefined check = %v, want True", got)
	}
	if got := m.Check(plain, "anything.undefined"); got != Undefined {
		t.Errorf("non-operator undefined check = %v, want Undefined", got)
	}
}

func TestOpPermissionRespectsExplicitDeny(t *testing.T) {
	cfg := testConfig()
	op := &User{Nodes: []Node{perm("gate.operator", true), perm("banned.cmd", false)}}
	m := testManager(cfg, nil)

	if got := m.Check(op, "banned.cmd"); got != False {
		t.Errorf("explicit deny for operator = %v, want False", got)
	}
}

func TestOpPermissionDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.OpPermission = ""
	op := &User{Nodes: []Node{perm("gate.operator", true)}}
	m := testManager(cfg, nil)

	if got := m.Check(op, "anything.undefined"); got != Undefined {
		t.Errorf("op disabled = %v, want Undefined", got)
	}
}

func testConfig() *Config {
	c := defaultConfig()
	c.Server = "proxy"
	return c
}

func perm(key string, value bool) Node {
	return Node{Key: key, Value: value, Server: globalContext, World: globalContext}
}

func resolveUser(u *User, groups map[string]*Group, cfg *Config) *resolvedData {
	q := queryContext{server: cfg.Server, world: globalContext}
	resolver := func(name string) *Group { return groups[name] }
	return resolve(u, q, cfg, resolver, time.Unix(1000, 0))
}

func TestDirectAllowAndDeny(t *testing.T) {
	cfg := testConfig()
	u := &User{Nodes: []Node{perm("a.b", true), perm("a.c", false)}}
	rd := resolveUser(u, nil, cfg)

	if got := rd.check("a.b", cfg); got != True {
		t.Errorf("a.b = %v, want True", got)
	}
	if got := rd.check("a.c", cfg); got != False {
		t.Errorf("a.c = %v, want False", got)
	}
	if got := rd.check("a.d", cfg); got != Undefined {
		t.Errorf("a.d = %v, want Undefined", got)
	}
}

func TestInheritance(t *testing.T) {
	cfg := testConfig()
	groups := map[string]*Group{
		"admin": {Name: "admin", Nodes: []Node{perm("server.admin", true)}},
	}
	u := &User{Nodes: []Node{perm("group.admin", true)}}
	rd := resolveUser(u, groups, cfg)

	if got := rd.check("server.admin", cfg); got != True {
		t.Errorf("inherited server.admin = %v, want True", got)
	}
	if got := rd.check("group.admin", cfg); got != True {
		t.Errorf("group.admin membership = %v, want True", got)
	}
}

func TestUserOverridesGroup(t *testing.T) {
	cfg := testConfig()
	groups := map[string]*Group{
		"default": {Name: "default", Nodes: []Node{perm("fly", true)}},
	}
	// User explicitly denies fly even though the group grants it.
	u := &User{Nodes: []Node{perm("group.default", true), perm("fly", false)}}
	rd := resolveUser(u, groups, cfg)

	if got := rd.check("fly", cfg); got != False {
		t.Errorf("fly = %v, want False (user overrides group)", got)
	}
}

func TestWeightedGroupPriority(t *testing.T) {
	cfg := testConfig()
	groups := map[string]*Group{
		"low":  {Name: "low", Nodes: []Node{perm("weight.10", true), perm("rank", false)}},
		"high": {Name: "high", Nodes: []Node{perm("weight.100", true), perm("rank", true)}},
	}
	u := &User{Nodes: []Node{perm("group.low", true), perm("group.high", true)}}
	rd := resolveUser(u, groups, cfg)

	if got := rd.check("rank", cfg); got != True {
		t.Errorf("rank = %v, want True (higher weight group wins)", got)
	}
}

func TestExpiry(t *testing.T) {
	cfg := testConfig()
	n := perm("temp", true)
	n.Expiry = 500 // already expired at now=1000
	u := &User{Nodes: []Node{n}}
	rd := resolveUser(u, nil, cfg)

	if got := rd.check("temp", cfg); got != Undefined {
		t.Errorf("expired temp = %v, want Undefined", got)
	}
}

func TestWildcard(t *testing.T) {
	cfg := testConfig()
	u := &User{Nodes: []Node{perm("essentials.*", true)}}
	rd := resolveUser(u, nil, cfg)

	if got := rd.check("essentials.home", cfg); got != True {
		t.Errorf("essentials.home via wildcard = %v, want True", got)
	}
	if got := rd.check("other.thing", cfg); got != Undefined {
		t.Errorf("other.thing = %v, want Undefined", got)
	}
}

func TestRootWildcard(t *testing.T) {
	cfg := testConfig()
	u := &User{Nodes: []Node{perm("*", true)}}
	rd := resolveUser(u, nil, cfg)

	if got := rd.check("anything.here", cfg); got != True {
		t.Errorf("root wildcard = %v, want True", got)
	}
}

func TestRegex(t *testing.T) {
	cfg := testConfig()
	u := &User{Nodes: []Node{perm("r=essentials\\..*", true)}}
	rd := resolveUser(u, nil, cfg)

	if got := rd.check("essentials.home", cfg); got != True {
		t.Errorf("regex match = %v, want True", got)
	}
	if got := rd.check("other", cfg); got != Undefined {
		t.Errorf("regex non-match = %v, want Undefined", got)
	}
}

func TestServerContext(t *testing.T) {
	cfg := testConfig() // server = "proxy"
	u := &User{Nodes: []Node{
		{Key: "lobby.perm", Value: true, Server: "lobby", World: globalContext},
		{Key: "proxy.perm", Value: true, Server: "proxy", World: globalContext},
	}}
	rd := resolveUser(u, nil, cfg)

	if got := rd.check("lobby.perm", cfg); got != Undefined {
		t.Errorf("lobby-scoped perm = %v, want Undefined on proxy server", got)
	}
	if got := rd.check("proxy.perm", cfg); got != True {
		t.Errorf("proxy-scoped perm = %v, want True", got)
	}
}

func TestIncludeGlobalDisabled(t *testing.T) {
	cfg := testConfig()
	cfg.IncludeGlobal = false
	u := &User{Nodes: []Node{perm("global.perm", true)}} // server=global
	rd := resolveUser(u, nil, cfg)

	if got := rd.check("global.perm", cfg); got != Undefined {
		t.Errorf("global perm with include-global=false = %v, want Undefined", got)
	}
}

func TestDirectBeatsWildcardDeny(t *testing.T) {
	cfg := testConfig()
	// wildcard grants, but a direct deny on the exact node must win.
	u := &User{Nodes: []Node{perm("essentials.*", true), perm("essentials.tp", false)}}
	rd := resolveUser(u, nil, cfg)

	if got := rd.check("essentials.tp", cfg); got != False {
		t.Errorf("essentials.tp = %v, want False (direct beats wildcard)", got)
	}
	if got := rd.check("essentials.home", cfg); got != True {
		t.Errorf("essentials.home = %v, want True (wildcard)", got)
	}
}
