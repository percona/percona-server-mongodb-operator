package restore

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/mongodb/mongo-tools-common/db"
	"github.com/mongodb/mongo-tools-common/options"
	"github.com/mongodb/mongo-tools/mongorestore"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm"
)

var excludeFromDumpRestore = []string{
	pbm.DB + "." + pbm.CmdStreamCollection,
	pbm.DB + "." + pbm.LogCollection,
	pbm.DB + "." + pbm.ConfigCollection,
	pbm.DB + "." + pbm.BcpCollection,
	pbm.DB + "." + pbm.RestoresCollection,
	pbm.DB + "." + pbm.LockCollection,
	"config.version",
	"config.mongos",
}

type Restore struct {
	cn     *pbm.PBM
	node   *pbm.Node
	name   string
	backup string
}

// New creates a new restore object
func New(cn *pbm.PBM, node *pbm.Node) *Restore {
	return &Restore{
		cn:   cn,
		node: node,
	}
}

func (r *Restore) Run(cmd pbm.RestoreCmd) error {
	stg, err := r.cn.GetStorage()
	if err != nil {
		return errors.Wrap(err, "get backup store")
	}

	bcp, err := r.cn.GetBackupMeta(cmd.BackupName)
	if errors.Cause(err) == mongo.ErrNoDocuments {
		bcp, err = getMetaFromStore(cmd.BackupName, stg)
	}
	if err != nil {
		return errors.Wrap(err, "get backup metadata")
	}

	if bcp.Status != pbm.StatusDone {
		return errors.Errorf("backup wasn't successfull: status: %s, error: %s", bcp.Status, bcp.Error)
	}

	im, err := r.node.GetIsMaster()
	if err != nil {
		return errors.Wrap(err, "get isMaster data")
	}

	rsName := im.SetName
	if rsName == "" {
		rsName = pbm.NoReplset
	}

	var (
		rsBackup pbm.BackupReplset
		ok       bool
	)
	for _, v := range bcp.Replsets {
		if v.Name == rsName {
			rsBackup = v
			ok = true
		}
	}
	if !ok {
		return errors.Errorf("metadata for replset/shard %s is not found", rsName)
	}

	meta := &pbm.RestoreMeta{
		Name:     cmd.Name,
		Backup:   cmd.BackupName,
		StartTS:  time.Now().Unix(),
		Status:   pbm.StatusStarting,
		Replsets: []pbm.RestoreReplset{},
	}
	if im.IsLeader() {
		err = r.cn.SetRestoreMeta(meta)
		if err != nil {
			return errors.Wrap(err, "write backup meta to db")
		}
		hbstop := make(chan struct{})
		defer close(hbstop)
		go func() {
			tk := time.NewTicker(time.Second * 5)
			defer tk.Stop()
			for {
				select {
				case <-tk.C:
					err := r.cn.RestoreHB(cmd.Name)
					if err != nil {
						log.Println("[ERROR] send pbm heartbeat:", err)
					}
				case <-hbstop:
					return
				}
			}
		}()
	}

	// Waiting for StatusStarting to move further.
	// In case some preparations has to be done before backup.
	err = r.waitForStatus(cmd.Name, pbm.StatusStarting)
	if err != nil {
		return errors.Wrap(err, "waiting for start")
	}

	rsMeta := pbm.RestoreReplset{
		Name:       rsName,
		StartTS:    time.Now().UTC().Unix(),
		Status:     pbm.StatusRunning,
		Conditions: []pbm.Condition{},
	}

	defer func() {
		if err != nil {
			ferr := r.MarkFailed(cmd.Name, rsMeta.Name, err.Error())
			log.Printf("Mark restore as failed `%v`: %v\n", err, ferr)
		}
	}()

	rsMeta.Status = pbm.StatusRunning
	err = r.cn.AddRestoreRSMeta(cmd.Name, rsMeta)
	if err != nil {
		return errors.Wrap(err, "add shard's metadata")
	}

	if im.IsLeader() {
		err = r.reconcileStatus(cmd.Name, pbm.StatusRunning, im, &pbm.WaitActionStart)
		if err != nil {
			if errors.Cause(err) == errConvergeTimeOut {
				return errors.Wrap(err, "couldn't get response from all shards")
			}
			return errors.Wrap(err, "check cluster for restore started")
		}
	}

	err = r.waitForStatus(cmd.Name, pbm.StatusRunning)
	if err != nil {
		return errors.Wrap(err, "waiting for start")
	}

	dumpReader, dumpCloser, err := Source(stg, rsBackup.DumpName, pbm.CompressionTypeNone) //, bcp.Compression)
	if err != nil {
		return errors.Wrap(err, "create source object for the dump restore")
	}
	defer func() {
		dumpReader.Close()
		if dumpCloser != nil {
			dumpCloser.Close()
		}
	}()

	ver, err := r.node.GetMongoVersion()
	if err != nil || len(ver.Version) < 1 {
		return errors.Wrap(err, "define mongo version")
	}
	preserveUUID := true
	if ver.Version[0] < 4 {
		preserveUUID = false
	}

	topts := options.ToolOptions{
		AppName:    "mongodump",
		VersionStr: "0.0.1",
		URI:        &options.URI{ConnectionString: r.node.ConnURI()},
		Auth:       &options.Auth{},
		Namespace:  &options.Namespace{},
		Connection: &options.Connection{},
		Direct:     true,
	}

	rsession, err := db.NewSessionProvider(topts)
	if err != nil {
		return errors.Wrap(err, "create session for the dump restore")
	}

	mr := mongorestore.MongoRestore{
		SessionProvider: rsession,
		ToolOptions:     &topts,
		InputOptions: &mongorestore.InputOptions{
			Gzip:    bcp.Compression == pbm.CompressionTypeGZIP,
			Archive: "-",
		},
		OutputOptions: &mongorestore.OutputOptions{
			BulkBufferSize:           2000,
			BypassDocumentValidation: true,
			Drop:                     true,
			NumInsertionWorkers:      20,
			NumParallelCollections:   1,
			PreserveUUID:             preserveUUID,
			StopOnError:              true,
			TempRolesColl:            "temproles",
			TempUsersColl:            "tempusers",
			WriteConcern:             "majority",
		},
		NSOptions: &mongorestore.NSOptions{
			NSExclude: excludeFromDumpRestore,
		},
		InputReader: dumpReader,
	}

	rdumpResult := mr.Restore()
	if rdumpResult.Err != nil {
		return errors.Wrapf(rdumpResult.Err, "restore mongo dump (successes: %d / fails: %d)", rdumpResult.Successes, rdumpResult.Failures)
	}
	mr.Close()

	err = r.cn.ChangeRestoreRSState(cmd.Name, rsMeta.Name, pbm.StatusDumpDone, "")
	if err != nil {
		return errors.Wrap(err, "set shard's StatusDumpDone")
	}
	log.Println("mongorestore finished")

	if im.IsLeader() {
		err = r.reconcileStatus(cmd.Name, pbm.StatusDumpDone, im, nil)
		if err != nil {
			return errors.Wrap(err, "check cluster for restore dump done")
		}
	}

	err = r.waitForStatus(cmd.Name, pbm.StatusDumpDone)
	if err != nil {
		return errors.Wrap(err, "waiting for start")
	}

	log.Println("starting the oplog replay")

	oplogReader, oplogCloser, err := Source(stg, rsBackup.OplogName, bcp.Compression)
	if err != nil {
		return errors.Wrap(err, "create source object for the oplog restore")
	}
	defer func() {
		oplogReader.Close()
		if oplogCloser != nil {
			oplogCloser.Close()
		}
	}()

	err = NewOplog(r.node, ver, preserveUUID).Apply(oplogReader)

	err = r.cn.ChangeRestoreRSState(cmd.Name, rsMeta.Name, pbm.StatusDone, "")
	if err != nil {
		return errors.Wrap(err, "set shard's StatusDone")
	}

	if im.IsLeader() {
		err = r.reconcileStatus(cmd.Name, pbm.StatusDone, im, nil)
		if err != nil {
			return errors.Wrap(err, "check cluster for the restore done")
		}
	}

	return nil
}

