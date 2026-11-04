// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package ipnauth

import (
	"context"

	"tappervpn.com/ipn"
)

// Self is a caller identity that represents the tappervpnd itself and therefore
// has unlimited access.
var Self Actor = unrestricted{}

// TODO is a caller identity used when the operation is performed on behalf of a user,
// rather than by tappervpnd itself, but the surrounding function is not yet extended
// to accept an [Actor] parameter. It grants the same unrestricted access as [Self].
var TODO Actor = unrestricted{}

// unrestricted is an [Actor] that has unlimited access to the currently running
// tappervpnd instance. It's typically used for operations performed by tappervpnd
// on its own, or upon a request from the control plane, rather on behalf of a user.
type unrestricted struct{}

// UserID implements [Actor].
func (unrestricted) UserID() ipn.WindowsUserID { return "" }

// Username implements [Actor].
func (unrestricted) Username() (string, error) { return "", nil }

// Context implements [Actor].
func (unrestricted) Context() context.Context { return context.Background() }

// ClientID implements [Actor].
// It always returns (NoClientID, false) because the tappervpnd itself
// is not a connected LocalAPI client.
func (unrestricted) ClientID() (_ ClientID, ok bool) { return NoClientID, false }

// CheckProfileAccess implements [Actor].
func (unrestricted) CheckProfileAccess(_ ipn.LoginProfileView, _ ProfileAccess, _ AuditLogFunc) error {
	// Unrestricted access to all profiles.
	return nil
}

// IsLocalSystem implements [Actor].
//
// Deprecated: this method exists for compatibility with the current (as of 2025-01-28)
// permission model and will be removed as we progress on tappervpn/corp#18342.
func (unrestricted) IsLocalSystem() bool { return false }

// IsLocalAdmin implements [Actor].
//
// Deprecated: this method exists for compatibility with the current (as of 2025-01-28)
// permission model and will be removed as we progress on tappervpn/corp#18342.
func (unrestricted) IsLocalAdmin(operatorUID string) bool { return false }

// IsTapperVPNd reports whether the given Actor represents TapperVPNd itself,
// such as [Self] or a [TODO] placeholder actor.
func IsTapperVPNd(a Actor) bool {
	_, ok := a.(unrestricted)
	return ok
}
