package sharded

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
)

type backupDelete struct {
	name string
	ts   time.Time
}

func (c *Cluster) BackupDelete(storage string) {
	c.pitrOn()
	defer c.pitrOff()

	checkData := c.DataChecker()

	backups := make([]backupDelete, 5)
	for i := 0; i < len(backups); i++ {
		ts := time.Now()
		time.Sleep(1 * time.Second)
		c.printBcpList()
		bcpName := c.LogicalBackup()
		backups[i] = backupDelete{
			name: bcpName,
			ts:   ts,
		}
		c.BackupWaitDone(context.TODO(), bcpName)

		time.Sleep(time.Minute)
	}

	c.printBcpList()

	log.Println("delete backup", backups[4].name)
	_, err := c.pbm.RunCmd("pbm", "delete-backup", "-y", backups[4].name)
	if err != nil {
		log.Fatalf("ERROR: delete backup %s: %v", backups[4].name, err)
	}
	log.Println("wait for delete")
	err = c.mongopbm.WaitOp(context.TODO(), &lock.LockHeader{Type: ctrl.CmdDeleteBackup}, time.Minute*5)
	if err != nil {
		log.Fatalf("waiting for the delete: %v", err)
	}

	c.printBcpList()

	log.Printf("delete backups older than %s / %s \n", backups[3].name, backups[4].ts.Format("2006-01-02T15:04:05"))
	_, err = c.pbm.RunCmd("pbm", "delete-backup", "-y", "--older-than", backups[4].ts.Format("2006-01-02T15:04:05"))
	if err != nil {
		log.Fatalf("ERROR: delete backups older than %s: %v", backups[3].name, err)
	}
	log.Println("wait for delete")
	err = c.mongopbm.WaitOp(context.TODO(), &lock.LockHeader{Type: ctrl.CmdDeleteBackup}, time.Minute*5)
	if err != nil {
		log.Fatalf("waiting for the delete: %v", err)
	}
	time.Sleep(time.Second * 5)
	c.pitrOff()

	c.printBcpList()

	left := map[string]struct{}{
		backups[3].name: {}, // is a base for the pitr timeline, shouldn't be deleted
	}
	log.Println("should be only backups", left)
	checkArtefacts(storage, left)

	blist, err := c.mongopbm.BackupsList(context.TODO(), 0)
	if err != nil {
		log.Fatalln("ERROR: get backups list", err)
	}

	if len(blist) != len(left) {
		log.Fatalf("ERROR: backups list mismatch, expect: %v, have: %v", left, blist)
	}
	for _, b := range blist {
		if _, ok := left[b.Name]; !ok {
			log.Fatalf("ERROR: backup %s should be deleted", b.Name)
		}
	}

	list, err := c.pbm.List()
	if err != nil {
		log.Fatalf("ERROR: get backups/pitr list: %v", err)
	}

	if len(list.Snapshots) == 0 {
		log.Println("ERROR: empty spanpshots list")
		c.printBcpList()
		os.Exit(1)
	}

	tsp := time.Unix(list.Snapshots[len(list.Snapshots)-1].RestoreTS, 0).Add(time.Second * 10)

	log.Printf("delete pitr older than %s \n", tsp.Format("2006-01-02T15:04:05"))
	_, err = c.pbm.RunCmd("pbm", "delete-pitr", "-y", "--older-than", tsp.Format("2006-01-02T15:04:05"))
	if err != nil {
		log.Fatalf("ERROR: delete pitr older than %s: %v", tsp.Format("2006-01-02T15:04:05"), err)
	}
	log.Println("wait for delete-pitr")
	err = c.mongopbm.WaitOp(context.TODO(), &lock.LockHeader{Type: ctrl.CmdDeletePITR}, time.Minute*5)
	if err != nil {
		log.Fatalf("ERROR: waiting for the delete-pitr: %v", err)
	}

	list, err = c.pbm.List()
	if err != nil {
		log.Fatalf("ERROR: get backups/pitr list: %v", err)
	}

	if len(list.PITR.Ranges) == 0 {
		log.Fatalf("ERROR: empty pitr list, expected range after the last backup")
	}
	if len(list.Snapshots) == 0 {
		log.Fatalf("ERROR: empty spanpshots list")
	}

	if list.PITR.Ranges[0].Range.Start-1 != uint32(list.Snapshots[len(list.Snapshots)-1].RestoreTS) {
		log.Printf("ERROR: expected range starts the next second after the backup complete time, %v | %v",
			list.PITR.Ranges[0].Range.Start+1, uint32(list.Snapshots[len(list.Snapshots)-1].RestoreTS))
		c.printBcpList()
		os.Exit(1)
	}

	log.Println("delete pitr all")
	_, err = c.pbm.RunCmd("pbm", "delete-pitr", "-y", "--all")
	if err != nil {
		log.Fatalf("ERROR: delete all pitr: %v", err)
	}
	log.Println("wait for delete-pitr all")
	err = c.mongopbm.WaitOp(context.TODO(), &lock.LockHeader{Type: ctrl.CmdDeletePITR}, time.Minute*5)
	if err != nil {
		log.Fatalf("ERROR: waiting for the delete-pitr: %v", err)
	}

	list, err = c.pbm.List()
	if err != nil {
		log.Fatalf("ERROR: get backups/pitr list: %v", err)
	}

	if len(list.PITR.Ranges) != 0 {
		log.Println("ERROR: expected empty pitr list but got:")
		c.printBcpList()
		os.Exit(1)
	}

	log.Println("trying to restore from", backups[3])
	c.DeleteBallast()
	c.LogicalRestore(context.TODO(), backups[3].name)
	checkData()
}

// checkArtefacts checks if all backups artifacts removed
// except for the shouldStay
func checkArtefacts(conf string, shouldStay map[string]struct{}) {
	log.Println("check all artifacts deleted excepts backup's", shouldStay)

	files, err := listAllFiles(conf)
	if err != nil {
		log.Fatalln("ERROR: list files:", err)
	}

	for _, file := range files {
		if strings.Contains(file.Name, defs.StorInitFile) {
			continue
		}
		if strings.Contains(file.Name, defs.PITRfsPrefix) {
			continue
		}

		var ok bool
		for b := range shouldStay {
			if strings.Contains(file.Name, b) {
				ok = true
				break
			}
		}
		if !ok {
			log.Fatalln("ERROR: failed to delete lefover", file.Name)
		}
	}
}

func (c *Cluster) CannotRunDeleteDuringBackup() {
	bcpName := c.LogicalBackup()
	c.printBcpList()
	log.Println("deleting backup", bcpName)
	o, err := c.pbm.RunCmd("pbm", "delete-backup", "-y", bcpName)
	if err == nil || !strings.Contains(err.Error(), "another operation in progress, Snapshot backup") {
		list, lerr := c.pbm.RunCmd("pbm", "list")
		log.Fatalf("ERROR: running backup '%s' shouldn't be deleted.\n"+
			"Output: %s\nStderr:%v\nBackups list:\n%v\n%v",
			bcpName, o, err, list, lerr)
	}
	c.BackupWaitDone(context.TODO(), bcpName)
	time.Sleep(time.Second * 2)
}

func (c *Cluster) printBcpList() {
	listo, _ := c.pbm.RunCmd("pbm", "list", "--full")
	fmt.Printf("backup list:\n%s\n", listo)
}

func (c *Cluster) printBcpStatus() {
	listo, _ := c.pbm.RunCmd("pbm", "status")
	fmt.Printf("backup list:\n%s\n", listo)
}
