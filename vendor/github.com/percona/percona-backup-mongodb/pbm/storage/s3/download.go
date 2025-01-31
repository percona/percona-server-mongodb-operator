package s3

import (
	"container/heap"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
)

// Downloading objects from the storage.
//
// Each object can be downloaded concurrently in chunks. If a download of a
// chunk has failed it will be retried a certain amount of time before
// returning with an error.
// It starts with the number of workers equal to the concurrency setting. Each
// worker takes a task with a needed object range (chunk) and downloads it into
// a part (span) of its memory buffer (arena). Returns an io.ReaderCloser
// object with the content of the span. And gets a next free span to download
// the next chunk.
// The consumer closing io.ReaderCloser marks the respective span as free reuse.
// An arenas pool is created with the `Download` object and reused for every next
// downloaded object.
// Although the object's chunks can be downloaded concurrently, they should be
// streamed to the consumer sequentially (objects usually are compressed, hence
// the consumer can't be an oi.Seeker). Therefore if a downloaded span's range
// is out of order (preceding chunks aren't downloaded yet) it is added to the
// heap structure (`chunksQueue`) and waits for its queue to be passed to
// the consumer.
// The max size the buffer of would be `arenaSize * concurrency`. Where
// `arenaSize` is `spanSize * spansInArena`. It doesn't mean all of this size
// would be allocated as some of the span slots may remain unused.

const (
	downloadChuckSizeDefault = 8 << 20
	downloadRetries          = 10

	ccSpanDefault = 32 << 20
	arenaSpans    = 8 // an amount of spans in arena
)

type DownloadStat struct {
	Arenas      []ArenaStat `bson:"a" json:"a"`
	Concurrency int         `bson:"cc" json:"cc"`
	ArenaSize   int         `bson:"arSize" json:"arSize"`
	SpansNum    int         `bson:"spanNum" json:"spanNum"`
	SpanSize    int         `bson:"spanSize" json:"spanSize"`
	BufSize     int         `bson:"bufSize" json:"bufSize"`
}

func (s DownloadStat) String() string {
	return fmt.Sprintf("buf %d, arena %d, span %d, spanNum %d, cc %d, %v",
		s.BufSize, s.ArenaSize, s.SpanSize, s.SpansNum, s.Concurrency, s.Arenas)
}

// Download is used to concurrently download objects from the storage.
type Download struct {
	s3 *S3

	arenas   []*arena // mem buffer for downloads
	spanSize int
	cc       int // download concurrency

	stat DownloadStat
}

func (s *S3) NewDownload(cc, bufSizeMb, spanSizeMb int) *Download {
	arenaSize, spanSize, cc := downloadOpts(cc, bufSizeMb, spanSizeMb)
	s.log.Debug("download max buf %d (arena %d, span %d, concurrency %d)", arenaSize*cc, arenaSize, spanSize, cc)

	arenas := []*arena{}
	for i := 0; i < cc; i++ {
		arenas = append(arenas, newArena(arenaSize, spanSize))
	}

	return &Download{
		s3:       s,
		arenas:   arenas,
		spanSize: spanSize,
		cc:       cc,

		stat: DownloadStat{
			Concurrency: cc,
			ArenaSize:   arenaSize,
			SpansNum:    arenaSize / spanSize,
			SpanSize:    spanSize,
			BufSize:     arenaSize * cc,
		},
	}
}

// assume we need more spans in arena above this number of CPUs used
const lowCPU = 8

