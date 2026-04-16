package sharded

import (
	"context"
	"log"
	"math/rand"
	"time"

	pbmt "github.com/percona/percona-backup-mongodb/e2e-tests/pkg/pbm"
)

type Backuper interface {
	Backup()
	Restore()
	WaitStarted()
	WaitSnapshot()
	WaitDone()
}

type Snapshot struct {
	bcpName string
	c       *Cluster
	started chan struct{}
	done    chan struct{}
}

func NewSnapshot(c *Cluster) *Snapshot {
	return &Snapshot{
		c:       c,
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (s *Snapshot) Backup() {
	s.bcpName = s.c.LogicalBackup()
	s.started <- struct{}{}
	s.c.BackupWaitDone(context.TODO(), s.bcpName)
	time.Sleep(time.Second * 1)
	s.done <- struct{}{}
}

func (s *Snapshot) WaitSnapshot() {}
func (s *Snapshot) WaitDone()     { <-s.done }
func (s *Snapshot) WaitStarted()  { <-s.started }

func (s *Snapshot) Restore() {
	s.c.LogicalRestore(context.TODO(), s.bcpName)
}

type Pitr struct {
	pointT  time.Time
	c       *Cluster
	started chan struct{}
	done    chan struct{}
	sdone   chan struct{}
}

func NewPitr(c *Cluster) *Pitr {
	return &Pitr{
		c:       c,
		started: make(chan struct{}),
		done:    make(chan struct{}),
		sdone:   make(chan struct{}),
	}
}

func (p *Pitr) Backup() {
	p.c.pitrOn()
	bcpName := p.c.LogicalBackup()
	p.started <- struct{}{}
	p.c.BackupWaitDone(context.TODO(), bcpName)
	p.sdone <- struct{}{}

	ds := time.Second * 30 * time.Duration(rand.Int63n(5)+2)
	log.Printf("PITR slicing for %v", ds)
	time.Sleep(ds)

	var cn *pbmt.Mongo
	for _, cn = range p.c.shards {
		break
	}
	lw, err := cn.GetLastWrite()
	if err != nil {
		log.Fatalln("ERROR: get cluster last write time:", err)
	}
	p.pointT = time.Unix(int64(lw.T), 0)

	p.done <- struct{}{}
}

func (p *Pitr) WaitSnapshot() { <-p.sdone }
func (p *Pitr) WaitDone()     { <-p.done }
func (p *Pitr) WaitStarted()  { <-p.started }

func (p *Pitr) Restore() {
	p.c.pitrOff()
	p.c.PITRestore(p.pointT)
}

type Physical struct {
	bcpName string
	c       *Cluster
	started chan struct{}
	done    chan struct{}
}

func NewPhysical(c *Cluster) *Physical {
	return &Physical{
		c:       c,
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (s *Physical) Backup() {
	s.bcpName = s.c.PhysicalBackup()
	s.started <- struct{}{}
	s.c.BackupWaitDone(context.TODO(), s.bcpName)
	time.Sleep(time.Second * 1)
	s.done <- struct{}{}
}

func (s *Physical) WaitSnapshot() {}
func (s *Physical) WaitDone()     { <-s.done }
func (s *Physical) WaitStarted()  { <-s.started }

func (s *Physical) Restore() {
	s.c.PhysicalRestore(context.TODO(), s.bcpName)
}
