package agent

import (
	"context"
	"log"
	"math/rand"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/restore"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

type Agent struct {
	pbm  *pbm.PBM
	node *pbm.Node
}

func New(pbm *pbm.PBM) *Agent {
	return &Agent{
		pbm: pbm,
	}
}

func (a *Agent) AddNode(ctx context.Context, cn *mongo.Client, curi string) {
	a.node = pbm.NewNode(ctx, "node0", cn, curi)
}

// Start starts listening the commands stream.
func (a *Agent) Start() error {
	c, cerr, err := a.pbm.ListenCmd()
	if err != nil {
		return err
	}

	for {
		select {
		case cmd := <-c:
			switch cmd.Cmd {
			case pbm.CmdBackup:
				log.Println("Got command", cmd.Cmd, cmd.Backup.Name)
				a.Backup(cmd.Backup)
			case pbm.CmdRestore:
				log.Println("Got command", cmd.Cmd, cmd.Restore.BackupName)
				a.Restore(cmd.Restore)
			case pbm.CmdResyncBackupList:
				log.Println("Got command", cmd.Cmd)
				a.ResyncBackupList()
			}
		case err := <-cerr:
			switch err.(type) {
			case pbm.ErrorCursor:
				return errors.Wrap(err, "stop listening")
			default:
				// channel closed / cursor is empty
				if err == nil {
					return errors.New("change stream was closed")
				}

				log.Println("[ERROR] listening commands:", err)
			}
		}
	}
}

// Backup starts backup
func (a *Agent) Backup(bcp pbm.BackupCmd) {
	q, err := backup.NodeSuits(bcp, a.node)
	if err != nil {
		log.Println("[ERROR] backup: node check:", err)
		return
	}

	// node is not suitable for doing backup
	if !q {
		log.Println("Node in not suitable for backup")
		return
	}

	nodeInfo, err := a.node.GetIsMaster()
	if err != nil {
		log.Println("[ERROR] backup: get node isMaster data:", err)
		return
	}

	// wait for a random time (1 to 100 ms) before acquiring a lock
	// TODO: do we need this? check
	time.Sleep(time.Duration(rand.Int63n(1e2)) * time.Millisecond)

	lock := a.pbm.NewLock(pbm.LockHeader{
		Type:       pbm.CmdBackup,
		Replset:    nodeInfo.SetName,
		Node:       nodeInfo.Me,
		BackupName: bcp.Name,
	})

	got, err := lock.Acquire()
	if err != nil {
		switch err.(type) {
		case pbm.ErrConcurrentOp:
			log.Println("[INFO] backup: acquiring lock:", err)
		default:
			log.Println("[ERROR] backup: acquiring lock:", err)
		}
		return
	}
	if !got {
		log.Println("Backup has been scheduled on another replset node")
		return
	}

	log.Printf("Backup %s started on node %s/%s", bcp.Name, nodeInfo.SetName, nodeInfo.Me)
	tstart := time.Now()
	err = backup.New(a.pbm, a.node).Run(bcp)
	if err != nil {
		log.Println("[ERROR] backup:", err)
	} else {
		log.Printf("Backup %s finished", bcp.Name)
	}

	// In the case of fast backup (small db) we have to wait before releasing the lock.
	// Otherwise, since the primary node waits for `WaitBackupStart*0.9` before trying to acquire the lock
	// it might happen that the backup will be made twice:
	//
	// secondary1 >---------*!lock(fail - acuired by s1)---------------------------
	// secondary2 >------*lock====backup====*unlock--------------------------------
	// primary    >--------*wait--------------------*lock====backup====*unlock-----
	//
	// Secondaries also may start trying to acquire a lock with quite an interval (e.g. due to network issues)
	// TODO: we cannot rely on the nodes wall clock.
	// TODO: ? pbmBackups should have unique index by name ?
	needToWait := pbm.WaitActionStart - time.Since(tstart)
	if needToWait > 0 {
		time.Sleep(needToWait)
	}
	err = lock.Release()
	if err != nil {
		log.Printf("[ERROR] backup: unable to release backup lock for %v:%v\n", lock, err)
	}
}

func (a *Agent) Restore(r pbm.RestoreCmd) {
	nodeInfo, err := a.node.GetIsMaster()
	if err != nil {
		log.Println("[ERROR] backup: get node isMaster data:", err)
		return
	}
	if !nodeInfo.IsMaster {
		log.Println("Node in not suitable for restore")
		return
	}

	lock := a.pbm.NewLock(pbm.LockHeader{
		Type:       pbm.CmdRestore,
		Replset:    nodeInfo.SetName,
		Node:       nodeInfo.Me,
		BackupName: r.Name,
	})

	got, err := lock.Acquire()

	if err != nil {
		log.Println("[ERROR] restore: acquiring lock:", err)
		return
	}
	if !got {
		log.Println("[ERROR] unbale to run the restore while another backup or restore process running")
		return
	}
	defer lock.Release()

	log.Printf("[INFO] Restore of '%s' started", r.BackupName)
	err = restore.New(a.pbm, a.node).Run(r)
	if err != nil {
		log.Println("[ERROR] restore:", err)
		return
	}
	log.Printf("[INFO] Restore of '%s' finished successfully", r.BackupName)
}

// ResyncBackupList uploads a backup list from the remote store
func (a *Agent) ResyncBackupList() {
	nodeInfo, err := a.node.GetIsMaster()
	if err != nil {
		log.Println("[ERROR] resync_list: get node isMaster data:", err)
		return
	}

	if !nodeInfo.IsLeader() {
		log.Println("[INFO] resync_list: not a memeber of the leader rs")
		return
	}

	lock := a.pbm.NewLock(pbm.LockHeader{
		Type:    pbm.CmdResyncBackupList,
		Replset: nodeInfo.SetName,
		Node:    nodeInfo.Me,
	})

	got, err := lock.Acquire()
	if err != nil {
		switch err.(type) {
		case pbm.ErrConcurrentOp:
			log.Println("[INFO] resync_list: acquiring lock:", err)
		default:
			log.Println("[ERROR] resync_list: acquiring lock:", err)
		}
		return
	}
	if !got {
		log.Println("[INFO] resync_list: operation has been scheduled on another replset node")
		return
	}

	tstart := time.Now()
	log.Println("[INFO] resync_list: started")
	err = a.pbm.ResyncBackupList()
	if err != nil {
		log.Println("[ERROR] resync_list:", err)
	} else {
		log.Println("[INFO] resync_list: succeed")
	}

	needToWait := time.Second*1 - time.Since(tstart)
	if needToWait > 0 {
		time.Sleep(needToWait)
	}
	err = lock.Release()
	if err != nil {
		log.Printf("[ERROR] backup: unable to release backup lock for %v:%v\n", lock, err)
	}
}
