package sharded

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/util"
)

func (c *Cluster) BackupCancellation(storage string) {
	bcpName := c.LogicalBackup()
	ts := time.Now()
	log.Println("canceling backup", bcpName)
	o, err := c.pbm.RunCmd("pbm", "cancel-backup")
	if err != nil {
		log.Fatalf("Error: cancel backup '%s'.\nOutput: %s\nStderr:%v", bcpName, o, err)
	}

	time.Sleep(20 * time.Second)
	c.printBcpStatus()

	checkNoBackupFiles(bcpName, storage)

	log.Println("check backup state")
	m, err := c.mongopbm.GetBackupMeta(context.TODO(), bcpName)
	if err != nil {
		log.Fatalf("Error: get metadata for backup %s: %v", bcpName, err)
	}

	if m.Status != defs.StatusCancelled {
		log.Fatalf("Error: wrong backup status, expect %s, got %v", defs.StatusCancelled, m.Status)
	}

	needToWait := defs.WaitBackupStart + time.Second - time.Since(ts)
	if needToWait > 0 {
		log.Printf("waiting for the lock to be released for %s", needToWait)
		time.Sleep(needToWait)
	}
}

func checkNoBackupFiles(backupName, conf string) {
	log.Println("check no artifacts left for backup", backupName)

	files, err := listAllFiles(conf)
	if err != nil {
		log.Fatalln("ERROR: list files:", err)
	}

	for _, file := range files {
		if strings.Contains(file.Name, backupName) {
			log.Fatalln("ERROR: failed to delete lefover", file.Name)
		}
	}
}

func listAllFiles(confFilepath string) ([]storage.FileInfo, error) {
	buf, err := os.ReadFile(confFilepath)
	if err != nil {
		return nil, errors.Wrap(err, "read config file")
	}

	cfg, err := config.Parse(bytes.NewBuffer(buf))
	if err != nil {
		return nil, errors.Wrap(err, "parse config")
	}

	stg, err := util.StorageFromConfig(&cfg.Storage, "", nil)
	if err != nil {
		return nil, errors.Wrap(err, "storage from config")
	}

	files, err := stg.List("", "")
	if err != nil {
		return nil, errors.Wrap(err, "list files")
	}

	return files, nil
}
