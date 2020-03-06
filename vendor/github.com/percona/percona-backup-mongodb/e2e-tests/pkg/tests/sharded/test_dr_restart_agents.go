package sharded

import (
	"log"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
)

const pbmLostAgentsErr = "some pbm-agents were lost during the backup"

// RestartAgents restarts agents during backup.
// Currently restarts agents on all shards. Also consider restarting
//  only one shard and/or configsrv, but see https://jira.percona.com/browse/PBM-406?focusedCommentId=248029&page=com.atlassian.jira.plugin.system.issuetabpanels%3Acomment-tabpanel#comment-248029
func (c *Cluster) RestartAgents() {
	if len(c.shards) == 0 {
		log.Fatalln("no shards in cluster")
	}

	bcpName := c.Backup()

	for rs := range c.shards {
		log.Println("Stopping agents on the replset", rs)
		err := c.docker.StopAgents(rs)
		if err != nil {
			log.Fatalln("ERROR: stopping agents on the replset", err)
		}
		log.Println("Agents has stopped", rs)
	}

	waitfor := time.Duration(pbm.StaleFrameSec+10) * time.Second
	log.Println("Sleeping for", waitfor)
	time.Sleep(waitfor)
	meta, err := c.mongopbm.GetBackupMeta(bcpName)
	if err != nil {
		log.Fatalf("ERROR: get metadata for the backup %s: %v", bcpName, err)
	}

	if meta.Status != pbm.StatusError && meta.Error != pbmLostAgentsErr {
		log.Fatalf("ERROR: wrong state of the backup %s. Expect: %s/%s. Got: %s/%s", bcpName, pbm.StatusError, pbmLostAgentsErr, meta.Status, meta.Error)
	}

	for rs := range c.shards {
		log.Println("Starting agents on the replset", rs)
		err = c.docker.StartAgents(rs)
		if err != nil {
			log.Fatalln("ERROR: starting agents on the replset", err)
		}
		log.Println("Agents started", rs)
	}

	log.Println("Trying a new backup")
	c.BackupAndRestore()
}
