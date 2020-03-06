package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
)

func backup(cn *pbm.PBM, bcpName, compression string) (string, error) {
	locks, err := cn.GetLocks(&pbm.LockHeader{})
	if err != nil {
		log.Println("get locks", err)
	}

	ts, err := cn.ClusterTime()
	if err != nil {
		return "", errors.Wrap(err, "read cluster time")
	}

	// Stop if there is some live operation.
	// But if there is some stale lock leave it for agents to deal with.
	for _, l := range locks {
		if l.Heartbeat.T+pbm.StaleFrameSec >= ts.T {
			return "", errors.Errorf("another operation in progress, %s/%s", l.Type, l.BackupName)
		}
	}

	stg, err := cn.GetStorage()
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return "", errors.New("no store set. Set remote store with <pbm store set>")
		}
		return "", errors.Wrap(err, "get remote-store")
	}

	err = cn.SendCmd(pbm.Cmd{
		Cmd: pbm.CmdBackup,
		Backup: pbm.BackupCmd{
			Name:        bcpName,
			Compression: pbm.CompressionType(compression),
		},
	})
	if err != nil {
		return "", errors.Wrap(err, "send command")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*20)
	defer cancel()
	err = waitForStatus(ctx, cn, bcpName)
	if err != nil {
		return "", err
	}

	storeString := ""
	switch stg.Type {
	case pbm.StorageS3:
		storeString = "s3://"
		if stg.S3.EndpointURL != "" {
			storeString += stg.S3.EndpointURL + "/"
		}
		storeString += stg.S3.Bucket
		if stg.S3.Prefix != "" {
			storeString += "/" + stg.S3.Prefix
		}
	case pbm.StorageFilesystem:
		storeString = stg.Filesystem.Path
	}
	return storeString, nil
}

func waitForStatus(ctx context.Context, cn *pbm.PBM, bcpName string) error {
	tk := time.NewTicker(time.Second * 1)
	defer tk.Stop()
	var err error
	bmeta := new(pbm.BackupMeta)
	for {
		select {
		case <-tk.C:
			fmt.Print(".")
			bmeta, err = cn.GetBackupMeta(bcpName)
			if err != nil {
				return errors.Wrap(err, "get backup metadata")
			}
			switch bmeta.Status {
			case pbm.StatusRunning, pbm.StatusDumpDone, pbm.StatusDone:
				return nil
			case pbm.StatusError:
				rs := ""
				for _, s := range bmeta.Replsets {
					rs += fmt.Sprintf("\n- Backup on replicaset \"%s\" in state: %v", s.Name, s.Status)
					if s.Error != "" {
						rs += ": " + s.Error
					}
				}
				return errors.New(bmeta.Error + rs)
			}
		case <-ctx.Done():
			rs := ""
			for _, s := range bmeta.Replsets {
				rs += fmt.Sprintf("- Backup on replicaset \"%s\" in state: %v\n", s.Name, s.Status)
				if s.Error != "" {
					rs += ": " + s.Error
				}
			}
			if rs == "" {
				rs = "<no replset has started backup>\n"
			}

			return errors.New("no confirmation that backup has successfully started. Replsets status:\n" + rs)
		}
	}
}

func printBackupList(cn *pbm.PBM, size int64) {
	bcps, err := cn.BackupsList(size)
	if err != nil {
		log.Fatalln("Error: unable to get backups list:", err)
	}
	fmt.Println("Backup history:")
	for _, b := range bcps {
		var bcp string
		switch b.Status {
		case pbm.StatusDone:
			bcp = b.Name
		case pbm.StatusError:
			bcp = fmt.Sprintf("%s\tFailed with \"%s\"", b.Name, b.Error)
		default:
			bcp, err = printBackupProgress(b, cn)
			if err != nil {
				log.Fatalf("Error: list backup %s: %v\n", b.Name, err)
			}
		}

		fmt.Println(" ", bcp)
	}
}

func printBackupProgress(b pbm.BackupMeta, pbmClient *pbm.PBM) (string, error) {
	locks, err := pbmClient.GetLocks(&pbm.LockHeader{
		Type:       pbm.CmdBackup,
		BackupName: b.Name,
	})

	if err != nil {
		return "", errors.Wrap(err, "get locks")
	}

	ts, err := pbmClient.ClusterTime()
	if err != nil {
		return "", errors.Wrap(err, "read cluster time")
	}

	stale := false
	staleMsg := "Stale: pbm-agents make no progress:"
	for _, l := range locks {
		if l.Heartbeat.T+pbm.StaleFrameSec < ts.T {
			stale = true
			staleMsg += fmt.Sprintf(" %s/%s [%s],", l.Replset, l.Node, time.Unix(int64(l.Heartbeat.T), 0).Format(time.RFC3339))
		}
	}

	if stale {
		return fmt.Sprintf("%s\t%s", b.Name, staleMsg[:len(staleMsg)-1]), nil
	}

	return fmt.Sprintf("%s\tIn progress [%s] (Launched at %s)", b.Name, b.Status, time.Unix(b.StartTS, 0).Format(time.RFC3339)), nil
}