// Adjust download options. We go from spanSize. But if bufMaxMb is
// set, it will be a hard limit on total memory.
//
//nolint:nonamedreturns
func downloadOpts(cc, bufMaxMb, spanSizeMb int) (arenaSize, span, c int) {
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

func (d *Download) SourceReader(name string) (io.ReadCloser, error) {
	return d.s3.sourceReader(name, d.arenas, d.cc, d.spanSize)
}

func (d *Download) Stat() DownloadStat {
	d.stat.Arenas = []ArenaStat{}
	for _, a := range d.arenas {
		d.stat.Arenas = append(d.stat.Arenas, a.stat)
	}

	return d.stat
}

func (s *S3) SourceReader(name string) (io.ReadCloser, error) {
	return s.d.SourceReader(name)
}

type getObjError struct {
	Err error
}

func (e getObjError) Error() string {
	return e.Err.Error()
}

func (e getObjError) Unwap() error {
	return e.Err
}

func (getObjError) Is(err error) bool {
	if err == nil {
		return false
	}

	_, ok := err.(getObjError) //nolint:errorlint
	return ok
}

// requests an object in chunks and retries if download has failed
type partReader struct {
	fname     string
	fsize     int64 // a total size of object (file) to download
	written   int64
	chunkSize int64

	getSess func() (*s3.S3, error)
	l       log.LogEvent
	opts    *Config
	buf     []byte // preallocated buf for io.Copy

	taskq   chan chunkMeta
	resultq chan chunk
	errc    chan error
	close   chan struct{}
}

func (s *S3) newPartReader(fname string, fsize int64, chunkSize int) *partReader {
	return &partReader{
		l:         s.log,
		buf:       make([]byte, 32*1024),
		opts:      s.opts,
		fname:     fname,
		fsize:     fsize,
		chunkSize: int64(chunkSize),
		getSess: func() (*s3.S3, error) {
			sess, err := s.s3session()
			if err != nil {
				return nil, err
			}
			sess.Client.Config.HTTPClient.Timeout = time.Second * 60
			return sess, nil
		},
	}
}

type chunkMeta struct {
	start int64
	end   int64
}

type chunk struct {
	r    io.ReadCloser
	meta chunkMeta
}

// a queue (heap) for out-of-order chunks
type chunksQueue []*chunk

func (b chunksQueue) Len() int           { return len(b) }
func (b chunksQueue) Less(i, j int) bool { return b[i].meta.start < b[j].meta.start }
func (b chunksQueue) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }
func (b *chunksQueue) Push(x any)        { *b = append(*b, x.(*chunk)) }
func (b *chunksQueue) Pop() any {
	old := *b
	n := len(old)
	x := old[n-1]
	*b = old[0 : n-1]
	return x
}

func (s *S3) sourceReader(fname string, arenas []*arena, cc, downloadChuckSize int) (io.ReadCloser, error) {
	if cc < 1 {
		return nil, errors.Errorf("num of workers shuld be at least 1 (got %d)", cc)
	}
	if len(arenas) < cc {
		return nil, errors.Errorf("num of arenas (%d) less then workers (%d)", len(arenas), cc)
	}

	fstat, err := s.FileStat(fname)
	if err != nil {
		return nil, errors.Wrap(err, "get file stat")
	}

	r, w := io.Pipe()

	go func() {
		pr := s.newPartReader(fname, fstat.Size, downloadChuckSize)

		pr.Run(cc, arenas)

		exitErr := io.EOF
		defer func() {
			w.CloseWithError(exitErr)
			pr.Reset()
		}()

		cqueue := &chunksQueue{}
		heap.Init(cqueue)

		for {
			select {
			case rs := <-pr.resultq:
				// Although chunks are requested concurrently they must be written sequentially
				// to the destination as it is not necessary a file (decompress, mongorestore etc.).
				// If it is not its turn (previous chunks weren't written yet) the chunk will be
				// added to the buffer to wait. If the buffer grows too much the scheduling of new
				// chunks will be paused for buffer to be handled.
				if rs.meta.start != pr.written {
					heap.Push(cqueue, &rs)
					continue
				}

				err := pr.writeChunk(&rs, w)
				if err != nil {
					exitErr = errors.Wrapf(err, "SourceReader: copy bytes %d-%d from resoponse", rs.meta.start, rs.meta.end)
					return
				}

				// check if we can send something from the buffer
				for len(*cqueue) > 0 && []*chunk(*cqueue)[0].meta.start == pr.written {
					r := heap.Pop(cqueue).(*chunk)
					err := pr.writeChunk(r, w)
					if err != nil {
						exitErr = errors.Wrapf(err, "SourceReader: copy bytes %d-%d from resoponse buffer", r.meta.start, r.meta.end)
						return
					}
				}

				// we've read all bytes in the object
				if pr.written >= pr.fsize {
					return
				}

			case err := <-pr.errc:
				exitErr = errors.Wrapf(err, "SourceReader: download '%s/%s'", s.opts.Bucket, fname)
				return
			}
		}
	}()

	return r, nil
}

