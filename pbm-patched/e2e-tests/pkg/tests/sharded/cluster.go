package sharded

import (
	"context"
	"encoding/json"
	"fmt"
	stdlog "log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	pbmt "github.com/percona/percona-backup-mongodb/e2e-tests/pkg/pbm"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	"github.com/percona/percona-backup-mongodb/pbm/restore"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type Cluster struct {
	ctx context.Context

	pbm    *pbmt.Ctl
	docker *pbmt.Docker

	mongos   *pbmt.Mongo
	shards   map[string]*pbmt.Mongo
	mongopbm *pbmt.MongoPBM
	confsrv  string

	cfg ClusterConf
}

type ClusterConf struct {
	Configsrv       string
	Mongos          string
	Shards          map[string]string
	DockerURI       string
	PbmContainer    string
	ConfigsrvRsName string
}

func New(cfg ClusterConf) *Cluster {
	ctx := context.Background()
	c := &Cluster{
		ctx:      ctx,
		mongos:   mgoConn(ctx, cfg.Mongos),
		mongopbm: pbmConn(ctx, cfg.Configsrv),
		shards:   make(map[string]*pbmt.Mongo),
		confsrv:  cfg.ConfigsrvRsName,
		cfg:      cfg,
	}
	for name, uri := range cfg.Shards {
		c.shards[name] = mgoConn(ctx, uri)
	}

	pbmObj, err := pbmt.NewCtl(c.ctx, cfg.DockerURI, cfg.PbmContainer)
	if err != nil {
		stdlog.Fatalln("connect to mongo:", err)
	}
	stdlog.Println("connected to pbm")

	c.pbm = pbmObj

	c.docker, err = pbmt.NewDocker(c.ctx, cfg.DockerURI)
	if err != nil {
		stdlog.Fatalln("connect to docker:", err)
	}
	stdlog.Println("connected to docker")

	return c
}

func (c *Cluster) Reconnect() {
	ctx := context.Background()
	c.mongos = mgoConn(ctx, c.cfg.Mongos)
	c.mongopbm = pbmConn(ctx, c.cfg.Configsrv)

	for name, uri := range c.cfg.Shards {
		c.shards[name] = mgoConn(ctx, uri)
	}
}

func (c *Cluster) ApplyConfig(ctx context.Context, file string) {
	stdlog.Println("apply config")
	err := c.pbm.ApplyConfig(file)
	if err != nil {
		l, _ := c.pbm.ContainerLogs()
		stdlog.Fatalf("apply config: %v\ncontainer logs: %s\n", err, l)
	}

	stdlog.Println("waiting for the new storage to resync")
	err = c.mongopbm.WaitOp(ctx,
		&lock.LockHeader{Type: ctrl.CmdResync},
		time.Minute*5)
	if err != nil {
		stdlog.Fatalf("waiting for the store resync: %v", err)
	}

	time.Sleep(time.Second * 6) // give time to refresh agent-checks
}

func (c *Cluster) ServerVersion() string {
	v, err := c.mongos.ServerVersion()
	if err != nil {
		stdlog.Fatalln("Get server version:", err)
	}
	return v
}

func (c *Cluster) DeleteBallast() {
	stdlog.Println("deleting data")
	deleted, err := c.mongos.ResetBallast()
	if err != nil {
		stdlog.Fatalln("deleting data:", err)
	}
	stdlog.Printf("deleted %d documents", deleted)
}

func (c *Cluster) LogicalRestore(ctx context.Context, bcpName string) {
	c.LogicalRestoreWithParams(ctx, bcpName, []string{})
}

