package sharded

import (
	"context"
	"log"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

func (c *Cluster) BackupAndRestore(typ defs.BackupType) {
	backup := c.LogicalBackup
	restore := c.LogicalRestore
	if typ == defs.PhysicalBackup {
		backup = c.PhysicalBackup
		restore = c.PhysicalRestore
	}

	checkData := c.DataChecker()

	bcpName := backup()
	c.BackupWaitDone(context.TODO(), bcpName)
	c.DeleteBallast()

	// to be sure the backup didn't vanish after the resync
	// i.e. resync finished correctly
	log.Println("resync backup list")
	err := c.mongopbm.StoreResync(context.TODO())
	if err != nil {
		log.Fatalln("Error: resync backup lists:", err)
	}

	restore(context.TODO(), bcpName)
	checkData()
}
