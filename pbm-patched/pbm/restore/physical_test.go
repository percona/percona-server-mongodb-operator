package restore

import (
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/log"
)

func TestNodeStatus(t *testing.T) {
	t.Run("isForCleanup", func(t *testing.T) {
		var progress nodeStatus

		if progress.isForCleanup() {
			t.Errorf("in initial progress phase, node is not for the cleanup")
		}

		progress |= restoreStared
		if !progress.isForCleanup() {
			t.Errorf("in point of no return phase cleanup should be done")
		}

		progress |= restoreDone
		if progress.isForCleanup() {
			t.Errorf("in done phase, node is not for the cleanup")
		}
	})

	t.Run("isFailed", func(t *testing.T) {
		var progress nodeStatus

		if !progress.isFailed() {
			t.Errorf("node is int initial progress phase, so it should be marked as failed")
		}

		progress |= restoreStared
		if !progress.isFailed() {
			t.Errorf("node is in started phase, so it should be marked as failed")
		}

		progress |= restoreDone
		if progress.isFailed() {
			t.Errorf("in done phase, node shouldn't be marked as failed")
		}
	})
}

func TestMoveAll(t *testing.T) {
	t.Run("move all files and dir", func(t *testing.T) {
		tempSrc, _ := os.MkdirTemp("", "src")
		defer os.RemoveAll(tempSrc)

		tempDst, _ := os.MkdirTemp("", "dst")
		defer os.RemoveAll(tempDst)

		testFiles := []string{"file1", "file2", "file3"}
		for i, file := range testFiles {
			_ = os.WriteFile(
				filepath.Join(tempSrc, file),
				[]byte(fmt.Sprintf("test content %d", i)), 0o644)
		}

		subDir := filepath.Join(tempSrc, "subdir")
		_ = os.Mkdir(subDir, 0o755)

		err := moveAll(tempSrc, tempDst, nil, log.DiscardLogger.NewDefaultEvent())
		if err != nil {
			t.Fatalf("moveAll failed: %v", err)
		}

		// files are moved
		for _, file := range testFiles {
			if _, err := os.Stat(filepath.Join(tempDst, file)); os.IsNotExist(err) {
				t.Errorf("file %s not found in destination directory", file)
			}
			if _, err := os.Stat(filepath.Join(tempSrc, file)); !os.IsNotExist(err) {
				t.Errorf("file %s still exists in source directory", file)
			}
		}

		// subdir is moved
		if _, err := os.Stat(filepath.Join(tempDst, "subdir")); os.IsNotExist(err) {
			t.Errorf("subdirectory not found in destination directory")
		}
		if _, err := os.Stat(filepath.Join(tempSrc, "subdir")); !os.IsNotExist(err) {
			t.Errorf("subdirectory still exists in source directory")
		}
	})

	t.Run("ignore files and dirs", func(t *testing.T) {
		tempSrc, _ := os.MkdirTemp("", "src")
		defer os.RemoveAll(tempSrc)

		tempDst, _ := os.MkdirTemp("", "dst")
		defer os.RemoveAll(tempDst)

		testFiles := []string{"file1", "file2", "ignore_me"}
		for i, file := range testFiles {
			_ = os.WriteFile(
				filepath.Join(tempSrc, file),
				[]byte(fmt.Sprintf("test content %d", i)), 0o644)
		}

		_ = os.Mkdir(filepath.Join(tempSrc, "ignore_dir"), 0o755)
		_ = os.Mkdir(filepath.Join(tempSrc, "normal_dir"), 0o755)

		toIgnore := []string{"ignore_me", "ignore_dir"}

		err := moveAll(tempSrc, tempDst, toIgnore, log.DiscardLogger.NewDefaultEvent())
		if err != nil {
			t.Fatalf("moveAll failed: %v", err)
		}

		// non-ignored files are moved
		movedFiles := []string{"file1", "file2"}
		for _, file := range movedFiles {
			if _, err := os.Stat(filepath.Join(tempDst, file)); os.IsNotExist(err) {
				t.Errorf("file %s not found in destination directory", file)
			}
		}

		// ignored items remain in source
		for _, item := range toIgnore {
			if _, err := os.Stat(filepath.Join(tempSrc, item)); os.IsNotExist(err) {
				t.Errorf("ignored item %s not found in source directory", item)
			}
			if _, err := os.Stat(filepath.Join(tempDst, item)); !os.IsNotExist(err) {
				t.Errorf("ignored item %s was moved to destination directory", item)
			}
		}

		// non-ignored directory is moved
		if _, err := os.Stat(filepath.Join(tempDst, "normal_dir")); os.IsNotExist(err) {
			t.Errorf("normal directory not found in destination directory")
		}
	})

	t.Run("source dir doesn't exist", func(t *testing.T) {
		tempDst, _ := os.MkdirTemp("", "dst")
		defer os.RemoveAll(tempDst)

		nonExistentDir := "/path/not/exist"

		err := moveAll(nonExistentDir, tempDst, nil, log.DiscardLogger.NewDefaultEvent())
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), "open dir") {
			t.Errorf("want:'open dir', got:%v", err)
		}
	})

	t.Run("write permission error", func(t *testing.T) {
		tempSrc, _ := os.MkdirTemp("", "src")
		defer os.RemoveAll(tempSrc)

		tempDst, _ := os.MkdirTemp("", "dst")
		defer os.RemoveAll(tempDst)

		_ = os.Chmod(tempDst, 0o400)

		// Create test file in source
		_ = os.WriteFile(
			filepath.Join(tempSrc, "test"),
			[]byte("test content"), 0o644)

		err := moveAll(tempSrc, tempDst, nil, log.DiscardLogger.NewDefaultEvent())
		if err == nil {
			t.Fatal("want perm error")
		}

		if !strings.Contains(err.Error(), "move test") {
			t.Errorf("want:'move test', got:%v", err)
		}
	})
}