func (c *Cluster) LogicalRestoreWithParams(_ context.Context, bcpName string, options []string) {
	stdlog.Println("restoring the backup")
	_, err := c.pbm.Restore(bcpName, options)
	if err != nil {
		stdlog.Fatalln("restoring the backup:", err)
	}

	stdlog.Println("waiting for the restore")
	err = c.pbm.CheckRestore(bcpName, time.Minute*25)
	if err != nil {
		stdlog.Fatalln("check backup restore:", err)
	}
	// just wait so the all data gonna be written (aknowleged) before the next steps
	time.Sleep(time.Second * 1)
	stdlog.Printf("restore finished '%s'\n", bcpName)
}

func (c *Cluster) PhysicalRestore(ctx context.Context, bcpName string) {
	c.PhysicalRestoreWithParams(ctx, bcpName, []string{})
}

func (c *Cluster) PhysicalRestoreWithParams(ctx context.Context, bcpName string, options []string) {
	stdlog.Println("reset ENV variables")
	for name := range c.shards {
		err := pbmt.ClockSkew(name, "0", c.cfg.DockerURI)
		if err != nil {
			stdlog.Fatalln("reset ENV variables:", err)
		}
	}

	stdlog.Println("restoring the backup")
	name, err := c.pbm.Restore(bcpName, options)
	if err != nil {
		stdlog.Fatalln("restoring the backup:", err)
	}

	stdlog.Println("waiting for the restore", name)
	err = c.waitPhyRestore(ctx, name, time.Minute*25)
	if err != nil {
		stdlog.Fatalln("check backup restore:", err)
	}
	// just wait so the all data gonna be written (aknowleged) before the next steps
	time.Sleep(time.Second * 1)

	// sharded cluster, hence have a mongos
	if c.confsrv == "cfg" {
		stdlog.Println("stopping mongos")
		err = c.docker.StopContainers([]string{"com.percona.pbm.app=mongos"})
		if err != nil {
			stdlog.Fatalln("stop mongos:", err)
		}
	}

	stdlog.Println("restarting the culster")
	err = c.docker.StartContainers([]string{"com.percona.pbm.app=mongod"})
	if err != nil {
		stdlog.Fatalln("restart mongod:", err)
	}

	time.Sleep(time.Second * 5)

	if c.confsrv == "cfg" {
		stdlog.Println("starting mongos")
		err = c.docker.StartContainers([]string{"com.percona.pbm.app=mongos"})
		if err != nil {
			stdlog.Fatalln("start mongos:", err)
		}
	}

	stdlog.Println("restarting agents")
	err = c.docker.StartAgentContainers([]string{"com.percona.pbm.app=agent"})
	if err != nil {
		stdlog.Fatalln("restart agents:", err)
	}

	// Give time for agents to report its availability status
	// after the restart
	time.Sleep(time.Second * 7)

	stdlog.Println("resync")
	err = c.pbm.Resync()
	if err != nil {
		stdlog.Fatalln("resync:", err)
	}

	c.Reconnect()

	err = c.mongopbm.WaitOp(ctx,
		&lock.LockHeader{Type: ctrl.CmdResync},
		time.Minute*5)
	if err != nil {
		stdlog.Fatalf("waiting for resync: %v", err)
	}

	time.Sleep(time.Second * 7)

	stdlog.Printf("restore finished '%s'\n", bcpName)
}

func (c *Cluster) waitPhyRestore(ctx context.Context, name string, waitFor time.Duration) error {
	stg, err := c.mongopbm.Storage(ctx)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	fname := fmt.Sprintf("%s/%s.json", defs.PhysRestoresDir, name)
	stdlog.Println("checking", fname)

	tmr := time.NewTimer(waitFor)
	tkr := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-tmr.C:
			list, err := c.pbm.RunCmd("pbm", "status")
			if err != nil {
				return errors.Wrap(err, "timeout reached. get status")
			}
			return errors.Errorf("timeout reached. status:\n%s", list)
		case <-tkr.C:
			rmeta, err := getRestoreMetaStg(fname, stg)
			if errors.Is(err, errors.ErrNotFound) {
				continue
			}
			if err != nil {
				return errors.Wrap(err, "get restore metadata")
			}

			switch rmeta.Status {
			case defs.StatusDone:
				return nil
			case defs.StatusError:
				return errors.Errorf("restore failed with: %s", rmeta.Error)
			}
		}
	}
}

