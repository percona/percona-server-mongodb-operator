package init

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type migrationDirs struct{ data, src, dst, moved string }

func newMigrationDirs(t *testing.T) migrationDirs {
	t.Helper()

	root := t.TempDir()
	d := migrationDirs{
		data:  filepath.Join(root, "data"),
		src:   filepath.Join(root, "data", "journal"),
		dst:   filepath.Join(root, "journal"),
		moved: filepath.Join(root, "data", ".journal-migrated"),
	}
	require.NoError(t, os.MkdirAll(d.data, 0o755))
	require.NoError(t, os.MkdirAll(d.dst, 0o755))
	return d
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func runMigration(t *testing.T, d migrationDirs, env ...string) ([]byte, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", journalMigrationScript, "journal-migration", d.data, d.src, d.dst)
	cmd.Env = append(os.Environ(), env...)
	return cmd.CombinedOutput()
}

func TestJournalMigrationScript(t *testing.T) {
	t.Parallel()

	tornLog := "WiredTigerLog\x00\x01\x02" + string(make([]byte, 100))

	tests := map[string]struct {
		setup   func(t *testing.T, d migrationDirs)
		wantSrc map[string]string
		wantDst map[string]string
	}{
		"fresh member": {
			setup:   func(t *testing.T, d migrationDirs) {},
			wantDst: map[string]string{},
		},
		"stale journal after the data volume was wiped": {
			setup: func(t *testing.T, d migrationDirs) {
				writeFile(t, filepath.Join(d.dst, "WiredTigerLog.0000000001"), "old")
				writeFile(t, filepath.Join(d.dst, "WiredTigerPreplog.0000000001"), "old")
				require.NoError(t, os.Mkdir(filepath.Join(d.dst, "lost+found"), 0o755))
			},
			wantDst: map[string]string{"lost+found": ""},
		},
		"journal migrated from the data volume": {
			setup: func(t *testing.T, d migrationDirs) {
				writeFile(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
				writeFile(t, filepath.Join(d.src, "WiredTigerLog.0000000002"), "journal")
			},
			wantSrc: map[string]string{},
			wantDst: map[string]string{"WiredTigerLog.0000000002": "journal"},
		},
		"partial journal is copied byte for byte": {
			setup: func(t *testing.T, d migrationDirs) {
				writeFile(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
				writeFile(t, filepath.Join(d.src, "WiredTigerLog.0000000007"), tornLog)
				writeFile(t, filepath.Join(d.src, "WiredTigerPreplog.0000000001"), "")
			},
			wantSrc: map[string]string{},
			wantDst: map[string]string{"WiredTigerLog.0000000007": tornLog, "WiredTigerPreplog.0000000001": ""},
		},
		"partial copy is overwritten on rerun": {
			setup: func(t *testing.T, d migrationDirs) {
				writeFile(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
				writeFile(t, filepath.Join(d.src, "WiredTigerLog.0000000002"), "journal")
				writeFile(t, filepath.Join(d.dst, "WiredTigerLog.0000000002"), "jo")
			},
			wantSrc: map[string]string{},
			wantDst: map[string]string{"WiredTigerLog.0000000002": "journal"},
		},
		"restored journal replaces the old one": {
			setup: func(t *testing.T, d migrationDirs) {
				writeFile(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
				writeFile(t, filepath.Join(d.src, "WiredTigerLog.0000000002"), "restored")
				writeFile(t, filepath.Join(d.dst, "WiredTigerLog.0000000002"), "old")
				writeFile(t, filepath.Join(d.dst, "WiredTigerLog.0000000005"), "old")
				require.NoError(t, os.Mkdir(filepath.Join(d.dst, "lost+found"), 0o755))
			},
			wantSrc: map[string]string{},
			wantDst: map[string]string{"WiredTigerLog.0000000002": "restored", "lost+found": ""},
		},
		"already migrated": {
			setup: func(t *testing.T, d migrationDirs) {
				writeFile(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
				require.NoError(t, os.MkdirAll(d.src, 0o755))
				writeFile(t, filepath.Join(d.dst, "WiredTigerLog.0000000003"), "current")
			},
			wantSrc: map[string]string{},
			wantDst: map[string]string{"WiredTigerLog.0000000003": "current"},
		},
		"leftover from an interrupted cleanup is removed": {
			setup: func(t *testing.T, d migrationDirs) {
				writeFile(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
				require.NoError(t, os.MkdirAll(d.src, 0o755))
				writeFile(t, filepath.Join(d.moved, "WiredTigerLog.0000000003"), "current")
				writeFile(t, filepath.Join(d.dst, "WiredTigerLog.0000000003"), "current")
			},
			wantSrc: map[string]string{},
			wantDst: map[string]string{"WiredTigerLog.0000000003": "current"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newMigrationDirs(t)
			tt.setup(t, d)

			out, err := runMigration(t, d)
			require.NoError(t, err, string(out))

			if tt.wantSrc != nil {
				assert.Equal(t, tt.wantSrc, readDir(t, d.src))
			}
			assert.Equal(t, tt.wantDst, readDir(t, d.dst))
			assert.NoDirExists(t, d.moved)
		})
	}
}

func TestJournalMigrationScriptCrash(t *testing.T) {
	t.Parallel()

	journal := map[string]string{
		"WiredTigerLog.0000000002":     "journal-2",
		"WiredTigerLog.0000000003":     "journal-3",
		"WiredTigerPreplog.0000000001": "prep",
	}

	tests := map[string]struct {
		cmd    string
		failOn int
	}{
		"copy fails after the first file":         {cmd: "cp", failOn: 2},
		"rename of the source fails":              {cmd: "mv", failOn: 1},
		"killed before the source was recreated":  {cmd: "mkdir", failOn: 1},
		"killed while removing the moved journal": {cmd: "rm", failOn: 2},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newMigrationDirs(t)
			writeFile(t, filepath.Join(d.data, "WiredTiger.wt"), "meta")
			for f, content := range journal {
				writeFile(t, filepath.Join(d.src, f), content)
			}

			out, err := runMigration(t, d, "PATH="+failingShim(t, tt.cmd, tt.failOn)+string(os.PathListSeparator)+os.Getenv("PATH"))
			require.Error(t, err, string(out))

			intact := func(dir string) bool {
				_, err := os.Stat(dir)
				return err == nil && assert.ObjectsAreEqual(journal, readDir(t, dir))
			}
			assert.True(t, intact(d.src) || intact(d.dst), "journal lost after the crash: src=%v dst=%v", safeReadDir(t, d.src), safeReadDir(t, d.dst))

			out, err = runMigration(t, d)
			require.NoError(t, err, string(out))

			assert.Equal(t, journal, readDir(t, d.dst))
			assert.Empty(t, safeReadDir(t, d.src))
			assert.NoDirExists(t, d.moved)
		})
	}
}

func failingShim(t *testing.T, cmd string, failOn int) string {
	t.Helper()

	real, err := exec.LookPath(cmd)
	require.NoError(t, err)

	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	shim := `#!/bin/sh
n=$(($(cat "` + counter + `" 2>/dev/null || echo 0) + 1))
echo "$n" > "` + counter + `"
[ "$n" -eq ` + strconv.Itoa(failOn) + ` ] && exit 1
exec "` + real + `" "$@"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, cmd), []byte(shim), 0o755))
	return dir
}

func safeReadDir(t *testing.T, dir string) map[string]string {
	t.Helper()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return readDir(t, dir)
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
