package identity

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sandeepbollavaram/vortex/internal/authz"
)

// LegacyKey is what the migration needs to know about a key in the API key
// store. It carries no secret or hash.
type LegacyKey struct {
	ID          string
	UserID      string
	Description string
	Roles       []string
	Revoked     bool
}

// bootstrapDescriptions are the descriptions Gubern itself gives the local
// operator's keys: `gubern setup` ("setup key"), the TUI key import
// ("tui-setup-key"), and the key created for an empty store ("auto-created").
var bootstrapDescriptions = map[string]bool{"setup key": true, "tui-setup-key": true, "auto-created": true}

// IsBootstrapDescription reports whether desc is reserved for the local
// operator's bootstrap keys. The API refuses to issue keys with it, so a key
// that somehow stayed unbound can never be mistaken for one.
func IsBootstrapDescription(desc string) bool { return bootstrapDescriptions[desc] }

// IsBootstrapKey reports whether k is one of the local operator's bootstrap
// keys: a bootstrap description, user "admin", and exactly the admin role.
// Those are created on this machine by setup or first start, so they belong
// to the owner. Anything else is a legacy service key.
func IsBootstrapKey(k LegacyKey) bool {
	return bootstrapDescriptions[k.Description] && k.UserID == "admin" &&
		len(k.Roles) == 1 && k.Roles[0] == "admin"
}

// EnsureOwner creates the owner person with the owner template when the store
// has none. It reports whether it created one.
func (s *Store) EnsureOwner() (bool, error) {
	if _, err := s.Principal(OwnerID); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if _, err := s.CreatePrincipal(OwnerID, authz.Person, "Owner", SystemActor); err != nil {
		return false, err
	}
	if err := s.ApplyTemplate(OwnerID, "owner", SystemActor); err != nil {
		return false, err
	}
	return true, nil
}

// MigrationReport says what MigrateKeys did with each key id.
type MigrationReport struct {
	Owner    []string // bound to the owner person
	Service  []string // bound to a new legacy service principal with role grants
	NoGrants []string // bound to a legacy service principal with no grants (unknown roles)
}

// Empty reports whether nothing was migrated.
func (r MigrationReport) Empty() bool {
	return len(r.Owner)+len(r.Service)+len(r.NoGrants) == 0
}

// MigrateKeys binds every unrevoked key that is not yet bound (design §9):
//
//   - a bootstrap key (IsBootstrapKey) is bound to the owner person;
//   - any other key gets its own service principal "legacy-key-<id>" with the
//     grants of its roles' templates for a service: never approvals:decide,
//     never more than the role meant;
//   - a key with no known role gets a service principal with no grants.
//
// It is idempotent: bound keys are skipped, so it runs on every start and
// also picks up keys `gubern setup` wrote while the server was stopped.
func (s *Store) MigrateKeys(keys []LegacyKey) (MigrationReport, error) {
	var rep MigrationReport
	if _, err := s.EnsureOwner(); err != nil {
		return rep, err
	}
	bound, err := s.BoundKeys()
	if err != nil {
		return rep, err
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	for _, k := range keys {
		if k.Revoked || k.ID == "" || bound[k.ID] != "" {
			continue
		}
		if IsBootstrapKey(k) {
			if err := s.BindCredential(k.ID, OwnerID, SystemActor); err != nil {
				return rep, err
			}
			rep.Owner = append(rep.Owner, k.ID)
			continue
		}
		pid := "legacy-key-" + k.ID
		if _, err := s.Principal(pid); errors.Is(err, ErrNotFound) {
			if _, err := s.CreatePrincipal(pid, authz.Service, legacyName(k), SystemActor); err != nil {
				return rep, err
			}
		} else if err != nil {
			return rep, err
		}
		granted := false
		for _, role := range k.Roles {
			if authz.Template(role, authz.Service) == nil || role == "owner" {
				continue // unknown role: no grants, never a guess
			}
			if err := s.ApplyTemplate(pid, role, SystemActor); err != nil {
				return rep, err
			}
			granted = true
		}
		if err := s.BindCredential(k.ID, pid, SystemActor); err != nil {
			return rep, err
		}
		if granted {
			rep.Service = append(rep.Service, k.ID)
		} else {
			rep.NoGrants = append(rep.NoGrants, k.ID)
		}
	}
	if !rep.Empty() {
		_ = s.Record(SystemActor, "migration.keys", "keys",
			fmt.Sprintf("owner=%d service=%d no-grants=%d", len(rep.Owner), len(rep.Service), len(rep.NoGrants)))
	}
	return rep, nil
}

func legacyName(k LegacyKey) string {
	d := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, k.Description)
	if len(d) > 80 {
		d = d[:80]
	}
	if d == "" {
		return "Legacy key " + k.ID
	}
	return "Legacy key " + k.ID + " (" + d + ")"
}

// RecoverOwner re-enables the owner person (creating it when missing) and
// binds keyID to it. Only the local recovery CLI calls it, with the server
// stopped (design §10).
func (s *Store) RecoverOwner(keyID string) error {
	if _, err := s.EnsureOwner(); err != nil {
		return err
	}
	if err := s.SetStatus(OwnerID, StatusActive, LocalCLIActor); err != nil {
		return err
	}
	// The owner must hold the owner template; re-apply it when grants were
	// revoked, without duplicating live ones.
	gs, err := s.Grants(OwnerID)
	if err != nil {
		return err
	}
	have := map[authz.Scope]bool{}
	for _, g := range gs {
		if g.Resource == authz.AnyResource {
			have[g.Scope] = true
		}
	}
	p, err := s.Principal(OwnerID)
	if err != nil {
		return err
	}
	err = s.tx(func(tx *sql.Tx) error {
		for _, g := range authz.Template("owner", authz.Person) {
			if have[g.Scope] {
				continue
			}
			if _, err := s.addGrantTx(tx, p, g.Scope, g.Resource, LocalCLIActor, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.BindCredential(keyID, OwnerID, LocalCLIActor); err != nil {
		return err
	}
	return s.Record(LocalCLIActor, "owner.recover", OwnerID, "new credential key:"+keyID)
}