func (pr *partReader) Run(concurrency int, arenas []*arena) {
	pr.taskq = make(chan chunkMeta, concurrency)
	pr.resultq = make(chan chunk)
	pr.errc = make(chan error)
	pr.close = make(chan struct{})

	// schedule chunks for download
	go func() {
		for sent := int64(0); sent <= pr.fsize; {
			select {
			case <-pr.close:
				return
			case pr.taskq <- chunkMeta{sent, sent + pr.chunkSize - 1}:
				sent += pr.chunkSize
			}
		}
	}()

	for i := 0; i < concurrency; i++ {
		go pr.worker(arenas[i])
	}
}

func (pr *partReader) Reset() {
	close(pr.close)
}

func (pr *partReader) writeChunk(r *chunk, to io.Writer) error {
	if r == nil || r.r == nil {
		return nil
	}

	b, err := io.CopyBuffer(to, r.r, pr.buf)
	pr.written += b
	r.r.Close()

	return err
}

func (pr *partReader) worker(buf *arena) {
	sess, err := pr.getSess()
	if err != nil {
		pr.errc <- errors.Wrap(err, "create session")
		return
	}

	for {
		select {
		case ch := <-pr.taskq:
			r, err := pr.retryChunk(buf, sess, ch.start, ch.end, downloadRetries)
			if err != nil {
				pr.errc <- err
				return
			}

			pr.resultq <- chunk{r: r, meta: ch}

		case <-pr.close:
			return
		}
	}
}

func (pr *partReader) retryChunk(buf *arena, s *s3.S3, start, end int64, retries int) (io.ReadCloser, error) {
	var r io.ReadCloser
	var err error

	for i := 0; i < retries; i++ {
		r, err = pr.tryChunk(buf, s, start, end)
		if err == nil {
			return r, nil
		}

		pr.l.Warning("retryChunk got %v, try to reconnect in %v", err, time.Second*time.Duration(i))
		time.Sleep(time.Second * time.Duration(i))
		s, err = pr.getSess()
		if err != nil {
			pr.l.Warning("recreate session err: %v", err)
			continue
		}
		pr.l.Info("session recreated, resuming download")
	}

	return nil, err
}

func (pr *partReader) tryChunk(buf *arena, s *s3.S3, start, end int64) (io.ReadCloser, error) {
	// just quickly retry w/o new session in case of fail.
	// more sophisticated retry on a caller side.
	const retry = 2
	var err error
	for i := 0; i < retry; i++ {
		var r io.ReadCloser
		r, err = pr.getChunk(buf, s, start, end)

		if err == nil || errors.Is(err, io.EOF) {
			return r, nil
		}

		if errors.Is(err, &getObjError{}) {
			return r, err
		}

		pr.l.Warning("failed to download chunk %d-%d", start, end)
	}

	return nil, errors.Wrapf(err, "failed to download chunk %d-%d (of %d) after %d retries", start, end, pr.fsize, retry)
}

