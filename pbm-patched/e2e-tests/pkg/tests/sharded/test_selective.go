package sharded

import (
	"context"
	"log"
	"strings"

	"github.com/percona/percona-backup-mongodb/e2e-tests/pkg/tests"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

var clusterSpec = []tests.GenDBSpec{
	{
		Name: "db0",
		Collections: []tests.GenCollSpec{
			{
				Name: "c00",
			},
			{
				Name: "c01",
				ShardingKey: &tests.ShardingOptions{
					Key: map[string]any{"i": "hashed"},
				},
			},
			{
				Name: "c02",
			},
			{
				Name: "c03",
				ShardingKey: &tests.ShardingOptions{
					Key: map[string]any{"i": "hashed"},
				},
			},
		},
	},
	{
		Name: "db1",
		Collections: []tests.GenCollSpec{
			{
				Name: "c10",
				ShardingKey: &tests.ShardingOptions{
					Key: map[string]any{"r": "hashed"},
				},
			},
			{
				Name: "c11",
			},
		},
	},
}

func (c *Cluster) SelectiveRestoreSharded() {
	ctx, mongos := c.ctx, c.mongos.Conn()
	creds := tests.ExtractCredentionals(c.cfg.Mongos)

	defer func() {
		for _, db := range clusterSpec {
			if err := mongos.Database(db.Name).Drop(ctx); err != nil {
				log.Printf("drop database: %s", err.Error())
			}
		}
	}()

	err := tests.Deploy(ctx, mongos, clusterSpec)
	if err != nil {
		log.Printf("deploy: %s", err.Error())
		return
	}

	err = tests.GenerateData(ctx, mongos, clusterSpec)
	if err != nil {
		log.Printf("generate data (1): %s", err.Error())
		return
	}

	beforeState, err := tests.ClusterState(ctx, mongos, creds)
	if err != nil {
		log.Printf("get before cluster state: %s", err.Error())
		return
	}

	backupName := c.backup(defs.LogicalBackup)
	c.BackupWaitDone(context.TODO(), backupName)

	// regenerate new data
	err = tests.GenerateData(ctx, mongos, clusterSpec)
	if err != nil {
		log.Printf("generate data (2): %s", err.Error())
		return
	}

	selected := []string{"db0.c00", "db0.c01", "db1.*"}
	c.LogicalRestoreWithParams(context.TODO(), backupName, []string{"--ns", strings.Join(selected, ","), "--wait"})

	afterState, err := tests.ClusterState(ctx, mongos, creds)
	if err != nil {
		log.Printf("get after cluster state: %s", err.Error())
		return
	}

	if !tests.Compare(beforeState, afterState, selected) {
		log.Println("unexpected restored state")
		return
	}

	log.Printf("Deleting backup %v", backupName)
	err = c.mongopbm.DeleteBackup(context.TODO(), backupName)
	if err != nil {
		log.Fatalf("Error: delete backup %s: %v", backupName, err)
	}
}

func (c *Cluster) SelectiveBackupSharded() {
	ctx, mongos := c.ctx, c.mongos.Conn()
	creds := tests.ExtractCredentionals(c.cfg.Mongos)

	defer func() {
		for _, db := range clusterSpec {
			if err := mongos.Database(db.Name).Drop(ctx); err != nil {
				log.Printf("drop database: %s", err.Error())
			}
		}
	}()

	err := tests.Deploy(ctx, mongos, clusterSpec)
	if err != nil {
		log.Printf("deploy: %s", err.Error())
		return
	}

	err = tests.GenerateData(ctx, mongos, clusterSpec)
	if err != nil {
		log.Printf("generate data (1): %s", err.Error())
		return
	}

	beforeState, err := tests.ClusterState(ctx, mongos, creds)
	if err != nil {
		log.Printf("get before cluster state: %s", err.Error())
		return
	}

	backupName := c.backup(defs.LogicalBackup, "--ns", "db0.*")
	c.BackupWaitDone(context.TODO(), backupName)

	// regenerate new data
	err = tests.GenerateData(ctx, mongos, clusterSpec)
	if err != nil {
		log.Printf("generate data (2): %s", err.Error())
		return
	}

	selected := []string{"db0.c00", "db0.c01"}
	c.LogicalRestoreWithParams(context.TODO(), backupName, []string{"--ns", strings.Join(selected, ","), "--wait"})

	afterState, err := tests.ClusterState(ctx, mongos, creds)
	if err != nil {
		log.Printf("get after cluster state: %s", err.Error())
		return
	}

	if !tests.Compare(beforeState, afterState, selected) {
		log.Println("unexpected restored state")
		return
	}

	log.Printf("Deleting backup %v", backupName)
	err = c.mongopbm.DeleteBackup(context.TODO(), backupName)
	if err != nil {
		log.Fatalf("Error: delete backup %s: %v", backupName, err)
	}
}