func TestWaitMgoFreePort(t *testing.T) {
	t.Run("wait for the port a bit", func(t *testing.T) {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Fatalf("failed to bind port: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port

		portUsed := 5 * time.Second
		go func() {
			time.Sleep(portUsed)
			ln.Close()
		}()

		start := time.Now()
		err = waitMgoFreePort(port)
		duration := time.Since(start)

		if err != nil {
			t.Fatalf("error while waiting for the free port: %v", err)
		}
		if duration < portUsed-time.Second ||
			duration > portUsed+2*time.Second {
			t.Fatalf("wrong duration time, want~=%v, got=%v", portUsed, duration)
		}
	})

	t.Run("sequential check", func(t *testing.T) {
		var err error
		for i := range 10 {
			start := time.Now()
			err = waitMgoFreePort(8088)
			if err != nil {
				t.Fatalf("free port err: %v", err)
			}
			t.Logf("finish %d round, with duration %s", i+1, time.Since(start))
		}
	})
}

func TestResolveCleanupStrategy(t *testing.T) {
	cmpStrategy := func(fn any) uintptr {
		return reflect.ValueOf(fn).Pointer()
	}
	type strategyDesc string
	const (
		fallbackStrategy    strategyDesc = "fallback"
		fullCleanupStrategy strategyDesc = "fullCleanup"
		skipCleanup         strategyDesc = "skipCleanup"

		nodeUntouched nodeStatus = 0
		nodeTouched   nodeStatus = 1
	)

	testCases := []struct {
		desc            string
		fallback        bool
		allowPartlyDone bool
		clusterStatus   defs.Status
		nodeStatus      nodeStatus
		wantStrategy    strategyDesc
	}{
		// fallback enabled
		{
			desc:            "fallback: enabled, allow-partly-done: enabled, status: error",
			fallback:        true,
			allowPartlyDone: true,
			clusterStatus:   defs.StatusError,
			nodeStatus:      nodeTouched,
			wantStrategy:    fallbackStrategy,
		},
		{
			desc:            "fallback: enabled, allow-partly-done: enabled, status: partly-done",
			fallback:        true,
			allowPartlyDone: true,
			clusterStatus:   defs.StatusPartlyDone,
			nodeStatus:      nodeTouched,
			wantStrategy:    fullCleanupStrategy,
		},
		{
			desc:            "fallback: enabled, allow-partly-done: enabled, status: done",
			fallback:        true,
			allowPartlyDone: true,
			clusterStatus:   defs.StatusDone,
			nodeStatus:      nodeTouched,
			wantStrategy:    skipCleanup,
		},

		{
			desc:            "fallback: enabled, allow-partly-done: disabled, status: error",
			fallback:        true,
			allowPartlyDone: false,
			clusterStatus:   defs.StatusError,
			nodeStatus:      nodeTouched,
			wantStrategy:    fallbackStrategy,
		},
		{
			desc:            "fallback: enabled, allow-partly-done: disabled, status: partly-done",
			fallback:        true,
			allowPartlyDone: false,
			clusterStatus:   defs.StatusPartlyDone,
			nodeStatus:      nodeTouched,
			wantStrategy:    fallbackStrategy,
		},
		{
			desc:            "fallback: enabled, allow-partly-done: disabled, status: done",
			fallback:        true,
			allowPartlyDone: false,
			clusterStatus:   defs.StatusDone,
			nodeStatus:      nodeTouched,
			wantStrategy:    skipCleanup,
		},

		// fallback disabled
		{
			desc:            "fallback: disabled, allow-partly-done: enabled, status: error",
			fallback:        false,
			allowPartlyDone: true,
			clusterStatus:   defs.StatusError,
			nodeStatus:      nodeTouched,
			wantStrategy:    fullCleanupStrategy,
		},
		{
			desc:            "fallback: disabled, allow-partly-done: enabled, status: partly-done",
			fallback:        false,
			allowPartlyDone: true,
			clusterStatus:   defs.StatusPartlyDone,
			nodeStatus:      nodeTouched,
			wantStrategy:    fullCleanupStrategy,
		},
		{
			desc:            "fallback: disabled, allow-partly-done: enabled, status: done",
			fallback:        false,
			allowPartlyDone: true,
			clusterStatus:   defs.StatusDone,
			nodeStatus:      nodeTouched,
			wantStrategy:    skipCleanup,
		},
		{
			desc:            "fallback: disabled, allow-partly-done: disabled, status: error",
			fallback:        false,
			allowPartlyDone: false,
			clusterStatus:   defs.StatusError,
			nodeStatus:      nodeTouched,
			wantStrategy:    fullCleanupStrategy,
		},
		{
			desc:            "fallback: disabled, allow-partly-done: disabled, status: partly-done",
			fallback:        false,
			allowPartlyDone: false,
			clusterStatus:   defs.StatusPartlyDone,
			nodeStatus:      nodeTouched,
			wantStrategy:    fullCleanupStrategy,
		},
		{
			desc:            "fallback: disabled, allow-partly-done: disabled, status: done",
			fallback:        false,
			allowPartlyDone: false,
			clusterStatus:   defs.StatusDone,
			nodeStatus:      nodeTouched,
			wantStrategy:    skipCleanup,
		},

		// db path is untouched
		{
			desc:            "fallback: enabled, allow-partly-done: enabled, status: error",
			fallback:        true,
			allowPartlyDone: true,
			clusterStatus:   defs.StatusError,
			nodeStatus:      nodeUntouched,
			wantStrategy:    skipCleanup,
		},
		{
			desc:            "fallback: disabled, allow-partly-done: enabled, status: error",
			fallback:        false,
			allowPartlyDone: true,
			clusterStatus:   defs.StatusError,
			nodeStatus:      nodeUntouched,
			wantStrategy:    skipCleanup,
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			r := &PhysRestore{
				fallback:        tC.fallback,
				allowPartlyDone: tC.allowPartlyDone,
				log:             log.DiscardLogger.NewDefaultEvent(),
			}
			strategy := r.resolveCleanupStrategy(tC.clusterStatus, tC.nodeStatus)
			if tC.wantStrategy == fullCleanupStrategy {
				if cmpStrategy(strategy) != cmpStrategy(r.doFullCleanup) {
					t.Fatalf("want=%s", fullCleanupStrategy)
				}
			} else if tC.wantStrategy == fallbackStrategy {
				if cmpStrategy(strategy) != cmpStrategy(r.doFallbackCleanup) {
					t.Fatalf("want=%s", fallbackStrategy)
				}
			} else if tC.wantStrategy == skipCleanup {
				if cmpStrategy(strategy) != cmpStrategy(r.skipCleanup) {
					t.Fatalf("want=%s", skipCleanup)
				}
			}
		})
	}
}

