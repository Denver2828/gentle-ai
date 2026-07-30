//go:build !windows

package claude

import (
	"fmt"
	"os"
)

// SecureUserConfig enforces owner-only access on ~/.claude.json. On POSIX
// platforms that is exactly chmod 0600.
func SecureUserConfig(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("tighten mode of %q: %w", path, err)
	}
	return nil
}
