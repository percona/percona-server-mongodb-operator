package sharded

import (
	"context"
	"log"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

// LeaderLag checks if cluster deals with leader lag during backup start
// https://jira.percona.com/browse/PBM-635
func (c *Cluster) LeaderLag() {
	checkData := c.DataChecker()

	log.Println("Pausing agents on", c.confsrv)
	err := c.docker.PauseAgents(c.confsrv)
	if err != nil {
		log.Fatalln("ERROR: pausing agents:", err)
	}
	log.Println("Agents on pause", c.confsrv)

	bcpName := time.Now().UTC().Format(time.RFC3339)

	log.Println("Starting backup", bcpName)
	err = c.mongopbm.SendCmd(context.Background(), ctrl.Cmd{
		Cmd: ctrl.CmdBackup,
		Backup: &ctrl.BackupCmd{
			Type:        defs.LogicalBackup,
			Name:        bcpName,
			Compression: compress.CompressionTypeS2,
		},
	})
	if err != nil {
		log.Fatalln("ERROR: starting backup:", err)
	}

	waitfor := time.Second * 5
	log.Println("Sleeping for", waitfor)
	time.Sleep(waitfor)

	log.Println("Unpausing agents on", c.confsrv)
	err = c.docker.UnpauseAgents(c.confsrv)
	if err != nil {
		log.Fatalln("ERROR: unpause agents:", err)
	}
	log.Println("Agents resumed", c.confsrv)

	c.BackupWaitDone(context.TODO(), bcpName)
	c.DeleteBallast()

	// to be sure the backup didn't vanish after the resync
	// i.e. resync finished correctly
	log.Println("resync backup list")
	err = c.mongopbm.StoreResync(context.TODO())
	if err != nil {
		log.Fatalln("Error: resync backup lists:", err)
	}

	c.LogicalRestore(context.TODO(), bcpName)
	checkData()
}
