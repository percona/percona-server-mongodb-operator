package sharded

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

const (
	pbmLostAgentsErr = "some of pbm-agents were lost during the backup"
	pbmLostShardErr  = "lost shard"
)

// RestartAgents restarts agents during backup.
// Currently restarts agents on all shards. Also consider restarting
// only one shard and/or configsrv, but see https://jira.percona.com/browse/PBM-406?focusedCommentId=248029&page=com.atlassian.jira.plugin.system.issuetabpanels%3Acomment-tabpanel#comment-248029
//
//nolint:lll
func (c *Cluster) RestartAgents() {
	if len(c.shards) == 0 {
		log.Fatalln("no shards in cluster")
	}

	bcpName := c.LogicalBackup()

	for rs := range c.shards {
		log.Println("Stopping agents on the replset", rs)
		err := c.docker.StopAgents(rs)
		if err != nil {
			log.Fatalln("ERROR: stopping agents on the replset", err)
		}
		log.Println("Agents has stopped", rs)
	}

	waitfor := time.Duration(defs.StaleFrameSec+10) * time.Second
	log.Println("Sleeping for", waitfor)
	time.Sleep(waitfor)
	meta, err := c.mongopbm.GetBackupMeta(context.TODO(), bcpName)
	if err != nil {
		log.Fatalf("ERROR: get metadata for the backup %s: %v", bcpName, err)
	}

	if meta.Status != defs.StatusError ||
		meta.Error() == nil ||
		meta.Error().Error() != pbmLostAgentsErr &&
			!strings.Contains(meta.Error().Error(), pbmLostShardErr) {
		log.Fatalf("ERROR: wrong state of the backup %s. Expect: %s/%s|...%s... Got: %s/%s",
			bcpName, defs.StatusError, pbmLostAgentsErr, pbmLostShardErr, meta.Status, meta.Error())
	}

	for rs := range c.shards {
		log.Println("Starting agents on the replset", rs)
		err = c.docker.StartAgents(rs)
		if err != nil {
			log.Fatalln("ERROR: starting agents on the replset", err)
		}
		log.Println("Agents started", rs)
	}

	log.Printf("Sleeping for %v for agents to report status", time.Second*7)
	time.Sleep(time.Second * 7)
	log.Println("Trying a new backup")
	c.BackupAndRestore(defs.LogicalBackup)
}
