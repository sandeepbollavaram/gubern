package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/config"
)

// Local FS tools are registered once at startup (buildAgentRuntime →
// RegisterLocalTools). SIGHUP reload updates config.Holder but does not rebuild
// tool registries. This test documents that resolveLocalFSConfig — the same
// helper used at startup — reflects a swapped Holder, which is necessary but
// not sufficient for live local-tool behavior.
func TestResolveLocalFSConfig_FollowsConfigHolderSwap(t *testing.T) {
	trusted := t.TempDir()
	work := filepath.Join(trusted, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}

	holder := config.NewHolder(&config.Config{
		Agents: config.Agents{
			LocalTools: config.LocalTools{TrustedRoot: trusted, AutoMode: false},
		},
	})

	first, err := resolveLocalFSConfig(holder.Get(), work)
	if err != nil {
		t.Fatalf("first resolveLocalFSConfig: %v", err)
	}
	want, _ := filepath.Abs(trusted)
	if first.TrustedRoot != want || first.AutoMode {
		t.Fatalf("first = %+v, want TrustedRoot=%q AutoMode=false", first, want)
	}

	holder.Store(&config.Config{
		Agents: config.Agents{
			LocalTools: config.LocalTools{TrustedRoot: trusted, AutoMode: true},
		},
	})

	second, err := resolveLocalFSConfig(holder.Get(), work)
	if err != nil {
		t.Fatalf("second resolveLocalFSConfig after holder swap: %v", err)
	}
	if second.TrustedRoot != want || !second.AutoMode {
		t.Fatalf("second = %+v, want TrustedRoot=%q AutoMode=true", second, want)
	}
}

// TestResolveLocalFSConfig_RejectsUnknownPolicyTool: a misspelt deny entry
// would leave the tool enabled, so startup refuses instead.
func TestResolveLocalFSConfig_RejectsUnknownPolicyTool(t *testing.T) {
	cfg := &config.Config{Agents: config.Agents{Policy: config.AgentPolicy{DenyTools: []string{"run_terminl"}}}}
	if _, err := resolveLocalFSConfig(cfg, t.TempDir()); err == nil || !strings.Contains(err.Error(), "run_terminl") {
		t.Errorf("err = %v", err)
	}
	cfg.Agents.Policy.DenyTools = []string{"run_terminal"}
	fs, err := resolveLocalFSConfig(cfg, t.TempDir())
	if err != nil || len(fs.Policy.Deny) != 1 {
		t.Errorf("valid policy: %+v, %v", fs.Policy, err)
	}
}