func (r *Restore) reconcileStatus(name string, status pbm.Status, im *pbm.IsMaster, timeout *time.Duration) error {
	shards := []pbm.Shard{
		{
			ID:   im.SetName,
			Host: im.SetName + "/" + strings.Join(im.Hosts, ","),
		},
	}

	if im.IsSharded() {
		s, err := r.cn.GetShards()
		if err != nil {
			return errors.Wrap(err, "get shards list")
		}
		shards = append(shards, s...)
	}

	if timeout != nil {
		return errors.Wrap(r.convergeClusterWithTimeout(name, shards, status, *timeout), "convergeClusterWithTimeout")
	}
	return errors.Wrap(r.convergeCluster(name, shards, status), "convergeCluster")
}

// convergeCluster waits until all given shards reached `status` and updates a cluster status
func (r *Restore) convergeCluster(name string, shards []pbm.Shard, status pbm.Status) error {
	tk := time.NewTicker(time.Second * 1)
	defer tk.Stop()
	for {
		select {
		case <-tk.C:
			ok, err := r.converged(name, shards, status)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		case <-r.cn.Context().Done():
			return nil
		}
	}
}

var errConvergeTimeOut = errors.New("reached converge timeout")

// convergeClusterWithTimeout waits up to the geiven timeout until all given shards reached `status` and then updates the cluster status
func (r *Restore) convergeClusterWithTimeout(name string, shards []pbm.Shard, status pbm.Status, t time.Duration) error {
	tk := time.NewTicker(time.Second * 1)
	defer tk.Stop()
	tout := time.NewTicker(t)
	defer tout.Stop()
	for {
		select {
		case <-tk.C:
			ok, err := r.converged(name, shards, status)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		case <-tout.C:
			return errConvergeTimeOut
		case <-r.cn.Context().Done():
			return nil
		}
	}
}

