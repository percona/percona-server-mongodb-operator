package sharded

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

func (c *Cluster) NetworkCut() {
	log.Println("peek a random replset")
	rs := ""
	for name := range c.shards {
		rs = name
		break
	}
	if rs == "" {
		log.Fatalln("no shards in cluster")
	}

	bcpName := c.LogicalBackup()

	log.Println("Cut network on agents", rs)
	err := c.docker.RunOnReplSet(rs, time.Second*5,
		"tc", "qdisc", "add", "dev", "eth0", "root", "netem", "loss", "100%",
	)
	if err != nil {
		log.Fatalf("ERROR: run tc netem on %s: %v", rs, err)
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
	log.Printf("Backup status %s/%s\n", meta.Status, meta.Error())

	log.Println("Restore network on agents", rs)
	err = c.docker.RunOnReplSet(rs, time.Second*5,
		"tc", "qdisc", "del", "dev", "eth0", "root",
	)
	if err != nil {
		log.Fatalf("ERROR: run tc netem on %s: %v", rs, err)
	}

	// TODO: currently needed not to stuck on huge replica. Should be removed
	// after fixing https://jira.percona.com/browse/PBM-406?focusedCommentId=248029&page=com.atlassian.jira.plugin.system.issuetabpanels%3Acomment-tabpanel#comment-248029
	//nolint:lll
	for rs := range c.shards {
		log.Println("Stopping agents on the replset", rs)
		err := c.docker.StopAgents(rs)
		if err != nil {
			log.Fatalln("ERROR: stopping agents on the replset", err)
		}
		log.Println("Agents has stopped", rs)
	}
	for rs := range c.shards {
		log.Println("Starting agents on the replset", rs)
		err = c.docker.StartAgents(rs)
		if err != nil {
			log.Fatalln("ERROR: starting agents on the replset", err)
		}
		log.Println("Agents started", rs)
	}

	log.Println("Sleeping for", waitfor)
	time.Sleep(waitfor)
}
