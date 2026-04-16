package sharded

import (
	"context"
	"log"
	"math/rand"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	tpbm "github.com/percona/percona-backup-mongodb/e2e-tests/pkg/pbm"
)

func (c *Cluster) OplogReplay() {
	c.pitrOn()
	log.Println("turn on PITR")
	defer c.pitrOff()

	bcpName := c.LogicalBackup()

	counters := make(map[string]shardCounter)
	for name, cn := range c.shards {
		c.bcheckClear(name, cn)
		pcc := newpcounter(name, cn)
		ctx, cancel := context.WithCancel(context.TODO())
		go pcc.write(ctx, time.Millisecond*10*time.Duration(rand.Int63n(49)+1))
		counters[name] = shardCounter{
			cnt:    pcc,
			cancel: cancel,
		}
	}

	c.BackupWaitDone(context.TODO(), bcpName)

	c.printBcpList()

	time.Sleep(time.Second * 30)
	log.Printf("Sleep for %v", time.Second*30)

	log.Println("Get end reference time")
	firstt := getLastWriteTime(counters)

	bcp2 := c.LogicalBackup()
	c.BackupWaitDone(context.TODO(), bcp2)

	ds := time.Second * 30 * time.Duration(rand.Int63n(5)+2)
	log.Printf("Generating data for %v", ds)
	time.Sleep(ds)

	c.printBcpList()

	log.Println("Get end reference time")
	lastt := getLastWriteTime(counters)

	ds = time.Second * 30 * time.Duration(rand.Int63n(5)+2)
	log.Printf("Generating data for %v", ds)
	time.Sleep(ds)

	for _, c := range counters {
		c.cancel()
	}

	c.pitrOff()

	for name, shard := range c.shards {
		c.bcheckClear(name, shard)
	}

	c.printBcpList()

	// +1 sec since we are PITR restore done up to < time (not <=)
	c.LogicalRestore(context.TODO(), bcp2)
	c.ReplayOplog(
		time.Unix(int64(firstt.T), 0),
		time.Unix(int64(lastt.T), 0).Add(time.Second*1))

	for name, shard := range c.shards {
		c.pitrcCheck(name, shard, &counters[name].cnt.data, lastt)
	}
}

func getLastWrittenCounter(counters map[string]shardCounter) tpbm.Counter {
	rv := &tpbm.Counter{}

	for name, c := range counters {
		cc := c.cnt.current()
		log.Printf("\tshard %s: %d [%v] | %v",
			name, cc.WriteTime.T, time.Unix(int64(cc.WriteTime.T), 0), cc)

		if rv.WriteTime.Compare(cc.WriteTime) == -1 {
			rv = cc
		}
	}

	return *rv
}

func getLastWriteTime(counters map[string]shardCounter) primitive.Timestamp {
	return getLastWrittenCounter(counters).WriteTime
}
