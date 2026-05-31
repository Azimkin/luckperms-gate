package luckperms

import (
	"context"
	"time"

	"go.minekube.com/gate/pkg/edition/java/proxy"
	"go.minekube.com/gate/pkg/util/permission"
)

// loadTimeout bounds the synchronous user load during permission setup.
const loadTimeout = 10 * time.Second

// onPermissionsSetup loads the player's data and installs the permission function,
// mirroring VelocityConnectionListener.onPlayerPermissionsSetup.
func (pl *plugin) onPermissionsSetup(e *proxy.PermissionsSetupEvent) {
	player, ok := e.Subject().(proxy.Player)
	if !ok {
		return // console or non-player subject; keep default function
	}

	id := player.ID()
	ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
	defer cancel()

	if _, err := pl.manager.LoadUser(ctx, id, player.Username()); err != nil {
		pl.log.Error(err, "failed to load permissions for player", "uuid", id, "username", player.Username())
		// Leave the default permission function in place rather than blocking login.
		return
	}

	e.SetFunc(func(perm string) permission.TriState {
		u := pl.manager.getUser(id)
		if u == nil {
			return permission.Undefined
		}
		return pl.manager.Check(u, perm).toGate()
	})
}

// onDisconnect unloads cached data for the player.
func (pl *plugin) onDisconnect(e *proxy.DisconnectEvent) {
	id := e.Player().ID()
	pl.manager.Unload(id)
	pl.refresher.forget(id)
}
