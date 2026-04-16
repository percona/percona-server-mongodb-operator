package s3

import (
	"container/heap"
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
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

// Download is used to concurrently download objects from the storage.
type Download struct {
	arenas   []*storage.Arena // mem buffer for downloads
	spanSize int
	cc       int // download concurrency

	stat storage.DownloadStat
}

func (s *S3) DownloadStat() storage.DownloadStat {
	s.d.stat.Arenas = []storage.ArenaStat{}
	for _, a := range s.d.arenas {
		s.d.stat.Arenas = append(s.d.stat.Arenas, a.Stat)
	}

	return s.d.stat
}

func (s *S3) SourceReader(name string) (io.ReadCloser, error) {
	return s.sourceReader(name, s.d.arenas, s.d.cc, s.d.spanSize)
}

func (s *S3) newPartReader(fname string, fsize int64, chunkSize int) *storage.PartReader {
	return &storage.PartReader{
		Fname:     fname,
		Fsize:     fsize,
		ChunkSize: int64(chunkSize),
		Buf:       make([]byte, 32*1024),
		L:         s.log,
		GetChunk: func(fname string, arena *storage.Arena, cli any, start, end int64) (io.ReadCloser, error) {
			s3cli, ok := cli.(*s3.Client)
			if !ok {
				return nil, errors.Errorf("expected *s3.Client, got %T", cli)
			}
			return s.getChunk(fname, arena, s3cli, start, end)
		},
		GetSess: func() (any, error) {
			cli, err := s.s3client()
			if err != nil {
				return nil, err
			}
			return cli, nil
		},
	}
}

func (s *S3) sourceReader(fname string, arenas []*storage.Arena, cc, downloadChuckSize int) (io.ReadCloser, error) {
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

		cqueue := &storage.ChunksQueue{}
		heap.Init(cqueue)

		for {
			select {
			case rs := <-pr.Resultq:
				// Although chunks are requested concurrently they must be written sequentially
				// to the destination as it is not necessary a file (decompress, mongorestore etc.).
				// If it is not its turn (previous chunks weren't written yet) the chunk will be
				// added to the buffer to wait. If the buffer grows too much the scheduling of new
				// chunks will be paused for buffer to be handled.
				if rs.Meta.Start != pr.Written {
					heap.Push(cqueue, &rs)
					continue
				}

				err := pr.WriteChunk(&rs, w)
				if err != nil {
					exitErr = errors.Wrapf(err, "SourceReader: copy bytes %d-%d from resoponse", rs.Meta.Start, rs.Meta.End)
					return
				}

				// check if we can send something from the buffer
				for len(*cqueue) > 0 && (*cqueue)[0].Meta.Start == pr.Written {
					r := heap.Pop(cqueue).(*storage.Chunk)
					err := pr.WriteChunk(r, w)
					if err != nil {
						exitErr = errors.Wrapf(err, "SourceReader: copy bytes %d-%d from resoponse buffer", r.Meta.Start, r.Meta.End)
						return
					}
				}

				// we've read all bytes in the object
				if pr.Written >= pr.Fsize {
					return
				}

			case err := <-pr.Errc:
				exitErr = errors.Wrapf(err, "SourceReader: download '%s/%s'", s.opts.Bucket, fname)
				return
			}
		}
	}()

	return r, nil
}

func (s *S3) getChunk(fname string, buf *storage.Arena, cli *s3.Client, start, end int64) (io.ReadCloser, error) {
	getObjOpts := &s3.GetObjectInput{
		Bucket: aws.String(s.opts.Bucket),
		Key:    aws.String(path.Join(s.opts.Prefix, fname)),
		Range:  aws.String(fmt.Sprintf("bytes=%d-%d", start, end)),
	}

	sse := s.opts.ServerSideEncryption
	if sse != nil && sse.SseCustomerAlgorithm != "" {
		getObjOpts.SSECustomerAlgorithm = aws.String(sse.SseCustomerAlgorithm)
		decodedKey, err := base64.StdEncoding.DecodeString(sse.SseCustomerKey)
		getObjOpts.SSECustomerKey = aws.String(sse.SseCustomerKey)
		if err != nil {
			return nil, errors.Wrap(err, "SseCustomerAlgorithm specified with invalid SseCustomerKey")
		}
		keyMD5 := md5.Sum(decodedKey)
		getObjOpts.SSECustomerKeyMD5 = aws.String(base64.StdEncoding.EncodeToString(keyMD5[:]))
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()

	s3obj, err := cli.GetObject(ctx, getObjOpts)
	if err != nil {
		// if object size is undefined, we would read
		// until HTTP code 416 (Requested Range Not Satisfiable)
		var re *smithyhttp.ResponseError
		if errors.As(err, &re) && re.Err != nil && re.Response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			return nil, io.EOF
		}

		s.log.Warning("errGetObj Err: %v", err)
		return nil, storage.GetObjError{Err: err}
	}
	defer s3obj.Body.Close()

	if sse != nil {
		if sse.SseAlgorithm == string(types.ServerSideEncryptionAwsKms) {
			s3obj.ServerSideEncryption = types.ServerSideEncryptionAwsKms
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

	ch := buf.GetSpan()
	_, err = io.CopyBuffer(ch, s3obj.Body, buf.CpBuf)
	if err != nil {
		ch.Close()
		return nil, errors.Wrap(err, "copy")
	}
	return ch, nil
}
