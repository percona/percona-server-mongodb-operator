package sharded

import "log"

func (c *Cluster) BackupAndRestore() {
	checkData := c.DataChecker()

	bcpName := c.Backup()
	c.BackupWaitDone(bcpName)
	c.DeleteBallast()

	// to be sure the backup didn't vanish after the resync
	// i.e. resync finished correctly
	log.Println("resync backup list")
	err := c.mongopbm.StoreResync()
	if err != nil {
		log.Fatalln("resync backup lists:", err)
	}

	c.Restore(bcpName)
	checkData()
}
