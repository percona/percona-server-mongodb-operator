package storage

import (
	"fmt"
	"io"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
)

const (
	DownloadChuckSizeDefault = 8 << 20
	downloadRetries          = 10

	ccSpanDefault = 32 << 20
	arenaSpans    = 8 // an amount of spans in arena

	// assume we need more spans in arena above this number of CPUs used
	lowCPU = 8
)

// DownloadOpts adjusts download options. We go from spanSize. But if bufMaxMb is
// set, it will be a hard limit on total memory.
//
//nolint:nonamedreturns
func DownloadOpts(cc, bufMaxMb, spanSizeMb int) (arenaSize, span, c int) {
	if cc == 0 {
		cc = runtime.GOMAXPROCS(0)
	}

	// broad assumption that increased amount of concurrency may lead to
	// extra contention hence need in more spans in arena
	spans := arenaSpans
	if cc > lowCPU {
		spans *= 2
	}

	spanSize := spanSizeMb << 20
	if spanSize == 0 {
		spanSize = ccSpanDefault
	}

	bufSize := bufMaxMb << 20
	if bufSize == 0 || spanSize*spans*cc <= bufSize {
		return spanSize * spans, spanSize, cc
	}

	// download buffer can't be smaller than spanSize
	if bufSize < spanSize {
		spanSize = bufSize
	}

	// shrink coucurrency if bufSize too small
	if bufSize/cc < spanSize {
		cc = bufSize / spanSize
	}

	return spanSize * (bufSize / cc / spanSize), spanSize, cc
}

type DownloadStat struct {
	Arenas      []ArenaStat `bson:"a" json:"a"`
	Concurrency int         `bson:"cc" json:"cc"`
	ArenaSize   int         `bson:"arSize" json:"arSize"`
	SpansNum    int         `bson:"spanNum" json:"spanNum"`
	SpanSize    int         `bson:"spanSize" json:"spanSize"`
	BufSize     int         `bson:"bufSize" json:"bufSize"`
}

func NewDownloadStat(concurrency, arenaSize, spanSize int) DownloadStat {
	return DownloadStat{
		Concurrency: concurrency,
		ArenaSize:   arenaSize,
		SpansNum:    arenaSize / spanSize,
		SpanSize:    spanSize,
		BufSize:     arenaSize * concurrency,
	}
}

func (s DownloadStat) String() string {
	return fmt.Sprintf("buf %d, arena %d, span %d, spanNum %d, cc %d, %v",
		s.BufSize, s.ArenaSize, s.SpanSize, s.SpansNum, s.Concurrency, s.Arenas)
}

// ARENA

type ArenaStat struct {
	// the max amount of span was occupied simultaneously
	MaxSpan int `bson:"MaxSpan" json:"MaxSpan"`
	// how many times getSpan() was waiting for the free span
	WaitCnt int `bson:"WaitCnt" json:"WaitCnt"`
}

// Arena (bytes slice) is split into spans (represented by `dpsan`)
// whose size should be equal to download chunks. `dspan` implements io.Wrire
// and io.ReaderCloser interface. Close() marks the span as free to use
// (download another chunk).
// Free/busy spans list is managed via lock-free bitmap index.
type Arena struct {
	Buf        []byte
	SpanSize   int
	SpanBitCnt uint64
	FreeIndex  atomic.Uint64 // free slots bitmap

	Stat ArenaStat

	CpBuf []byte // preallocated buffer for io.Copy
}

func NewArena(size, spansize int) *Arena {
	snum := size / spansize

	size = spansize * snum
	return &Arena{
		Buf:        make([]byte, size),
		SpanSize:   spansize,
		SpanBitCnt: 1<<(size/spansize) - 1,
		CpBuf:      make([]byte, 32*1024),
	}
}

type dspan struct {
	rp   int // current read pos in the arena
	wp   int // current write pos in the arena
	high int // high bound index of span in the arena

	slot  int    // slot number in the arena
	arena *Arena // link to the arena
}

func (s *dspan) Write(p []byte) (int, error) {
	n := copy(s.arena.Buf[s.wp:s.high], p)

	s.wp += n
	return n, nil
}

func (s *dspan) Read(p []byte) (int, error) {
	n := copy(p, s.arena.Buf[s.rp:s.wp])
	s.rp += n

	if s.rp == s.wp {
		return n, io.EOF
	}

	return n, nil
}

func (s *dspan) Close() error {
	s.arena.putSpan(s)
	return nil
}

func (b *Arena) GetSpan() *dspan {
	var w bool
	for {
		m := b.FreeIndex.Load()
		if m >= b.SpanBitCnt {
			// write stat on contention - no free spans now
			if !w {
				b.Stat.WaitCnt++
				w = true
			}

			continue
		}
		i := firstzero(m)

		if i+1 > b.Stat.MaxSpan {
			b.Stat.MaxSpan = i + 1
		}

		if b.FreeIndex.CompareAndSwap(m, m^uint64(1)<<i) {
			return &dspan{
				rp:    i * b.SpanSize,
				wp:    i * b.SpanSize,
				high:  (i + 1) * b.SpanSize,
				slot:  i,
				arena: b,
			}
		}
	}
}

func (b *Arena) putSpan(c *dspan) {
	flip := uint64(1 << uint64(c.slot))
	for {
		m := b.FreeIndex.Load()
		if b.FreeIndex.CompareAndSwap(m, m&^flip) {
			return
		}
	}
}

// returns a position of the first (rightmost) unset (zero) bit
func firstzero(x uint64) int {
	x = ^x
	return popcnt((x & (-x)) - 1)
}

