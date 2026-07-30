package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
	"github.com/gentleman-programming/gentle-ai/v2/internal/versions"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name            string
		lookPathPath    string
		lookPathErr     error
		stat            statResult
		wantInstalled   bool
		wantBinaryPath  string
		wantConfigPath  string
		wantConfigFound bool
		wantErr         bool
	}{
		{
			name:            "binary and config directory found",
			lookPathPath:    "/usr/local/bin/claude",
			stat:            statResult{isDir: true},
			wantInstalled:   true,
			wantBinaryPath:  "/usr/local/bin/claude",
			wantConfigPath:  filepath.Join("/tmp/home", ".claude"),
			wantConfigFound: true,
		},
		{
			name:            "binary missing and config missing",
			lookPathErr:     errors.New("missing"),
			stat:            statResult{err: os.ErrNotExist},
			wantInstalled:   false,
			wantBinaryPath:  "",
			wantConfigPath:  filepath.Join("/tmp/home", ".claude"),
			wantConfigFound: false,
		},
		{
			name:           "stat error bubbles up",
			lookPathPath:   "/usr/local/bin/claude",
			stat:           statResult{err: errors.New("permission denied")},
			wantConfigPath: "",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Adapter{
				lookPath: func(string) (string, error) {
					return tt.lookPathPath, tt.lookPathErr
				},
				statPath: func(string) statResult {
					return tt.stat
				},
			}

			installed, binaryPath, configPath, configFound, err := a.Detect(context.Background(), "/tmp/home")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Detect() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if installed != tt.wantInstalled {
				t.Fatalf("Detect() installed = %v, want %v", installed, tt.wantInstalled)
			}

			if binaryPath != tt.wantBinaryPath {
				t.Fatalf("Detect() binaryPath = %q, want %q", binaryPath, tt.wantBinaryPath)
			}

			if configPath != tt.wantConfigPath {
				t.Fatalf("Detect() configPath = %q, want %q", configPath, tt.wantConfigPath)
			}

			if configFound != tt.wantConfigFound {
				t.Fatalf("Detect() configFound = %v, want %v", configFound, tt.wantConfigFound)
			}
		})
	}
}

func TestAdapter_SubAgentCapability(t *testing.T) {
	a := NewAdapter()

	if got := a.SupportsSubAgents(); got != true {
		t.Errorf("SupportsSubAgents() = %v, want true", got)
	}

	homeDir := "/home/test"
	wantDir := filepath.Join(homeDir, ".claude", "agents")
	if got := a.SubAgentsDir(homeDir); got != wantDir {
		t.Errorf("SubAgentsDir(%q) = %q, want %q", homeDir, got, wantDir)
	}

	if got := a.EmbeddedSubAgentsDir(); got != "claude/agents" {
		t.Errorf("EmbeddedSubAgentsDir() = %q, want %q", got, "claude/agents")
	}
}

func TestInstallCommand(t *testing.T) {
	a := NewAdapter()

	tests := []struct {
		name    string
		profile system.PlatformProfile
		want    [][]string
	}{
		{
			name:    "darwin profile uses npm without sudo",
			profile: system.PlatformProfile{OS: "darwin", PackageManager: "brew"},
			want:    [][]string{{"npm", "install", "-g", "--ignore-scripts", "@anthropic-ai/claude-code@" + versions.ClaudeCode}},
		},
		{
			name:    "ubuntu profile uses sudo npm",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroUbuntu, PackageManager: "apt"},
			want:    [][]string{{"sudo", "npm", "install", "-g", "--ignore-scripts", "@anthropic-ai/claude-code@" + versions.ClaudeCode}},
		},
		{
			name:    "arch profile uses sudo npm",
			profile: system.PlatformProfile{OS: "linux", LinuxDistro: system.LinuxDistroArch, PackageManager: "pacman"},
			want:    [][]string{{"sudo", "npm", "install", "-g", "--ignore-scripts", "@anthropic-ai/claude-code@" + versions.ClaudeCode}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, err := a.InstallCommand(tt.profile)
			if err != nil {
				t.Fatalf("InstallCommand() returned error: %v", err)
			}

			if !reflect.DeepEqual(command, tt.want) {
				t.Fatalf("InstallCommand() = %v, want %v", command, tt.want)
			}
		})
	}
}

func TestSlashCommands(t *testing.T) {
	a := NewAdapter()

	if !a.SupportsSlashCommands() {
		t.Fatal("SupportsSlashCommands() = false, want true")
	}

	got := a.CommandsDir("/home/u")
	want := filepath.Join("/home/u", ".claude", "commands")
	if got != want {
		t.Fatalf("CommandsDir() = %q, want %q", got, want)
	}
}

// TestMergeUserConfigSerializesConcurrentWriters drives eight concurrent
// merges of distinct servers into the same registry. Without the advisory
// lock the read-merge-write sequences interleave and drop registrations;
// with it every writer must land, deterministically.
func TestMergeUserConfigSerializesConcurrentWriters(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.WriteFile(UserConfigPath(homeDir), []byte(`{"oauthAccount":{"emailAddress":"user@example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	const writers = 8
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		overlay := []byte(fmt.Sprintf(`{"mcpServers":{"server-%d":{"command":"cmd-%d"}}}`, i, i))
		go func(overlay []byte) {
			_, _, err := MergeUserConfig(homeDir, overlay)
			errs <- err
		}(overlay)
	}
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent MergeUserConfig error = %v", err)
		}
	}

	raw, err := os.ReadFile(UserConfigPath(homeDir))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("registry unparsable after concurrent merges: %v", err)
	}
	servers, _ := config["mcpServers"].(map[string]any)
	for i := 0; i < writers; i++ {
		if _, ok := servers[fmt.Sprintf("server-%d", i)]; !ok {
			t.Fatalf("registration server-%d was lost by a concurrent merge: %#v", i, servers)
		}
	}
	if _, ok := config["oauthAccount"]; !ok {
		t.Fatalf("oauthAccount was lost by a concurrent merge: %#v", config)
	}
}

// TestMergeUserConfigWaitsForLockHolder proves a merge cannot run while
// another gentle-ai process holds the registry lock: it must wait for the
// release instead of writing through it.
func TestMergeUserConfigWaitsForLockHolder(t *testing.T) {
	homeDir := t.TempDir()
	release, err := LockUserConfig(homeDir)
	if err != nil {
		t.Fatalf("LockUserConfig() error = %v", err)
	}

	merged := make(chan error, 1)
	go func() {
		_, _, err := MergeUserConfig(homeDir, []byte(`{"mcpServers":{"context7":{"command":"npx"}}}`))
		merged <- err
	}()

	select {
	case err := <-merged:
		t.Fatalf("merge finished while the lock was held (err = %v)", err)
	case <-time.After(300 * time.Millisecond):
	}

	if err := release(); err != nil {
		t.Fatalf("release lock: %v", err)
	}
	if err := <-merged; err != nil {
		t.Fatalf("merge after release error = %v", err)
	}
	if _, err := os.Stat(UserConfigPath(homeDir)); err != nil {
		t.Fatalf("registry missing after released merge: %v", err)
	}
}