func (pr *partReader) getChunk(buf *arena, s *s3.S3, start, end int64) (io.ReadCloser, error) {
	getObjOpts := &s3.GetObjectInput{
		Bucket: aws.String(pr.opts.Bucket),
		Key:    aws.String(path.Join(pr.opts.Prefix, pr.fname)),
		Range:  aws.String(fmt.Sprintf("bytes=%d-%d", start, end)),
	}

	sse := pr.opts.ServerSideEncryption
	if sse != nil && sse.SseCustomerAlgorithm != "" {
		getObjOpts.SSECustomerAlgorithm = aws.String(sse.SseCustomerAlgorithm)
		decodedKey, err := base64.StdEncoding.DecodeString(sse.SseCustomerKey)
		getObjOpts.SSECustomerKey = aws.String(string(decodedKey))
		if err != nil {
			return nil, errors.Wrap(err, "SseCustomerAlgorithm specified with invalid SseCustomerKey")
		}
		keyMD5 := md5.Sum(decodedKey)
		getObjOpts.SSECustomerKeyMD5 = aws.String(base64.StdEncoding.EncodeToString(keyMD5[:]))
	}

	s3obj, err := s.GetObject(getObjOpts)
	if err != nil {
		// if object size is undefined, we would read
		// until HTTP code 416 (Requested Range Not Satisfiable)
		rerr, ok := err.(awserr.RequestFailure) //nolint:errorlint
		if ok && rerr.StatusCode() == http.StatusRequestedRangeNotSatisfiable {
			return nil, io.EOF
		}

		pr.l.Warning("errGetObj Err: %v", err)
		return nil, getObjError{err}
	}
	defer s3obj.Body.Close()

	if sse != nil {
		if sse.SseAlgorithm == s3.ServerSideEncryptionAwsKms {
			s3obj.ServerSideEncryption = aws.String(sse.SseAlgorithm)
			s3obj.SSEKMSKeyId = aws.String(sse.KmsKeyID)
		} else if sse.SseCustomerAlgorithm != "" {
			s3obj.SSECustomerAlgorithm = aws.String(sse.SseCustomerAlgorithm)
			decodedKey, _ := base64.StdEncoding.DecodeString(sse.SseCustomerKey)
			// We don't pass in the key in this case, just the MD5 hash of the key
			// for verification
			// s3obj.SSECustomerKey = aws.String(string(decodedKey))
			keyMD5 := md5.Sum(decodedKey)
			s3obj.SSECustomerKeyMD5 = aws.String(base64.StdEncoding.EncodeToString(keyMD5[:]))
		}
	}

	ch := buf.getSpan()
	_, err = io.CopyBuffer(ch, s3obj.Body, buf.cpbuf)
	if err != nil {
		ch.Close()
		return nil, errors.Wrap(err, "copy")
	}
	return ch, nil
}

// Download arena (bytes slice) is split into spans (represented by `dpsan`)
// whose size should be equal to download chunks. `dspan` implements io.Wrire
// and io.ReaderCloser interface. Close() marks the span as free to use
// (download another chunk).
// Free/busy spans list is managed via lock-free bitmap index.
type arena struct {
	buf        []byte
	spansize   int
	spanBitCnt uint64
	freeindex  atomic.Uint64 // free slots bitmap

	stat ArenaStat

	cpbuf []byte // preallocated buffer for io.Copy
}

type ArenaStat struct {
	// the max amount of span was occupied simultaneously
	MaxSpan int `bson:"MaxSpan" json:"MaxSpan"`
	// how many times getSpan() was waiting for the free span
	WaitCnt int `bson:"WaitCnt" json:"WaitCnt"`
}

func newArena(size, spansize int) *arena {
	snum := size / spansize

	size = spansize * snum
	return &arena{
		buf:        make([]byte, size),
		spansize:   spansize,
		spanBitCnt: 1<<(size/spansize) - 1,
		cpbuf:      make([]byte, 32*1024),
	}
}

func (b *arena) getSpan() *dspan {
	var w bool
	for {
		m := b.freeindex.Load()
		if m >= b.spanBitCnt {
			// write stat on contention - no free spans now
			if !w {
				b.stat.WaitCnt++
				w = true
			}

			continue
		}
		i := firstzero(m)

		if i+1 > b.stat.MaxSpan {
			b.stat.MaxSpan = i + 1
		}

		if b.freeindex.CompareAndSwap(m, m^uint64(1)<<i) {
			return &dspan{
				rp:    i * b.spansize,
				wp:    i * b.spansize,
				high:  (i + 1) * b.spansize,
				slot:  i,
				arena: b,
			}
		}
	}
}

func (b *arena) putSpan(c *dspan) {
	flip := uint64(1 << uint64(c.slot))
	for {
		m := b.freeindex.Load()
		if b.freeindex.CompareAndSwap(m, m&^flip) {
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

type dspan struct {
	rp   int // current read pos in the arena
	wp   int // current write pos in the arena
	high int // high bound index of span in the arena

	slot  int    // slot number in the arena
	arena *arena // link to the arena
}

func (s *dspan) Write(p []byte) (int, error) {
	n := copy(s.arena.buf[s.wp:s.high], p)

	s.wp += n
	return n, nil
}

func (s *dspan) Read(p []byte) (int, error) {
	n := copy(p, s.arena.buf[s.rp:s.wp])
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