func getRestoreMetaStg(name string, stg storage.Storage) (*restore.RestoreMeta, error) {
	_, err := stg.FileStat(name)
	if errors.Is(err, storage.ErrNotExist) {
		return nil, errors.ErrNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "get stat")
	}

	src, err := stg.SourceReader(name)
	if err != nil {
		return nil, errors.Wrapf(err, "get file %s", name)
	}

	rmeta := &restore.RestoreMeta{}
	err = json.NewDecoder(src).Decode(rmeta)
	if err != nil {
		return nil, errors.Wrapf(err, "decode meta %s", name)
	}

	return rmeta, nil
}

func (c *Cluster) PITRestoreCT(t primitive.Timestamp) {
	stdlog.Printf("restoring to the point-in-time %v", t)
	err := c.pbm.PITRestoreClusterTime(t.T, t.I)
	if err != nil {
		stdlog.Fatalln("restore:", err)
	}

	stdlog.Println("waiting for the restore")
	err = c.pbm.CheckPITRestore(time.Unix(int64(t.T), 0), time.Minute*25)
	if err != nil {
		stdlog.Fatalln("check restore:", err)
	}
	// just wait so the all data gonna be written (aknowleged) before the next steps
	time.Sleep(time.Second * 1)
	stdlog.Printf("restore to the point-in-time '%v' finished", t)
}

func (c *Cluster) PITRestore(t time.Time) {
	stdlog.Printf("restoring to the point-in-time %v", t)
	err := c.pbm.PITRestore(t)
	if err != nil {
		stdlog.Fatalln("restore:", err)
	}

	stdlog.Println("waiting for the restore")
	err = c.pbm.CheckPITRestore(t, time.Minute*25)
	if err != nil {
		stdlog.Fatalln("check restore:", err)
	}
	// just wait so the all data gonna be written (aknowleged) before the next steps
	time.Sleep(time.Second * 1)
	stdlog.Printf("restore to the point-in-time '%v' finished", t)
}

func (c *Cluster) LogicalBackup() string {
	return c.backup(defs.LogicalBackup)
}

func (c *Cluster) PhysicalBackup() string {
	return c.backup(defs.PhysicalBackup)
}

func (c *Cluster) ReplayOplog(a, b time.Time) {
	stdlog.Printf("replay oplog from %v to %v", a, b)
	if err := c.pbm.ReplayOplog(a, b); err != nil {
		stdlog.Fatalln("restore:", err)
	}

	stdlog.Println("waiting for the oplog replay")
	if err := c.pbm.CheckOplogReplay(a, b, 25*time.Minute); err != nil {
		stdlog.Fatalln("check restore:", err)
	}

	// just wait so the all data gonna be written (aknowleged) before the next steps
	time.Sleep(time.Second)
	stdlog.Printf("replay oplog from %v to %v finished", a, b)
}

func (c *Cluster) backup(typ defs.BackupType, opts ...string) string {
	stdlog.Println("starting backup")
	bcpName, err := c.pbm.Backup(typ, opts...)
	if err != nil {
		l, _ := c.pbm.ContainerLogs()
		stdlog.Fatalf("starting backup: %v\ncontainer logs: %s\n", err, l)
	}
	stdlog.Printf("backup started '%s'\n", bcpName)

	return bcpName
}

func (c *Cluster) BackupWaitDone(ctx context.Context, bcpName string) {
	stdlog.Println("waiting for the backup")
	ts := time.Now()
	err := c.checkBackup(ctx, bcpName, time.Minute*25)
	if err != nil {
		stdlog.Fatalln("check backup state:", err)
	}

	// locks being released NOT immediately after the backup succeed
	// see https://github.com/percona/percona-backup-mongodb/blob/v1.1.3/agent/agent.go#L128-L143
	needToWait := defs.WaitBackupStart + time.Second - time.Since(ts)
	if needToWait > 0 {
		stdlog.Printf("waiting for the lock to be released for %s", needToWait)
		time.Sleep(needToWait)
	}
	stdlog.Printf("backup finished '%s'\n", bcpName)
}

