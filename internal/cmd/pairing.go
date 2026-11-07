package cmd

import (
	"context"
	"errors"

	"github.com/sandeepbollavaram/vortex/internal/identity"
	"github.com/sandeepbollavaram/vortex/internal/messaging"
)

// channelIdentity connects Telegram and WhatsApp to the identity store
// (milestone F): paired accounts are let in, revoked ones refused, and
// "/pair <code>" links the sending account to the code's person.
func channelIdentity(ids *identity.Store) messaging.ChannelIdentity {
	return messaging.ChannelIdentity{
		Linked: func(provider, subject string) bool {
			_, err := ids.ExternalPrincipal(provider, subject)
			return err == nil
		},
		Revoked: ids.ExternalRevoked,
		Pair: func(_ context.Context, provider, subject, code string) string {
			pr, err := ids.RedeemExternal(code, provider, subject)
			switch {
			case err == nil:
				name := pr.PrincipalID
				if p, perr := ids.Principal(pr.PrincipalID); perr == nil {
					name = p.Name
				}
				return "✅ This account is now linked to " + name + ". Messages and approvals from it act as that person."
			case errors.Is(err, identity.ErrAlreadyPaired):
				return "This account is already linked to that person."
			case errors.Is(err, identity.ErrIdentityConflict):
				return "This account is linked to someone else. Ask the owner to unlink it first."
			default:
				ids.RecordPairingFailure(provider, subject, "invalid, used or expired code")
				return "That code is invalid, used or expired."
			}
		},
	}
}