// count the num of populated (set to 1) bits
func popcnt(x uint64) int {
	const m1 = 0x5555555555555555
	const m2 = 0x3333333333333333
	const m4 = 0x0f0f0f0f0f0f0f0f
	const h01 = 0x0101010101010101

	x -= (x >> 1) & m1
	x = (x & m2) + ((x >> 2) & m2)
	x = (x + (x >> 4)) & m4
	return int((x * h01) >> 56)
}

// a queue (heap) for out-of-order chunks
type ChunksQueue []*Chunk

func (b *ChunksQueue) Len() int           { return len(*b) }
func (b *ChunksQueue) Less(i, j int) bool { return (*b)[i].Meta.Start < (*b)[j].Meta.Start }
func (b *ChunksQueue) Swap(i, j int)      { (*b)[i], (*b)[j] = (*b)[j], (*b)[i] }
func (b *ChunksQueue) Push(x any)         { *b = append(*b, x.(*Chunk)) }
func (b *ChunksQueue) Pop() any {
	old := *b
	n := len(old)
	x := old[n-1]
	*b = old[0 : n-1]
	return x
}

// PART READER: common concurrent download logic.

// GetChunkFunc is a provider-specific callback to download a chunk
// given an Arena and a byte range [start, end].
type GetChunkFunc func(fname string, arena *Arena, cli interface{}, start, end int64) (io.ReadCloser, error)

// GetSessFunc is an optional callback for reinitializing a session/connection.
// It returns an interface{} that the provider can interpret as needed.
type GetSessFunc func() (interface{}, error)

type chunkMeta struct {
	Start int64
	End   int64
}

type Chunk struct {
	r    io.ReadCloser
	Meta chunkMeta
}

// requests an object in chunks and retries if download has failed
type PartReader struct {
	Fname     string
	Fsize     int64 // a total size of object (file) to download
	Written   int64
	ChunkSize int64

	GetChunk GetChunkFunc
	GetSess  GetSessFunc
	L        log.LogEvent

	Buf []byte // preallocated buf for io.Copy

	taskq   chan chunkMeta
	Resultq chan Chunk
	Errc    chan error
	close   chan struct{}
}

func (pr *PartReader) Run(concurrency int, arenas []*Arena) {
	pr.taskq = make(chan chunkMeta, concurrency)
	pr.Resultq = make(chan Chunk)
	pr.Errc = make(chan error)
	pr.close = make(chan struct{})

	// schedule chunks for download
	go func() {
		for sent := int64(0); sent <= pr.Fsize; {
			select {
			case <-pr.close:
				return
			case pr.taskq <- chunkMeta{sent, sent + pr.ChunkSize - 1}:
				sent += pr.ChunkSize
			}
		}
	}()

	for i := 0; i < concurrency; i++ {
		go pr.worker(arenas[i])
	}
}

func (pr *PartReader) worker(buf *Arena) {
	sess, err := pr.GetSess()
	if err != nil {
		pr.Errc <- errors.Wrap(err, "create session")
		return
	}

	for {
		select {
		case ch := <-pr.taskq:
			r, err := pr.retryChunk(buf, sess, ch.Start, ch.End, downloadRetries)
			if err != nil {
				pr.Errc <- err
				return
			}

			pr.Resultq <- Chunk{r: r, Meta: ch}

		case <-pr.close:
			return
		}
	}
}

func (pr *PartReader) retryChunk(buf *Arena, sess interface{}, start, end int64, retries int) (io.ReadCloser, error) {
	var r io.ReadCloser
	var err error

	for i := 0; i < retries; i++ {
		r, err = pr.tryChunk(buf, sess, start, end)
		if err == nil {
			return r, nil
		}

		pr.L.Warning("retryChunk got %v, try to reconnect in %v", err, time.Second*time.Duration(i))
		time.Sleep(time.Second * time.Duration(i))
		sess, err = pr.GetSess()
		if err != nil {
			pr.L.Warning("recreate session err: %v", err)
			continue
		}
		pr.L.Info("session recreated, resuming download")
	}

	return nil, err
}

func (pr *PartReader) tryChunk(buf *Arena, sess interface{}, start, end int64) (io.ReadCloser, error) {
	// just quickly retry w/o new session in case of fail.
	// more sophisticated retry on a caller side.
	const retry = 2
	var err error

	for i := 0; i < retry; i++ {
		var r io.ReadCloser
		r, err = pr.GetChunk(pr.Fname, buf, sess, start, end)

		if err == nil || errors.Is(err, io.EOF) {
			return r, nil
		}

		if errors.Is(err, &GetObjError{}) {
			return r, err
		}

		pr.L.Warning("failed to download chunk %d-%d", start, end)
	}

	return nil, errors.Wrapf(err, "failed to download chunk %d-%d (of %d) after %d retries", start, end, pr.Fsize, retry)
}

func (pr *PartReader) Reset() {
	close(pr.close)
}

func (pr *PartReader) WriteChunk(r *Chunk, to io.Writer) error {
	if r == nil || r.r == nil {
		return nil
	}

	b, err := io.CopyBuffer(to, r.r, pr.Buf)
	pr.Written += b
	r.r.Close()

	return err
}

type GetObjError struct {
	Err error
}

func (e GetObjError) Error() string {
	return e.Err.Error()
}

func (e GetObjError) Unwap() error {
	return e.Err
}

func (GetObjError) Is(err error) bool {
	if err == nil {
		return false
	}

	_, ok := err.(GetObjError) //nolint:errorlint
	return ok
}
