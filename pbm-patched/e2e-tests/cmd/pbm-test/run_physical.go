package main

import (
	"context"
	"math/rand"

	"github.com/percona/percona-backup-mongodb/e2e-tests/pkg/tests/sharded"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

func runPhysical(t *sharded.Cluster, typ testTyp) {
	cVersion := majmin(t.ServerVersion())

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

		t.ApplyConfig(context.TODO(), stg.conf)
		flush(t)

		t.SetBallastData(1e5)

		runTest("Physical Backup & Restore "+stg.name,
			func() { t.BackupAndRestore(defs.PhysicalBackup) })

		flushStore(t)
	}

	runTest("Physical Backup Data Bounds Check",
		func() { t.BackupBoundsCheck(defs.PhysicalBackup, cVersion) })

	runTest("Incremental Backup & Restore ",
		func() { t.IncrementalBackup(cVersion) })

	if typ == testsSharded {
		runTest("Physical Distributed Transactions backup",
			t.DistributedTrxPhysical)
	}

	runTest("Clock Skew Tests",
		func() { t.ClockSkew(defs.PhysicalBackup, cVersion) })

	flushStore(t)
}
