package mio

import (
	"container/heap"
	"context"
	"io"
	"path"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type Download struct {
	arenas   []*storage.Arena // mem buffer for downloads
	spanSize int
	cc       int // download concurrency

	stat storage.DownloadStat
}

func (m *Minio) DownloadStat() storage.DownloadStat {
	m.d.stat.Arenas = []storage.ArenaStat{}
	for _, a := range m.d.arenas {
		m.d.stat.Arenas = append(m.d.stat.Arenas, a.Stat)
	}

	return m.d.stat
}

func (m *Minio) SourceReader(name string) (io.ReadCloser, error) {
	return m.sourceReader(name, m.d.arenas, m.d.cc, m.d.spanSize)
}

func (m *Minio) sourceReader(fname string, arenas []*storage.Arena, cc, downloadChuckSize int) (io.ReadCloser, error) {
	if cc < 1 {
		return nil, errors.Errorf("num of workers should be at least 1 (got %d)", cc)
	}
	if len(arenas) < cc {
		return nil, errors.Errorf("num of arenas (%d) less then workers (%d)", len(arenas), cc)
	}

	fstat, err := m.FileStat(fname)
	if err != nil {
		return nil, errors.Wrap(err, "get file stat")
	}

	r, w := io.Pipe()

	go func() {
		pr := m.newPartReader(fname, fstat.Size, downloadChuckSize)

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
					exitErr = errors.Wrapf(err, "SourceReader: copy bytes %d-%d from response", rs.Meta.Start, rs.Meta.End)
					return
				}

				// check if we can send something from the buffer
				for len(*cqueue) > 0 && (*cqueue)[0].Meta.Start == pr.Written {
					r := heap.Pop(cqueue).(*storage.Chunk)
					err := pr.WriteChunk(r, w)
					if err != nil {
						exitErr = errors.Wrapf(err, "SourceReader: copy bytes %d-%d from response buffer", r.Meta.Start, r.Meta.End)
						return
					}
				}

				// we've read all bytes in the object
				if pr.Written >= pr.Fsize {
					return
				}

			case err := <-pr.Errc:
				exitErr = errors.Wrapf(err, "SourceReader: download '%s/%s'", m.cfg.Bucket, fname)
				return
			}
		}
	}()

	return r, nil
}

func (m *Minio) newPartReader(fname string, fsize int64, chunkSize int) *storage.PartReader {
	return &storage.PartReader{
		Fname:     fname,
		Fsize:     fsize,
		ChunkSize: int64(chunkSize),
		Buf:       make([]byte, 32*1024),
		L:         m.log,
		GetChunk: func(fname string, arena *storage.Arena, _ any, start, end int64) (io.ReadCloser, error) {
			return m.getPartialObject(fname, arena, start, end-start+1)
		},
		GetSess: func() (any, error) {
			return m.cl, nil // re-use the already-initialized client
		},
	}
}

func (m *Minio) getPartialObject(name string, buf *storage.Arena, start, length int64) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()

	objectName := path.Join(m.cfg.Prefix, name)

	opts := minio.GetObjectOptions{}
	err := opts.SetRange(start, start+length-1)
	if err != nil {
		return nil, errors.Wrap(err, "failed to set range on GetObjectOptions")
	}

	object, err := m.cl.GetObject(ctx, m.cfg.Bucket, objectName, opts)
	if err != nil {
		respErr := minio.ToErrorResponse(err)
		if respErr.Code == "NoSuchKey" || respErr.Code == "InvalidRange" {
			return nil, io.EOF
		}

		return nil, storage.GetObjError{Err: err}
	}
	defer object.Close()

	ch := buf.GetSpan()
	_, err = io.CopyBuffer(ch, object, buf.CpBuf)
	if err != nil {
		ch.Close()
		return nil, errors.Wrap(err, "copy")
	}

	return ch, nil
}
