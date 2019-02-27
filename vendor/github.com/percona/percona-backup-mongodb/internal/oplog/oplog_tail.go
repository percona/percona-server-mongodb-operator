package oplog

import (
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/percona/percona-backup-mongodb/internal/cluster"
	"github.com/percona/percona-backup-mongodb/mdbstructs"
	"github.com/pkg/errors"
)

type chanDataTye []byte

type OplogTail struct {
	session             *mgo.Session
	oplogCollection     string
	startOplogTimestamp bson.MongoTimestamp
	lastOplogTimestamp  bson.MongoTimestamp
	// This is an int64 and not a bson.MongoTimestamp because we need to use atomic.LoadInt64 (or StoreInt64)
	// for performance.
	stopAtTimestampt int64

	totalSize         uint64
	docsCount         uint64
	remainingBytes    []byte
	nextChunkPosition int

	wg              *sync.WaitGroup
	dataChan        chan chanDataTye
	stopChan        chan bool
	readerStopChan  chan bool
	startedReadChan chan bool
	readFunc        func([]byte) (int, error)
	lock            *sync.Mutex
	isEOF           bool
	running         bool
}

const (
	oplogDB = "local"
)

var (
	mgoIterBatch    = 100
	mgoIterPrefetch = 1.0
)

func Open(session *mgo.Session) (*OplogTail, error) {
	ot, err := open(session.Clone())
	if err != nil {
		return nil, err
	}
	go ot.tail()
	return ot, nil
}

func OpenAt(session *mgo.Session, t time.Time, c uint32) (*OplogTail, error) {
	ot, err := open(session.Clone())
	if err != nil {
		return nil, err
	}
	mongoTimestamp, err := bson.NewMongoTimestamp(t, c)
	if err != nil {
		return nil, err
	}
	ot.startOplogTimestamp = mongoTimestamp
	go ot.tail()
	return ot, nil
}

func (ot *OplogTail) WaitUntilFirstDoc() error {
	if ot.startedReadChan == nil {
		log.Fatal("ot.startedReadChan is nil")
	}
	select {
	case <-ot.startedReadChan:
		return nil
	case <-ot.stopChan:
		return nil
	}
}

func (ot *OplogTail) WaitUntilFirstDocWithTimeout(timeout time.Duration) (bool, error) {
	if ot.startedReadChan == nil {
		log.Fatal("ot.startedReadChan is nil")
	}
	select {
	case <-ot.startedReadChan:
		return false, nil
	case <-ot.stopChan:
		return false, nil
	case <-time.After(timeout):
		if err := ot.session.Ping(); err != nil {
			return true, err
		} else {
			return true, nil
		}
	}
}

func open(session *mgo.Session) (*OplogTail, error) {
	if session == nil {
		return nil, fmt.Errorf("Invalid session (nil)")
	}

	oplogCol, err := determineOplogCollectionName(session)
	if err != nil {
		return nil, errors.Wrap(err, "Cannot determine the oplog collection name")
	}

	ot := &OplogTail{
		session:         session,
		oplogCollection: oplogCol,
		dataChan:        make(chan chanDataTye, 1),
		stopChan:        make(chan bool),
		readerStopChan:  make(chan bool),
		startedReadChan: make(chan bool),
		running:         true,
		wg:              &sync.WaitGroup{},
		lock:            &sync.Mutex{},
	}
	ot.readFunc = makeReader(ot)
	return ot, nil
}

func (ot *OplogTail) LastOplogTimestamp() bson.MongoTimestamp {
	if ot != nil {
		return ot.lastOplogTimestamp
	}
	var ts bson.MongoTimestamp
	return ts
}

// Implement the Reader interface to be able to pipe it into an S3 stream or through an
// encrypter
func (ot *OplogTail) Read(buf []byte) (int, error) {
	n, err := ot.readFunc(buf)
	if err == nil {
		atomic.AddUint64(&ot.docsCount, 1)
		atomic.AddUint64(&ot.totalSize, uint64(n))
	}
	return n, err
}

func (ot *OplogTail) Size() uint64 {
	return atomic.LoadUint64(&ot.totalSize)
}

func (ot *OplogTail) Count() uint64 {
	return atomic.LoadUint64(&ot.docsCount)
}

