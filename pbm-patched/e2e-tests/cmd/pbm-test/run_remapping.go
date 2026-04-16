package main

import (
	"context"

	"github.com/percona/percona-backup-mongodb/e2e-tests/pkg/tests/sharded"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

func runRemappingTests(t *sharded.RemappingEnvironment) {
	storage := "/etc/pbm/minio.yaml"
	if !confExt(storage) {
		return
	}

	t.Donor.ApplyConfig(context.TODO(), storage)
	flush(t.Donor)
	t.Recipient.ApplyConfig(context.TODO(), storage)
	flush(t.Recipient)

	t.Donor.SetBallastData(1e4)

	runTest("Logical Backup & Restore with remapping Minio",
		func() { t.BackupAndRestore(defs.LogicalBackup) })

	flushStore(t.Recipient)
}