func (r *Restore) converged(name string, shards []pbm.Shard, status pbm.Status) (bool, error) {
	shardsToFinish := len(shards)
	bmeta, err := r.cn.GetRestoreMeta(name)
	if err != nil {
		return false, errors.Wrap(err, "get backup metadata")
	}

	clusterTime, err := r.cn.ClusterTime()
	if err != nil {
		return false, errors.Wrap(err, "read cluster time")
	}

	for _, sh := range shards {
		for _, shard := range bmeta.Replsets {
			if shard.Name == sh.ID {
				// check if node alive
				lock, err := r.cn.GetLockData(&pbm.LockHeader{
					Type:       pbm.CmdRestore,
					BackupName: name,
					Replset:    shard.Name,
				})

				// nodes are cleaning its locks moving to the done status
				// so no lock is ok and not need to ckech the heartbeats
				if status != pbm.StatusDone && err != mongo.ErrNoDocuments {
					if err != nil {
						return false, errors.Wrapf(err, "unable to read lock for shard %s", shard.Name)
					}
					if lock.Heartbeat.T+pbm.StaleFrameSec < clusterTime.T {
						return false, errors.Errorf("lost shard %s, last beat ts: %d", shard.Name, lock.Heartbeat.T)
					}
				}

				// check status
				switch shard.Status {
				case status:
					shardsToFinish--
				case pbm.StatusError:
					bmeta.Status = pbm.StatusError
					bmeta.Error = shard.Error
					return false, errors.Errorf("restore on the shard %s failed with: %s", shard.Name, shard.Error)
				}
			}
		}
	}

	if shardsToFinish == 0 {
		err := r.cn.ChangeRestoreState(name, status, "")
		if err != nil {
			return false, errors.Wrapf(err, "update backup meta with %s", status)
		}
		return true, nil
	}

	return false, nil
}

func (r *Restore) waitForStatus(name string, status pbm.Status) error {
	tk := time.NewTicker(time.Second * 1)
	defer tk.Stop()
	for {
		select {
		case <-tk.C:
			meta, err := r.cn.GetRestoreMeta(name)
			if err != nil {
				return errors.Wrap(err, "get restore metadata")
			}

			clusterTime, err := r.cn.ClusterTime()
			if err != nil {
				return errors.Wrap(err, "read cluster time")
			}

			if meta.Hb.T+pbm.StaleFrameSec < clusterTime.T {
				return errors.Errorf("restore stuck, last beat ts: %d", meta.Hb.T)
			}

			switch meta.Status {
			case status:
				return nil
			case pbm.StatusError:
				return errors.Wrap(err, "restore failed")
			}
		case <-r.cn.Context().Done():
			return nil
		}
	}
}

// MarkFailed set state of backup and given rs as error with msg
func (r *Restore) MarkFailed(name, rsName, msg string) error {
	err := r.cn.ChangeRestoreState(name, pbm.StatusError, msg)
	if err != nil {
		return errors.Wrap(err, "set backup state")
	}
	err = r.cn.ChangeRestoreRSState(name, rsName, pbm.StatusError, msg)
	return errors.Wrap(err, "set replset state")
}

func getMetaFromStore(bcpName string, stg pbm.Storage) (*pbm.BackupMeta, error) {
	rr, _, err := Source(stg, bcpName+".pbm.json", pbm.CompressionTypeNone)
	if err != nil {
		return nil, errors.Wrap(err, "get from store")
	}

	b := &pbm.BackupMeta{}
	err = json.NewDecoder(rr).Decode(b)

	return b, errors.Wrap(err, "decode")
}