// Cancel stopts the tailer immediately without waiting the tailer to reach the
// document having timestamp = IsMasterDoc().LastWrite.OpTime.Ts
func (ot *OplogTail) Cancel() {
	close(ot.stopChan)
	ot.wg.Wait()
}

func (ot *OplogTail) Close() error {
	if !ot.isRunning() {
		return fmt.Errorf("Tailer is already closed")
	}

	ismaster, err := cluster.NewIsMaster(ot.session)
	if err != nil {
		close(ot.stopChan)
		return fmt.Errorf("Cannot get master doc LastWrite.Optime: %s", err)
	}

	ot.lock.Lock()
	atomic.StoreInt64(&ot.stopAtTimestampt, int64(ismaster.IsMasterDoc().LastWrite.OpTime.Ts))
	ot.lock.Unlock()

	ot.wg.Wait()

	return nil
}

func (ot *OplogTail) CloseAt(ts bson.MongoTimestamp) error {
	if !ot.isRunning() {
		return fmt.Errorf("Tailer is already closed")
	}

	ot.lock.Lock()
	atomic.StoreInt64(&ot.stopAtTimestampt, int64(ts))
	ot.lock.Unlock()

	ot.wg.Wait()

	return nil
}

func (ot *OplogTail) isRunning() bool {
	if ot != nil {
		ot.lock.Lock()
		defer ot.lock.Unlock()
		return ot.running
	}
	return false
}

func (ot *OplogTail) setRunning(state bool) {
	ot.lock.Lock()
	defer ot.lock.Unlock()
	ot.running = state
}

func (ot *OplogTail) tail() {
	ot.wg.Add(1)
	defer ot.wg.Done()
	once := &sync.Once{}

	iter := ot.makeIterator()
	for {
		select {
		case <-ot.stopChan:
			iter.Close()
			return
		default:
		}
		result := bson.Raw{}

		iter.Next(&result)

		if iter.Err() != nil {
			iter.Close()
			iter = ot.makeIterator()
			continue
		}

		once.Do(func() { close(ot.startedReadChan) })

		if iter.Timeout() {
			ot.lock.Lock()
			if ot.stopAtTimestampt != 0 {
				iter.Close()
				ot.lock.Unlock()
				close(ot.readerStopChan)
				return
			}
			ot.lock.Unlock()
			continue
		}

		once.Do(func() { close(ot.startedReadChan) })

		data := bson.M{}
		if err := result.Unmarshal(&data); err == nil {
			ot.lastOplogTimestamp = data["ts"].(bson.MongoTimestamp)
			if ot.startOplogTimestamp == 0 {
				ot.startOplogTimestamp = ot.lastOplogTimestamp
			}
			if atomic.LoadInt64(&ot.stopAtTimestampt) != 0 {
				if ot.lastOplogTimestamp != 0 {
					if ot.lastOplogTimestamp > bson.MongoTimestamp(ot.stopAtTimestampt) {
						iter.Close()
						close(ot.readerStopChan)
						return
					}
				}
			}

			ot.dataChan <- result.Data
		} else {
			log.Fatalf("cannot unmarshal oplog doc: %s", err)
		}
	}
}

func (ot *OplogTail) getStopAtTimestamp() bson.MongoTimestamp {
	ot.lock.Lock()
	defer ot.lock.Unlock()
	return bson.MongoTimestamp(ot.stopAtTimestampt)
}

func (ot *OplogTail) setStopAtTimestamp(ts bson.MongoTimestamp) {
	ot.lock.Lock()
	defer ot.lock.Unlock()
	ot.stopAtTimestampt = int64(ts)
}

// TODO
// Maybe if stopAtTimestampt is nil, we can replace the timeout by -1 to avoid restarting the
// tailer query unnecessarily but I am following the two rules in the matter of optimization:
// Rule 1: Don't do it.
// Rule 2 (for experts only). Don't do it yet
func (ot *OplogTail) makeIterator() *mgo.Iter {
	col := ot.session.DB(oplogDB).C(ot.oplogCollection)
	comment := "github.com/percona/percona-backup-mongodb/internal/oplog.(*OplogTail).tail()"
	return col.Find(ot.tailQuery()).LogReplay().Comment(comment).Batch(mgoIterBatch).Prefetch(mgoIterPrefetch).Tail(1 * time.Second)
}

