package luckperms

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// queryContext is the resolved context a permission check runs in. On a proxy
// the world is unused (Velocity has no world context), so it stays "global".
type queryContext struct {
	server   string
	world    string
	contexts map[string][]string
}

// groupResolver loads a group by (lower-cased) name, or nil if absent.
type groupResolver func(name string) *Group

// nodeApplies reports whether a node is active in the given query context.
// isInheritance selects the apply-global-groups flags instead of include-global.
func (c *Config) nodeApplies(n *Node, q queryContext, isInheritance bool) bool {
	includeGlobalServer := c.IncludeGlobal
	includeGlobalWorld := c.IncludeGlobalWorld
	if isInheritance {
		includeGlobalServer = c.ApplyGlobalGroups
		includeGlobalWorld = c.ApplyGlobalWorldGroups
	}

	// server
	if strings.EqualFold(n.Server, globalContext) {
		if !includeGlobalServer {
			return false
		}
	} else if !strings.EqualFold(n.Server, q.server) {
		return false
	}

	// world
	if strings.EqualFold(n.World, globalContext) {
		if !includeGlobalWorld {
			return false
		}
	} else if !strings.EqualFold(n.World, q.world) {
		return false
	}

	// additional contexts
	for key, values := range n.Contexts {
		qv := q.contexts[key]
		if !contextSatisfied(values, qv, c.requireAllContextValues()) {
			return false
		}
	}
	return true
}

// contextSatisfied implements LuckPerms' context-satisfy-mode against a query.
func contextSatisfied(required, query []string, requireAll bool) bool {
	if len(required) == 0 {
		return true
	}
	has := func(v string) bool {
		for _, q := range query {
			if strings.EqualFold(q, v) {
				return true
			}
		}
		return false
	}
	if requireAll {
		for _, v := range required {
			if !has(v) {
				return false
			}
		}
		return true
	}
	for _, v := range required {
		if has(v) {
			return true
		}
	}
	return false
}

// resolvedData is the precomputed permission state for a holder in one context.
type resolvedData struct {
	direct    map[string]Tristate // exact-match keys (incl. group.x)
	regex     []regexEntry
	wildcards map[string]Tristate // prefix (without trailing ".*") -> state
	root      Tristate            // state of the "*" / "'*'" root wildcard
}

type regexEntry struct {
	pattern *regexp.Regexp
	state   Tristate
}

// resolve walks the inheritance graph (depth-first, pre-order, weighted) and
// builds the resolved permission data for the holder in the given context.
func resolve(holder PermissionHolder, q queryContext, cfg *Config, groups groupResolver, now time.Time) *resolvedData {
	accumulated := map[string]Tristate{}
	visited := map[string]bool{}

	var walk func(h PermissionHolder)
	walk = func(h PermissionHolder) {
		nodes := h.getNodes()

		// 1. accumulate this holder's directly-applicable nodes (first-wins).
		for i := range nodes {
			n := &nodes[i]
			if n.HasExpired(now) || !cfg.nodeApplies(n, q, false) {
				continue
			}
			if _, ok := accumulated[n.Key]; !ok {
				accumulated[n.Key] = tristateOf(n.Value)
			}
		}

		// 2. collect applicable parent groups, ordered by weight desc then name.
		type parent struct {
			group  *Group
			weight int
		}
		var parents []parent
		for i := range nodes {
			n := &nodes[i]
			if n.HasExpired(now) || !cfg.nodeApplies(n, q, true) {
				continue
			}
			name, ok := n.InheritedGroup()
			if !ok || visited[name] {
				continue
			}
			g := groups(name)
			if g == nil {
				continue
			}
			parents = append(parents, parent{group: g, weight: g.weight()})
		}
		sort.SliceStable(parents, func(a, b int) bool {
			if parents[a].weight != parents[b].weight {
				return parents[a].weight > parents[b].weight
			}
			return parents[a].group.Name < parents[b].group.Name
		})

		for _, p := range parents {
			if visited[p.group.Name] {
				continue
			}
			visited[p.group.Name] = true
			walk(p.group)
		}
	}
	walk(holder)

	return buildResolvedData(accumulated, cfg)
}

func buildResolvedData(accumulated map[string]Tristate, cfg *Config) *resolvedData {
	rd := &resolvedData{
		direct:    accumulated,
		wildcards: map[string]Tristate{},
		root:      Undefined,
	}
	for key, state := range accumulated {
		// wildcard prefixes
		if cfg.ApplyWildcards {
			if key == rootWildcard || key == rootWildcardQuote {
				rd.root = state
			} else if strings.HasSuffix(key, wildcardSuffix) && len(key) > 2 {
				rd.wildcards[key[:len(key)-len(wildcardSuffix)]] = state
			}
		}
		// regex entries
		if cfg.ApplyRegex {
			if pat, ok := parseRegexKey(key); ok {
				if re, err := regexp.Compile(pat); err == nil {
					rd.regex = append(rd.regex, regexEntry{pattern: re, state: state})
				}
			}
		}
	}
	return rd
}

func parseRegexKey(key string) (string, bool) {
	switch {
	case strings.HasPrefix(key, regexMarker1):
		return key[len(regexMarker1):], true
	case strings.HasPrefix(key, regexMarker2):
		return key[len(regexMarker2):], true
	default:
		return "", false
	}
}

// check resolves the tri-state for a permission, applying the same processor
// order as VelocityCalculatorFactory: Direct, Regex, Wildcard, (Sponge).
func (rd *resolvedData) check(permission string, cfg *Config) Tristate {
	// Direct
	if state, ok := rd.direct[permission]; ok && state != Undefined {
		return state
	}
	// Regex
	if cfg.ApplyRegex {
		for _, e := range rd.regex {
			if e.pattern.MatchString(permission) {
				if e.state != Undefined {
					return e.state
				}
			}
		}
	}
	// Wildcard (a.b.c -> a.b.* -> a.* -> *)
	if cfg.ApplyWildcards {
		node := permission
		for {
			idx := strings.LastIndex(node, ".")
			if idx == -1 {
				break
			}
			node = node[:idx]
			if node == "" {
				continue
			}
			if state, ok := rd.wildcards[node]; ok && state != Undefined {
				return state
			}
		}
		if rd.root != Undefined {
			return rd.root
		}
	}
	// Sponge implicit wildcards: "a.b.c" implies checking "a.b.c.*" etc.
	if cfg.ApplyWildcardsSponge {
		if state, ok := rd.direct[permission+wildcardSuffix]; ok && state != Undefined {
			return state
		}
		node := permission
		for {
			if state, ok := rd.direct[node+wildcardSuffix]; ok && state != Undefined {
				return state
			}
			idx := strings.LastIndex(node, ".")
			if idx == -1 {
				break
			}
			node = node[:idx]
		}
	}
	return Undefined
}