func TestRemoveAll(t *testing.T) {
	t.Run("removes all files in dir", func(t *testing.T) {
		tmpDir := setupTestFiles(t)

		err := removeAll(tmpDir, log.DiscardEvent)
		if err != nil {
			t.Fatalf("got error when removing all files, err=%v", err)
		}

		files := readDir(t, tmpDir)
		if len(files) != 0 {
			t.Fatalf("dir should be empty, got=%d files", len(files))
		}
	})

	t.Run("skipping internal mongod log files", func(t *testing.T) {
		tmpDir := setupTestFiles(t)

		err := removeAll(tmpDir, log.DiscardEvent, getInternalLogFileSkipRule())
		if err != nil {
			t.Fatalf("got error when removing all files, err=%v", err)
		}

		files := readDir(t, tmpDir)
		if len(files) != 3 {
			t.Fatalf("expected to have 3 log files, got=%d files, names=%s", len(files), files)
		}
	})

	t.Run("skipping fallback dir", func(t *testing.T) {
		tmpDir := setupTestFiles(t)

		err := removeAll(tmpDir, log.DiscardEvent, getFallbackSyncFileSkipRule())
		if err != nil {
			t.Fatalf("got error when removing all files, err=%v", err)
		}

		files := readDir(t, tmpDir)
		if len(files) != 1 || files[0] != fallbackDir {
			t.Fatalf("expected to have fallback dir, got=%d files/dirs, names=%s", len(files), files)
		}

		files = readDir(t, path.Join(tmpDir, fallbackDir))
		if len(files) != 1 || files[0] != "file.fallback" {
			t.Fatalf("expected to have 1 file within fallback dir, got=%d files/dirs, names=%s", len(files), files)
		}
	})

	t.Run("skipping all pbm related", func(t *testing.T) {
		tmpDir := setupTestFiles(t)

		err := removeAll(tmpDir, log.DiscardEvent, getFallbackSyncFileSkipRule(), getInternalLogFileSkipRule())
		if err != nil {
			t.Fatalf("got error when removing all files, err=%v", err)
		}

		files := readDir(t, tmpDir)
		if len(files) != 4 {
			t.Fatalf("expected to have fallback dir and mongod log file, got=%d files/dirs, names=%s", len(files), files)
		}
	})
}

