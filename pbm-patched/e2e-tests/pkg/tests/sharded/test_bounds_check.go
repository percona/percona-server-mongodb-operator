package sharded

import (
	"context"
	"log"
	"math/rand"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/mod/semver"

	pbmt "github.com/percona/percona-backup-mongodb/e2e-tests/pkg/pbm"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

type scounter struct {
	data   <-chan *[]pbmt.Counter
	cancel context.CancelFunc
}

func lte(t1, t2 primitive.Timestamp) bool {
	return t1.Compare(t2) <= 0
}

func lt(t1, t2 primitive.Timestamp) bool {
	return t1.Compare(t2) < 0
}

func (c *Cluster) BackupBoundsCheck(typ defs.BackupType, mongoVersion string) {
	inRange := lte
	backup := c.LogicalBackup
	restore := c.LogicalRestore
	if typ == defs.PhysicalBackup {
		backup = c.PhysicalBackup
		restore = c.PhysicalRestore

		// mongo v4.2 may not recover an oplog entry w/ timestamp equal
		// to the `BackLastWrite` (see https://jira.mongodb.org/browse/SERVER-54005).
		// Despite in general we expect to be restored all entries with `timestamp <= BackLastWrite`
		// in v4.2 we should expect only `timestamp < BackLastWrite`
		if semver.Compare(mongoVersion, "v4.2") == 0 {
			inRange = lt
		}
	}

	counters := make(map[string]scounter)
	for name, shard := range c.shards {
		c.bcheckClear(name, shard)
		dt, cancel := c.bcheckWrite(name, shard, time.Millisecond*10*time.Duration(rand.Int63n(49)+1))
		counters[name] = scounter{
			data:   dt,
			cancel: cancel,
		}
	}

	bcpName := backup()

	c.BackupWaitDone(context.TODO(), bcpName)
	time.Sleep(time.Second * 1)

	for _, c := range counters {
		c.cancel()
	}

	bcpMeta, err := c.mongopbm.GetBackupMeta(context.TODO(), bcpName)
	if err != nil {
		log.Fatalf("ERROR: get backup '%s' metadata: %v\n", bcpName, err)
	}
	// fmt.Println("BCP_LWT:", bcpMeta.LastWriteTS)

	c.DeleteBallast()
	for name, shard := range c.shards {
		c.bcheckClear(name, shard)
	}

	restore(context.TODO(), bcpName)

	for name, shard := range c.shards {
		c.bcheckCheck(name, shard, <-counters[name].data, bcpMeta.LastWriteTS, inRange)
	}
}

func (c *Cluster) bcheckClear(name string, shard *pbmt.Mongo) {
	log.Println(name, "reseting counters")
	dcnt, err := shard.ResetCounters()
	if err != nil {
		log.Println("WARNING:", name, "resetting counters:", err)
	}
	log.Println(name, "deleted counters:", dcnt)
}

func (c *Cluster) bcheckWrite(
	name string,
	shard *pbmt.Mongo,
	t time.Duration,
) (<-chan *[]pbmt.Counter, context.CancelFunc) {
	var data []pbmt.Counter
	ctx, cancel := context.WithCancel(c.ctx)
	dt := make(chan *[]pbmt.Counter)
	go func() {
		log.Println(name, "writing counters")
		tk := time.NewTicker(t)
		defer tk.Stop()
		cnt := 0
		for {
			select {
			case <-tk.C:
				td, err := shard.WriteCounter(cnt)
				if err != nil {
					log.Fatalln("ERROR:", name, "write test counter:", err)
				}

				td.WriteTime, err = shard.GetLastWrite()
				if err != nil {
					log.Fatalln("ERROR:", name, "get cluster last write time:", err)
				}

				// fmt.Println("->", cnt, td.WriteTime)
				data = append(data, *td)
				cnt++
			case <-ctx.Done():
				log.Println(name, "writing counters finished")
				dt <- &data
				return
			}
		}
	}()

	return dt, cancel
}

func (c *Cluster) bcheckCheck(
	name string,
	shard *pbmt.Mongo,
	data *[]pbmt.Counter,
	bcpLastWrite primitive.Timestamp,
	inRange func(ts, limit primitive.Timestamp) bool,
) {
	log.Println(name, "getting restored counters")
	restored, err := shard.GetCounters()
	if err != nil {
		log.Fatalln("ERROR: ", name, "get data:", err)
	}

	log.Println(name, "checking restored counters")
	var lastc pbmt.Counter
	for i, d := range *data {
		if inRange(d.WriteTime, bcpLastWrite) {
			if len(restored) <= i {
				log.Fatalf("ERROR: %s no record #%d/%d [%v] in restored (%d) | last: %v. Bcp last write: %v\n",
					name, i, d.Count, d, len(restored), lastc, bcpLastWrite)
			}
			r := restored[i]
			if d.Count != r.Count {
				log.Fatalf("ERROR: %s unmatched backuped %v and restored %v. Bcp last write: %v\n",
					name, d, r, bcpLastWrite)
			}
		} else if i < len(restored) {
			r := restored[i]
			log.Fatalf("ERROR: %s data %v shouldn't be restored. Cmp to: %v. Bcp last write: %v\n",
				name, r, d, bcpLastWrite)
		}

		lastc = d
	}
}
