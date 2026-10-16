//go:build !windows

package signing

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A key file that is a FIFO would block the read for ever; a device such as
// /dev/zero would never end. Neither is a key.
func TestAKeyFileThatIsNotARegularFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "x.pem")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	for _, path := range []string{fifo, dir} {
		if _, err := LoadTrustedKeys([]string{path}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("LoadTrustedKeys(%s) = %v, want a refusal", path, err)
		}
	}
}
