package gcs

import (
	"container/heap"
	"io"
	"net/http"

	"google.golang.org/api/googleapi"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type Download struct {
	arenas   []*storage.Arena // mem buffer for downloads
	spanSize int
	cc       int // download concurrency

	stat storage.DownloadStat
}

func (g *GCS) DownloadStat() storage.DownloadStat {
	g.d.stat.Arenas = []storage.ArenaStat{}
	for _, a := range g.d.arenas {
		g.d.stat.Arenas = append(g.d.stat.Arenas, a.Stat)
	}

	return g.d.stat
}

func (g *GCS) SourceReader(name string) (io.ReadCloser, error) {
	return g.sourceReader(name, g.d.arenas, g.d.cc, g.d.spanSize)
}

func (g *GCS) newPartReader(fname string, fsize int64, chunkSize int) *storage.PartReader {
	return &storage.PartReader{
		Fname:     fname,
		Fsize:     fsize,
		ChunkSize: int64(chunkSize),
		Buf:       make([]byte, 32*1024),
		L:         g.log,
		GetChunk: func(fname string, arena *storage.Arena, cli interface{}, start, end int64) (io.ReadCloser, error) {
			return cli.(gcsClient).getPartialObject(fname, arena, start, end-start+1)
		},
		GetSess: func() (interface{}, error) {
			return g.client, nil // re-use the already-initialized client
		},
	}
}

func (g *GCS) sourceReader(fname string, arenas []*storage.Arena, cc, downloadChuckSize int) (io.ReadCloser, error) {
	if cc < 1 {
		return nil, errors.Errorf("num of workers shuld be at least 1 (got %d)", cc)
	}
	if len(arenas) < cc {
		return nil, errors.Errorf("num of arenas (%d) less then workers (%d)", len(arenas), cc)
	}

	fstat, err := g.FileStat(fname)
	if err != nil {
		return nil, errors.Wrap(err, "get file stat")
	}

	r, w := io.Pipe()

	go func() {
		pr := g.newPartReader(fname, fstat.Size, downloadChuckSize)

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
				exitErr = errors.Wrapf(err, "SourceReader: download '%s/%s'", g.cfg.Bucket, fname)
				return
			}
		}
	}()

	return r, nil
}

func isRangeNotSatisfiable(err error) bool {
	var herr *googleapi.Error
	if errors.As(err, &herr) {
		if herr.Code == http.StatusRequestedRangeNotSatisfiable {
			return true
		}
	}
	return false
}
