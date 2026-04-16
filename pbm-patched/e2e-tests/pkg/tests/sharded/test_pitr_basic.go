package sharded

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	pbmt "github.com/percona/percona-backup-mongodb/e2e-tests/pkg/pbm"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
)

func (c *Cluster) PITRbasic() {
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

	bcp2 := c.LogicalBackup()
	c.BackupWaitDone(context.TODO(), bcp2)

	ds := time.Second * 30 * time.Duration(rand.Int63n(5)+2)
	log.Printf("Generating data for %v", ds)
	time.Sleep(ds)

	c.printBcpList()

	log.Println("Get reference time")
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

	log.Printf("Deleting backup %v", bcp2)
	err := c.mongopbm.DeleteBackup(context.TODO(), bcp2)
	if err != nil {
		log.Fatalf("Error: delete backup %s: %v", bcp2, err)
	}

	c.printBcpList()

	// +1 sec since we are PITR restore done up to < time (not <=)
	c.PITRestore(time.Unix(int64(lastt.T), 0).Add(time.Second * 1))

	for name, shard := range c.shards {
		c.pitrcCheck(name, shard, &counters[name].cnt.data, lastt)
	}
}

type shardCounter struct {
	cnt    *pcounter
	cancel context.CancelFunc
}

func (c *Cluster) pitrOn() {
	err := c.pbm.PITRon()
	if err != nil {
		log.Fatalf("ERROR: turn PITR on: %v\n", err)
	}
}

const pitrCheckPeriod = time.Second * 15

func (c *Cluster) pitrOff() {
	time.Sleep(pitrCheckPeriod * 11 / 10)

	err := c.pbm.PITRoff()
	if err != nil {
		log.Fatalf("ERROR: turn PITR off: %v\n", err)
	}
	log.Println("Turning pitr off")
	log.Println("waiting for the pitr to stop")
	err = c.mongopbm.WaitConcurentOp(context.TODO(),
		&lock.LockHeader{Type: ctrl.CmdPITR},
		time.Minute*5)
	if err != nil {
		log.Fatalf("ERROR: waiting for the pitr to stop: %v", err)
	}
	time.Sleep(time.Second * 1)
}

type pcounter struct {
	mx    sync.Mutex
	data  []pbmt.Counter
	curr  *pbmt.Counter
	shard string
	cn    *pbmt.Mongo
}

func newpcounter(shard string, cn *pbmt.Mongo) *pcounter {
	return &pcounter{
		shard: shard,
		cn:    cn,
	}
}

func (pc *pcounter) write(ctx context.Context, t time.Duration) {
	tk := time.NewTicker(t)
	defer tk.Stop()

	for cnt := 0; ; cnt++ {
		select {
		case <-tk.C:
			td, err := pc.cn.WriteCounter(cnt)
			if err != nil {
				log.Fatalln("ERROR:", pc.shard, "write test counter:", err)
			}

			td.WriteTime, err = pc.cn.GetLastWrite()
			if err != nil {
				log.Fatalln("ERROR:", pc.shard, "get cluster last write time:", err)
			}
			pc.data = append(pc.data, *td)
			pc.mx.Lock()
			pc.curr = td
			pc.mx.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

func (pc *pcounter) current() *pbmt.Counter {
	pc.mx.Lock()
	defer pc.mx.Unlock()
	return pc.curr
}

func (c *Cluster) pitrcCheck(name string, shard *pbmt.Mongo, data *[]pbmt.Counter, bcpLastWrite primitive.Timestamp) {
	log.Println(name, "getting restored counters")
	restored, err := shard.GetCounters()
	if err != nil {
		log.Fatalln("ERROR: ", name, "get data:", err)
	}

	log.Println(name, "checking restored counters")
	var lastc pbmt.Counter
	for i, d := range *data {
		// if d.WriteTime.Compare(bcpLastWrite) <= 0 {
		if d.WriteTime.T <= bcpLastWrite.T {
			if len(restored) <= i {
				log.Fatalf("ERROR: %s no record #%d/%d in restored (%d) | last: %v\n",
					name, i, d.Count, len(restored), lastc)
			}
			r := restored[i]
			if d.Count != r.Count {
				log.Fatalf("ERROR: %s unmatched backuped %v and restored %v. Bcp last write: %v\n", name, d, r, bcpLastWrite)
			}
		} else if i < len(restored) {
			r := restored[i]
			log.Fatalf("ERROR: %s data %v shouldn't be restored. Cmp to: %v. Bcp last write: %v\n", name, r, d, bcpLastWrite)
		}

		lastc = d
	}
}