// tailQuery returns a bson.M query filter for the oplog tail
// Criteria:
//   1. If 'lastOplogTimestamp' is defined, tail all non-noop oplogs with 'ts' $gt that ts
//   2. Or, if 'startOplogTimestamp' is defined, tail all non-noop oplogs with 'ts' $gte that ts
//   3. Or, tail all non-noop oplogs with 'ts' $gt 'lastWrite.OpTime.Ts' from the result of the "isMaster" mongodb server command
//   4. Or, tail all non-noop oplogs with 'ts' $gte now.
func (ot *OplogTail) tailQuery() bson.M {
	query := bson.M{"op": bson.M{"$ne": mdbstructs.OperationNoop}}

	ot.lock.Lock()
	if ot.lastOplogTimestamp != 0 {
		query["ts"] = bson.M{"$gt": ot.lastOplogTimestamp}
		ot.lock.Unlock()
		return query
	} else if ot.startOplogTimestamp != 0 {
		query["ts"] = bson.M{"$gte": ot.startOplogTimestamp}
		ot.lock.Unlock()
		return query
	}
	ot.lock.Unlock()

	isMaster, err := cluster.NewIsMaster(ot.session)
	if err != nil {
		mongoTimestamp, _ := bson.NewMongoTimestamp(time.Now(), 0)
		query["ts"] = bson.M{"$gte": mongoTimestamp}
		return query
	}

	query["ts"] = bson.M{"$gt": isMaster.LastWrite()}
	return query
}

func determineOplogCollectionName(session *mgo.Session) (string, error) {
	isMaster, err := cluster.NewIsMaster(session)
	if err != nil {
		return "", errors.Wrap(err, "Cannot determine the oplog collection name")
	}

	if len(isMaster.IsMasterDoc().Hosts) > 0 {
		return "oplog.rs", nil
	}
	if !isMaster.IsMasterDoc().IsMaster {
		return "", fmt.Errorf("not connected to master")
	}
	return "oplog.$main", nil
}

func makeReader(ot *OplogTail) func([]byte) (int, error) {
	return func(buf []byte) (int, error) {
		// Read comment #2 below before reading this
		// If in the previous call to Read, the provided buffer was smaller
		// than the document we had from the oplog, we have to return the
		// remaining bytes instead of reading a new document from the oplog.
		// Again, the provided buffer could be smaller than the remaining
		// bytes of the provious document.
		// ot.nextChunkPosition keeps track of the starting position of the
		// remaining bytes to return.
		// Run: go test -v -run TestReadIntoSmallBuffer
		// to see how it works.
		if ot.remainingBytes != nil {
			nextChunkSize := len(ot.remainingBytes)
			responseSize := nextChunkSize - ot.nextChunkPosition

			if len(buf) < nextChunkSize-ot.nextChunkPosition {
				copy(buf, ot.remainingBytes[ot.nextChunkPosition:])
				ot.nextChunkPosition += len(buf)
				return len(buf), nil
			}

			// This is the last chunk of data in ot.remainingBytes
			// After filling the destination buffer, clean ot.remainingBytes
			// so next call to the Read method will go through the select
			// below, to read a new document from the oplog tailer.
			copy(buf, ot.remainingBytes[ot.nextChunkPosition:])
			ot.remainingBytes = nil
			ot.nextChunkPosition = 0
			return responseSize, nil
		}

		select {
		case <-ot.readerStopChan:
			ot.readFunc = func([]byte) (int, error) {
				return 0, fmt.Errorf("already closed")
			}
			return 0, io.EOF
		case doc := <-ot.dataChan:
			// Comment #2
			// The buffer size where we have to copy the oplog document
			// could be smaller than the document. In that case, we must
			// keep the remaining bytes of the document for the next call
			// to the Read method.
			docSize := len(doc)
			bufSize := len(buf)
			retSize := len(doc)
			if bufSize < docSize {
				retSize = bufSize
			}

			if len(buf) < docSize {
				ot.remainingBytes = make([]byte, docSize-bufSize)
				copy(ot.remainingBytes, doc[bufSize:])
				ot.nextChunkPosition = 0
			}
			copy(buf, doc)
			return retSize, nil
		}
	}
}