func (c *Cluster) SetBallastData(amount int64) {
	stdlog.Println("set ballast data to", amount)
	cnt, err := c.mongos.SetBallast(amount)
	if err != nil {
		stdlog.Fatalln("generating ballast:", err)
	}
	stdlog.Println("ballast data:", cnt)
}

func (c *Cluster) DataChecker() func() {
	hashes1 := make(map[string]map[string]string)
	for name, s := range c.shards {
		h, err := s.DBhashes()
		if err != nil {
			stdlog.Fatalf("get db hashes %s: %v\n", name, err)
		}
		stdlog.Printf("current %s db hash %s\n", name, h["_all_"])
		hashes1[name] = h
	}

	return func() {
		stdlog.Println("Checking restored backup")

		for name, s := range c.shards {
			h, err := s.DBhashes()
			if err != nil {
				stdlog.Fatalf("get db hashes %s: %v\n", name, err)
			}
			if hashes1[name]["_all_"] != h["_all_"] {
				stdlog.Fatalf("%s: hashes don't match. before %s now %s", name, hashes1[name]["_all_"], h["_all_"])
			}
		}
	}
}

// Flush removes all backups, restores and PITR chunks metadata from the PBM db
func (c *Cluster) Flush() error {
	cols := []string{
		defs.BcpCollection,
		defs.PITRChunksCollection,
		defs.RestoresCollection,
	}
	for _, cl := range cols {
		_, err := c.mongopbm.Conn().MongoClient().Database(defs.DB).Collection(cl).DeleteMany(context.Background(), bson.M{})
		if err != nil {
			return errors.Wrapf(err, "delete many from %s", cl)
		}
	}

	return nil
}

func (c *Cluster) FlushStorage(ctx context.Context) error {
	stg, err := c.mongopbm.Storage(ctx)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	fls, err := stg.List("", "")
	if err != nil {
		return errors.Wrap(err, "get files list")
	}

	for _, f := range fls {
		err = stg.Delete(f.Name)
		if err != nil {
			stdlog.Println("Warning: unable to delete", f.Name)
		}
	}

	return nil
}

func (c *Cluster) checkBackup(ctx context.Context, bcpName string, waitFor time.Duration) error {
	tmr := time.NewTimer(waitFor)
	tkr := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-tmr.C:
			sts, err := c.pbm.RunCmd("pbm", "status")
			if err != nil {
				return errors.Wrap(err, "timeout reached. pbm status")
			}
			return errors.Errorf("timeout reached. pbm status:\n%s", sts)
		case <-tkr.C:
			m, err := c.mongopbm.GetBackupMeta(ctx, bcpName)
			if errors.Is(err, errors.ErrNotFound) {
				continue
			}
			if err != nil {
				return errors.Wrap(err, "get backup meta")
			}
			switch m.Status {
			case defs.StatusDone:
				// to be sure the lock is released
				time.Sleep(time.Second * 3)
				return nil
			case defs.StatusError:
				return m.Error()
			}
		}
	}
}

func mgoConn(ctx context.Context, uri string) *pbmt.Mongo {
	m, err := pbmt.NewMongo(ctx, uri)
	if err != nil {
		stdlog.Fatalln("connect to mongo:", err)
	}
	stdlog.Println("connected to", uri)
	return m
}

func pbmConn(ctx context.Context, uri string) *pbmt.MongoPBM {
	m, err := pbmt.NewMongoPBM(ctx, uri)
	if err != nil {
		stdlog.Fatalln("connect to mongo:", err)
	}
	stdlog.Println("connected to", uri)
	return m
}
