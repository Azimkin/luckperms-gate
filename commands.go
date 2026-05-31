package luckperms

import (
	"context"
	"sync"

	"go.minekube.com/brigodier"
	"go.minekube.com/gate/pkg/command"
	"go.minekube.com/gate/pkg/edition/java/proto/packet"
	"go.minekube.com/gate/pkg/edition/java/proxy"
	"go.minekube.com/gate/pkg/util/uuid"
)

// commandRefresher resends the client's command list when a player's permissions
// change, so proxy commands they gained/lost appear/disappear in tab-complete.
//
// The client command tree is normally sent once by the backend (forwarded by
// Gate, which injects the proxy's own commands filtered by permission). Gate does
// not cache that tree, so we snapshot it from PlayerAvailableCommandsEvent and,
// on a permission update, rebuild it: keep the backend commands, drop all proxy
// command names, then re-add the proxy commands the player may currently use.
type commandRefresher struct {
	proxy *plugin

	mu    sync.Mutex
	trees map[uuid.UUID]*brigodier.RootCommandNode // last tree sent to each player
}

func newCommandRefresher(pl *plugin) *commandRefresher {
	return &commandRefresher{proxy: pl, trees: map[uuid.UUID]*brigodier.RootCommandNode{}}
}

// onAvailableCommands snapshots the command tree about to be sent to a player.
func (r *commandRefresher) onAvailableCommands(e *proxy.PlayerAvailableCommandsEvent) {
	r.mu.Lock()
	r.trees[e.Player().ID()] = e.RootNode()
	r.mu.Unlock()
}

// forget drops a disconnected player's cached tree.
func (r *commandRefresher) forget(id uuid.UUID) {
	r.mu.Lock()
	delete(r.trees, id)
	r.mu.Unlock()
}

// resend rebuilds and sends a fresh command list reflecting current permissions.
func (r *commandRefresher) resend(id uuid.UUID) {
	// Only the proxy-injected commands are permission-filtered by us; if the proxy
	// does not announce its commands there is nothing for us to refresh.
	if !r.proxy.proxy.Config().AnnounceProxyCommands {
		return
	}

	player := r.proxy.proxy.Player(id)
	if player == nil || !player.Active() {
		return
	}

	r.mu.Lock()
	cached := r.trees[id]
	r.mu.Unlock()
	if cached == nil {
		return // never received a command tree for this player yet
	}

	root := &r.proxy.proxy.Command().Root

	// names of all proxy commands (regardless of permission) so we can strip the
	// stale set before re-adding the currently-usable ones.
	proxyNames := map[string]struct{}{}
	root.ChildrenOrdered().Range(func(name string, _ brigodier.CommandNode) bool {
		proxyNames[name] = struct{}{}
		return true
	})

	newRoot := &brigodier.RootCommandNode{}

	// keep backend commands from the cached tree (everything not owned by proxy).
	cached.ChildrenOrdered().Range(func(name string, node brigodier.CommandNode) bool {
		if _, isProxy := proxyNames[name]; !isProxy {
			newRoot.AddChild(node)
		}
		return true
	})

	// re-add proxy commands the player may currently use, filtered by permission.
	if filtered := filterNode(root, player); filtered != nil {
		filtered.ChildrenOrdered().Range(func(_ string, node brigodier.CommandNode) bool {
			newRoot.AddChild(node)
			return true
		})
	}

	if err := player.WritePacket(&packet.AvailableCommands{RootNode: newRoot}); err != nil {
		r.proxy.log.V(1).Info("failed to resend commands after permission update", "uuid", id, "error", err.Error())
		return
	}
	r.proxy.log.V(1).Info("resent command list after permission update", "uuid", id)
}

// filterNode mirrors the proxy's internal command filtering: it copies the node
// tree, dropping any node the source may not use (CanUse / permission check).
// Ported from go.minekube.com/gate ...proxy.filterNode (unexported there).
func filterNode(src brigodier.CommandNode, cmdSrc command.Source) brigodier.CommandNode {
	var dest brigodier.CommandNode
	if _, ok := src.(*brigodier.RootCommandNode); ok {
		dest = &brigodier.RootCommandNode{}
	} else {
		if !src.CanUse(command.ContextWithSource(context.Background(), cmdSrc)) {
			return nil
		}
		builder := src.CreateBuilder().Requires(func(context.Context) bool { return true })
		if src.Redirect() != nil {
			builder.Redirect(filterNode(src.Redirect(), cmdSrc))
		}
		dest = builder.Build()
	}

	src.ChildrenOrdered().Range(func(_ string, child brigodier.CommandNode) bool {
		if filtered := filterNode(child, cmdSrc); filtered != nil {
			dest.AddChild(filtered)
		}
		return true
	})
	return dest
}
