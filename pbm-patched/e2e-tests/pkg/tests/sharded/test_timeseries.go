package sharded

import (
	"context"
	"log"
	"time"

	pbmt "github.com/percona/percona-backup-mongodb/e2e-tests/pkg/pbm"
)

func (c *Cluster) Timeseries() {
	ts1, err := c.newTS("ts1")
	if err != nil {
		log.Fatalln("create timeseries:", err)
	}

	ts1.gen()

	c.pitrOn()
	defer c.pitrOff()

	bcpName := c.LogicalBackup()

	c.BackupWaitDone(context.TODO(), bcpName)

	time.Sleep(time.Second)

	ts2, err := c.newTS("ts2")
	if err != nil {
		log.Fatalln("create timeseries:", err)
	}

	ts2.gen()

	ds := time.Second * 135
	log.Printf("Generating data for %v", ds)
	time.Sleep(ds)

	ts1.stop()
	ts2.stop()
	time.Sleep(time.Second * 60)
	c.pitrOff()

	time.Sleep(time.Second * 6)

	err = c.mongos.Drop("ts1")
	if err != nil {
		log.Fatalf("ERROR: drop ts1: %v", err)
	}

	err = c.mongos.Drop("ts2")
	if err != nil {
		log.Fatalf("ERROR: drop ts2: %v", err)
	}

	list, err := c.pbm.List()
	if err != nil {
		log.Fatalf("ERROR: get backups/pitr list: %v", err)
	}

	if len(list.PITR.Ranges) == 0 {
		log.Fatalf("ERROR: empty pitr list, expected a range after the last backup")
	}

	c.PITRestore(time.Unix(int64(list.PITR.Ranges[len(list.PITR.Ranges)-1].Range.End), 0))

	ts1c, err := c.mongos.Count("ts1")
	if err != nil {
		log.Fatalf("ERROR: count docs in ts1: %v", err)
	}

	if ts1.count() != uint64(ts1c) {
		log.Fatalf("ERROR: wrong timeseries count, expect %d got %d", ts1.count(), ts1c)
	}

	ts2c, err := c.mongos.Count("ts2")
	if err != nil {
		log.Fatalf("ERROR: count docs in ts2: %v", err)
	}

	if ts2.count() != uint64(ts2c) {
		log.Fatalf("ERROR: wrong timeseries count, expect %d got %d", ts2.count(), ts2c)
	}
}

type ts struct {
	col  string
	cnt  uint64
	done chan struct{}

	m *pbmt.Mongo
}

func (c *Cluster) newTS(col string) (*ts, error) {
	err := c.mongos.CreateTS(col)
	if err != nil {
		return nil, err
	}
	return &ts{
		col:  col,
		done: make(chan struct{}),
		m:    c.mongos,
	}, nil
}

func (t *ts) gen() {
	go func() {
		for {
			select {
			case <-t.done:
				return
			default:
			}

			err := t.m.InsertTS(t.col)
			if err != nil {
				log.Fatalf("Error: insert timeseries into %s: %v", t.col, err)
			}
			t.cnt++
		}
	}()
}

func (t *ts) count() uint64 {
	return t.cnt
}

func (t *ts) stop() {
	t.done <- struct{}{}
}