func TestRemoveInternalMongoLogs(t *testing.T) {
	tmpDir := setupTestFiles(t)

	err := removeInternalMongoLogs(tmpDir, log.DiscardEvent)
	if err != nil {
		t.Fatalf("got error when removing internal mongod logs, err=%v", err)
	}

	files := readDir(t, tmpDir)
	if len(files) != 6 {
		t.Fatalf("only mongod log files should be removed, expected 6 files, got=%d files", len(files))
	}
	for _, f := range files {
		if strings.HasPrefix(f, internalMongodLog) {
			t.Fatalf("internal mongod log file is not deleted: %s", f)
		}
	}
}

func readDir(t *testing.T, dir string) []string {
	t.Helper()

	d, err := os.Open(dir)
	if err != nil {
		t.Fatalf("failing opening dir, err=%v", err)
	}
	defer d.Close()

	files, err := d.Readdirnames(-1)
	if err != nil {
		t.Fatalf("failing reading dir, err=%v", err)
	}

	return files
}

func setupTestFiles(t *testing.T) string {
	tmpDir, err := os.MkdirTemp("", "phys-test-*")
	if err != nil {
		t.Fatalf("error while creating setup files: %v", err)
	}

	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})

	// tmpDir/
	//   - file1.txt
	//   - file2.log
	//   - file3.txt.tmp
	//   - subdir/
	//     - file4.txt
	//     - file5.log
	//     - file6.txt.tmp
	//   - empty/
	//   - pbm.restore.log
	//   - pbm.restore.log.2025-05-15T14-33-34
	//   - pbm.restore.log.2025-05-15T14-37-34
	//   - .fallbacksync/
	//     - file.fs.txt
	createTestFile(t, filepath.Join(tmpDir, "file1.txt"), "content1")
	createTestFile(t, filepath.Join(tmpDir, "file2.log"), "content2")
	createTestFile(t, filepath.Join(tmpDir, "file3.txt.tmp"), "content3")

	createTestDir(t, filepath.Join(tmpDir, "subdir"))
	createTestFile(t, filepath.Join(tmpDir, "subdir", "file4.txt"), "content4")
	createTestFile(t, filepath.Join(tmpDir, "subdir", "file5.log"), "content5")
	createTestFile(t, filepath.Join(tmpDir, "subdir", "file6.txt.tmp"), "content6")

	createTestDir(t, filepath.Join(tmpDir, "empty"))

	// files & dirs with additional semantic:
	createTestFile(t, filepath.Join(tmpDir, "pbm.restore.log"), "log-content-1")
	createTestFile(t, filepath.Join(tmpDir, "pbm.restore.log.2025-05-15T14-33-34"), "log-content-2")
	createTestFile(t, filepath.Join(tmpDir, "pbm.restore.log.2025-05-15T14-37-34"), "log-content-3")

	createTestDir(t, filepath.Join(tmpDir, fallbackDir))
	createTestFile(t, filepath.Join(tmpDir, fallbackDir, "file.fallback"), "content")

	return tmpDir
}

func createTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("error while creating file %s: %v", path, err)
	}
}

func createTestDir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("error while creating dir %s: %v", path, err)
	}
}
