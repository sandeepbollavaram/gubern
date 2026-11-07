package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sandeepbollavaram/vortex/internal/audit"
	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

// identityStorePath is the identity database beside the API key store, in the
// same private per-user directory (design §3).
func identityStorePath(keyStorePath string) string {
	return filepath.Join(filepath.Dir(keyStorePath), "identity.db")
}

// openIdentityStore opens the identity store for the running server, mirrors
// its lifecycle events into the HMAC-chained audit log, and binds every API
// key not yet bound to a principal (legacy migration, design §9).
func openIdentityStore(keyStore *auth.APIKeyStore, keyStorePath string, auditLog *audit.Log, log *slog.Logger) (*identity.Store, error) {
	st, err := identity.Open(identityStorePath(keyStorePath))
	if err != nil {
		return nil, err
	}
	if auditLog != nil {
		st.OnAudit = func(e identity.AuditEvent) {
			_ = auditLog.Append(context.Background(), e.Actor, "identity."+e.Action, e.Target,
				map[string]any{"detail": e.Detail})
		}
	}
	rep, err := st.MigrateKeys(legacyKeys(keyStore))
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("binding API keys to principals: %w", err)
	}
	if !rep.Empty() {
		log.Info("API keys bound to principals",
			"owner", len(rep.Owner), "service", len(rep.Service), "no_grants", len(rep.NoGrants))
	}
	if len(rep.NoGrants) > 0 {
		log.Warn("API keys with no known role were given no permissions; re-issue them with a role",
			"key_ids", strings.Join(rep.NoGrants, ","))
	}
	return st, nil
}

func legacyKeys(ks *auth.APIKeyStore) []identity.LegacyKey {
	var out []identity.LegacyKey
	for _, k := range ks.All() {
		roles := make([]string, 0, len(k.Roles))
		for _, r := range k.Roles {
			roles = append(roles, string(r))
		}
		out = append(out, identity.LegacyKey{
			ID: k.ID, UserID: k.UserID, Description: k.Description, Roles: roles,
			Revoked: ks.IsRevoked(k.ID),
		})
	}
	return out
}

// newIdentityCommand builds `gubern identity`: local administration of
// principals and owner recovery. Every subcommand works on the files in this
// user's private Gubern directory; none of them has a network endpoint.
func newIdentityCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "identity",
		Short: "Manage people, service principals and owner recovery (local only)",
		Long: "Manage Gubern's identity store on this machine.\n\n" +
			"These commands read and write the identity database beside the API key\n" +
			"store. Holding access to that private directory is the proof of ownership;\n" +
			"no subcommand talks to the network.",
	}
	c.AddCommand(newIdentityListCommand(), newIdentityRecoverOwnerCommand(),
		newIdentityBindKeyCommand(), newIdentityAddPersonCommand())
	return c
}

// openLocalIdentity opens the key store and identity store for a CLI command.
func openLocalIdentity() (*auth.APIKeyStore, string, *identity.Store, error) {
	ks, ksPath := openAPIKeyStore(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ks.SetPath(ksPath)
	st, err := identity.Open(identityStorePath(ksPath))
	if err != nil {
		return nil, "", nil, err
	}
	// Keys written while the server was stopped get their principals now, by
	// the same rules the server applies at start.
	if _, err := st.MigrateKeys(legacyKeys(ks)); err != nil {
		_ = st.Close()
		return nil, "", nil, err
	}
	return ks, ksPath, st, nil
}

// serverRunning reports whether a Gubern server answers on the management
// address. Owner recovery refuses to run while it does.
func serverRunning() bool {
	addr := managementAPIListenAddr()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/health")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func newIdentityListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List principals, their bound keys and grants",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _, st, err := openLocalIdentity()
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			ps, err := st.Principals()
			if err != nil {
				return err
			}
			bound, _ := st.BoundKeys()
			keysOf := map[string][]string{}
			for k, p := range bound {
				keysOf[p] = append(keysOf[p], k)
			}
			out := cmd.OutOrStdout()
			for _, p := range ps {
				gs, _ := st.Grants(p.ID)
				fmt.Fprintf(out, "%s\t%s\t%s\t%q\tkeys=%s\tgrants=%d\n",
					p.ID, p.Kind, p.Status, p.Name, strings.Join(keysOf[p.ID], ","), len(gs))
			}
			return nil
		},
	}
}

func newIdentityRecoverOwnerCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "recover-owner",
		Short: "Issue a new owner credential (server must be stopped)",
		Long: "Re-enable the owner person, restore its grants, and issue a new owner\n" +
			"API key written to the tui-key file (mode 0600). Only the key id is printed.\n\n" +
			"The server must be stopped: that proves control of this machine and keeps\n" +
			"a single writer on the key store. There is no network equivalent.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if serverRunning() {
				return errors.New("a Gubern server is running on " + managementAPIListenAddr() +
					"; stop it (gubern stop) before recovering the owner")
			}
			ks, ksPath, st, err := openLocalIdentity()
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			key, secret, err := ks.Issue("admin", "default", []auth.Role{auth.RoleAdmin}, "owner recovery", 0)
			if err != nil {
				return err
			}
			if err := ks.Save(ksPath); err != nil {
				return fmt.Errorf("saving key store: %w", err)
			}
			if err := st.RecoverOwner(key.ID); err != nil {
				_ = ks.Revoke(key.ID)
				return fmt.Errorf("binding the new key to the owner: %w", err)
			}
			keyPath := tuiKeyPath()
			if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(keyPath, []byte(secret), 0o600); err != nil {
				return fmt.Errorf("writing %s: %w", keyPath, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Owner recovered. New owner key %s written to %s\n", key.ID, keyPath)
			return nil
		},
	}
}

func newIdentityBindKeyCommand() *cobra.Command {
	var principal string
	c := &cobra.Command{
		Use:   "bind-key <key-id> --principal <id>",
		Short: "Bind an existing API key to a principal (e.g. a legacy key to its person)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if principal == "" {
				return errors.New("--principal is required")
			}
			ks, _, st, err := openLocalIdentity()
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			if ks.IsRevoked(args[0]) {
				return fmt.Errorf("key %s is revoked", args[0])
			}
			found := false
			for _, k := range ks.All() {
				found = found || k.ID == args[0]
			}
			if !found {
				return fmt.Errorf("no key %s in the key store", args[0])
			}
			if _, err := st.Principal(principal); err != nil {
				return fmt.Errorf("principal %s: %w", principal, err)
			}
			if err := st.BindCredential(args[0], principal, identity.LocalCLIActor); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Key %s now authenticates as %s\n", args[0], principal)
			return nil
		},
	}
	c.Flags().StringVar(&principal, "principal", "", "principal id to bind the key to")
	return c
}

func newIdentityAddPersonCommand() *cobra.Command {
	var role, keyFile string
	c := &cobra.Command{
		Use:   "add-person <name> --role <role> --key-file <path>",
		Short: "Add a person with a role template and issue their API key into a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if authz.Template(role, authz.Person) == nil || role == "owner" {
				return fmt.Errorf("unknown role %q (admin, operator, viewer, readonly)", role)
			}
			if keyFile == "" {
				return errors.New("--key-file is required; the secret is never printed")
			}
			ks, ksPath, st, err := openLocalIdentity()
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			p, err := st.CreatePrincipal("", authz.Person, args[0], identity.LocalCLIActor)
			if err != nil {
				return err
			}
			if err := st.ApplyTemplate(p.ID, role, identity.LocalCLIActor); err != nil {
				return err
			}
			key, secret, err := ks.Issue(p.ID, "default", []auth.Role{auth.Role(role)}, "person "+args[0], 0)
			if err != nil {
				return err
			}
			if err := ks.Save(ksPath); err != nil {
				return err
			}
			if err := st.BindCredential(key.ID, p.ID, identity.LocalCLIActor); err != nil {
				_ = ks.Revoke(key.ID)
				return err
			}
			if err := os.WriteFile(keyFile, []byte(secret), 0o600); err != nil {
				_ = ks.Revoke(key.ID)
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Person %s (%s) added with role %s; key %s written to %s\n",
				p.ID, p.Name, role, key.ID, keyFile)
			return nil
		},
	}
	c.Flags().StringVar(&role, "role", "", "role template: admin, operator, viewer or readonly")
	c.Flags().StringVar(&keyFile, "key-file", "", "file to write the new key's secret to (mode 0600)")
	return c
}
