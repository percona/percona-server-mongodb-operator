package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"

	"golang.org/x/mod/semver"

	"github.com/percona/percona-backup-mongodb/e2e-tests/pkg/tests/sharded"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

func run(t *sharded.Cluster, typ testTyp) {
	cVersion := majmin(t.ServerVersion())

	storage := "/etc/pbm/fs.yaml"
	// t.ApplyConfig(storage)
	// flush(t)

	remoteStg := []struct {
		name string
		conf string
	}{
		{"AWS", "/etc/pbm/aws.yaml"},
		{"GCS", "/etc/pbm/gcs.yaml"},
		{"GCS_HMAC", "/etc/pbm/gcs_hmac.yaml"},
		{"AWS_MinIO", "/etc/pbm/aws_minio.yaml"},
		{"Azure", "/etc/pbm/azure.yaml"},
		{"OSS", "/etc/pbm/oss.yaml"},
		{"FS", "/etc/pbm/fs.yaml"},
	}

	rand.Shuffle(len(remoteStg), func(i, j int) {
		remoteStg[i], remoteStg[j] = remoteStg[j], remoteStg[i]
	})

	minio := struct {
		name string
		conf string
	}{name: "Minio", conf: "/etc/pbm/minio.yaml"}
	remoteStg = append(remoteStg, minio)

	for _, stg := range remoteStg {
		if !confExt(stg.conf) {
			continue
		}

		storage = stg.conf

		t.ApplyConfig(context.TODO(), storage)
		flush(t)

		t.SetBallastData(1e5)

		runTest("Logical Backup & Restore "+stg.name,
			func() { t.BackupAndRestore(defs.LogicalBackup) })

		runTest("Logical PITR & Restore "+stg.name, t.PITRbasic)

		runTest("Oplog Replay "+stg.name, t.OplogReplay)

		t.SetBallastData(1e3)
		flush(t)

		runTest("Check Backups deletion "+stg.name,
			func() { t.BackupDelete(storage) })

		flushStore(t)
	}

	t.SetBallastData(1e5)

	runTest("Check the Cannot Run Delete During Backup",
		t.CannotRunDeleteDuringBackup)

	// Skip test for sharded envs until PBM-1446 is fixed
	if typ != testsSharded {
		runTest("Check Backup Cancellation",
			func() { t.BackupCancellation(storage) })
	}
	runTest("Leader lag during backup start",
		t.LeaderLag)

	runTest("Logical Backup Data Bounds Check",
		func() { t.BackupBoundsCheck(defs.LogicalBackup, cVersion) })

	if typ == testsSharded {
		t.SetBallastData(1e6)

		runTest("Selective restore in sharded cluster", t.SelectiveRestoreSharded)

		runTest("Selective backup in sharded cluster", t.SelectiveBackupSharded)

		// TODO: in the case of non-sharded cluster there is no other agent to observe
		// TODO: failed state during the backup. For such topology test should check if
		// TODO: a sequential run (of the backup let's say) handles a situation.
		runTest("Cut network during the backup",
			t.NetworkCut)

		t.SetBallastData(1e5)

		runTest("Restart agents during the backup",
			t.RestartAgents)

		runTest("Distributed Transactions backup",
			t.DistributedTrxSnapshot)

		runTest("Distributed Transactions PITR",
			t.DistributedTrxPITR)

		runTest("Cleaning up sharded database for full restore",
			t.CleanupFullRestore)

		// disttxnconf := "/etc/pbm/fs-disttxn-4x.yaml"
		// tsTo := primitive.Timestamp{1644410656, 8}

		//if semver.Compare(cVersion, "v5.0") >= 0 {
		//	disttxnconf = "/etc/pbm/fs-disttxn-50.yaml"
		//	tsTo = primitive.Timestamp{1644243375, 7}
		//}

		// t.ApplyConfig(context.TODO(), disttxnconf)
		// runTest("Distributed Transactions PITR",
		//	func() { t.DistributedCommit(tsTo) })

		t.ApplyConfig(context.TODO(), storage)
	}

	if semver.Compare(cVersion, "v5.0") >= 0 {
		t.SetBallastData(1e3)
		flush(t)

		runTest("Check timeseries",
			t.Timeseries)

		flush(t)
	}

	t.SetBallastData(1e5)

	runTest("Clock Skew Tests",
		func() { t.ClockSkew(defs.LogicalBackup, cVersion) })

	flushStore(t)
}

func runTest(name string, fn func()) {
	printStart(name)
	fn()
	printDone(name)
}

func printStart(name string) {
	log.Printf("[START] ======== %s ========\n", name)
}

func printDone(name string) {
	log.Printf("[DONE] ======== %s ========\n", name)
}

func flush(t *sharded.Cluster) {
	flushStore(t)
	flushPbm(t)
}

func flushPbm(t *sharded.Cluster) {
	err := t.Flush()
	if err != nil {
		log.Fatalln("Error: unable flush pbm db:", err)
	}
}

func flushStore(t *sharded.Cluster) {
	err := t.FlushStorage(context.TODO())
	if err != nil {
		log.Fatalln("Error: unable flush storage:", err)
	}
}

func confExt(f string) bool {
	_, err := os.Stat(f)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking config %s: %v\n", f, err)
		return false
	}

	return true
}

func majmin(v string) string {
	if len(v) == 0 {
		return v
	}

	if v[0] != 'v' {
		v = "v" + v
	}

	return semver.MajorMinor(v)
}
