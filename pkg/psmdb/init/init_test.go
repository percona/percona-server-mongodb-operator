package init

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJournalMigrationScript(t *testing.T) {
	t.Parallel()

	type dirs struct{ data, src, dst string }

	write := func(t *testing.T, path, content string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	tests := map[string]struct {
		needsGNUStat bool
		setup        func(t *testing.T, d dirs)
		wantSrc      map[string]string
		wantDst      map[string]string
	}{
		"fresh member": {
			setup:   func(t *testing.T, d dirs) {},
			wantDst: map[string]string{},
		},
		"stale journal after the data volume was wiped": {
			setup: func(t *testing.T, d dirs) {
				write(t, filepath.Join(d.dst, "WiredTigerLog.0000000001"), "old")
				write(t, filepath.Join(d.dst, "WiredTigerPreplog.0000000001"), "old")
				require.NoError(t, os.Mkdir(filepath.Join(d.dst, "lost+found"), 0o755))
			},
			wantDst: map[string]string{"lost+found": ""},
		},
		"journal migrated from the data volume": {
			needsGNUStat: true,
			setup: func(t *testing.T, d dirs) {
				write(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
				write(t, filepath.Join(d.src, "WiredTigerLog.0000000002"), "journal")
			},
			wantSrc: map[string]string{},
			wantDst: map[string]string{"WiredTigerLog.0000000002": "journal"},
		},
		"partial copy is overwritten on rerun": {
			needsGNUStat: true,
			setup: func(t *testing.T, d dirs) {
				write(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
				write(t, filepath.Join(d.src, "WiredTigerLog.0000000002"), "journal")
				write(t, filepath.Join(d.dst, "WiredTigerLog.0000000002"), "jo")
			},
			wantSrc: map[string]string{},
			wantDst: map[string]string{"WiredTigerLog.0000000002": "journal"},
		},
		"already migrated": {
			setup: func(t *testing.T, d dirs) {
				write(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
				require.NoError(t, os.MkdirAll(d.src, 0o755))
				write(t, filepath.Join(d.dst, "WiredTigerLog.0000000003"), "current")
			},
			wantSrc: map[string]string{},
			wantDst: map[string]string{"WiredTigerLog.0000000003": "current"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tt.needsGNUStat && runtime.GOOS != "linux" {
				t.Skip("the script relies on GNU stat")
			}

			root := t.TempDir()
			d := dirs{
				data: filepath.Join(root, "data"),
				src:  filepath.Join(root, "data", "journal"),
				dst:  filepath.Join(root, "journal"),
			}
			require.NoError(t, os.MkdirAll(d.data, 0o755))
			require.NoError(t, os.MkdirAll(d.dst, 0o755))
			tt.setup(t, d)

			out, err := exec.CommandContext(t.Context(), "/bin/sh", "-c", journalMigrationScript, "journal-migration", d.data, d.src, d.dst).CombinedOutput()
			require.NoError(t, err, string(out))

			if tt.wantSrc != nil {
				assert.Equal(t, tt.wantSrc, readDir(t, d.src))
			}
			assert.Equal(t, tt.wantDst, readDir(t, d.dst))
		})
	}
}

func readDir(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	got := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			got[e.Name()] = ""
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		got[e.Name()] = string(b)
	}
	return got
}
