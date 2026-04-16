package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type Results struct {
	Size Byte
	Time time.Duration
}

func (r Results) String() string {
	return fmt.Sprintf("%v sent in %s.\nAvg upload rate = %.2fMB/s.",
		r.Size, r.Time.Round(time.Second), float64(r.Size/MB)/r.Time.Seconds())
}

type Byte float64

const (
	_       = iota
	KB Byte = 1 << (10 * iota)
	MB
	GB
	TB
)

func (b Byte) String() string {
	switch {
	case b >= TB:
		return fmt.Sprintf("%.2fTB", b/TB)
	case b >= GB:
		return fmt.Sprintf("%.2fGB", b/GB)
	case b >= MB:
		return fmt.Sprintf("%.2fMB", b/MB)
	case b >= KB:
		return fmt.Sprintf("%.2fKB", b/KB)
	}
	return fmt.Sprintf("%.2fB", b)
}

type Rand struct {
	size    Byte
	dataset [][]byte
}

func NewRand(size Byte) *Rand {
	r := &Rand{
		size:    size,
		dataset: make([][]byte, len(dataset)),
	}
	for i, s := range dataset {
		r.dataset[i] = []byte(s)
	}
	return r
}

func (r *Rand) WriteTo(w io.Writer) (int64, error) {
	var written int64
	for i := 0; written < int64(r.size); i++ {
		n, err := w.Write(r.dataset[i%len(dataset)])
		if err != nil {
			return written, err
		}
		written += int64(n)
	}
	return written, nil
}

type Collection struct {
	size Byte
	c    *mongo.Collection
}

func NewCollection(size Byte, cn *mongo.Client, namespace string) (*Collection, error) {
	ns := strings.SplitN(namespace, ".", 2)
	if len(ns) != 2 {
		return nil, errors.New("namespace should be in format `database.collection`")
	}

	r := &Collection{
		size: size,
		c:    cn.Database(ns[0]).Collection(ns[1]),
	}
	return r, nil
}

func (c *Collection) WriteTo(w io.Writer) (int64, error) {
	ctx := context.Background()

	cur, err := c.c.Find(ctx, bson.M{})
	if err != nil {
		return 0, errors.Wrap(err, "create cursor")
	}
	defer cur.Close(ctx)

	var written int64
	for cur.Next(ctx) {
		n, err := w.Write(cur.Current)
		if err != nil {
			return written, errors.Wrap(err, "write")
		}
		written += int64(n)
		if written >= int64(c.size) {
			return written, nil
		}
	}

	return written, errors.Wrap(cur.Err(), "cursor")
}

const fileName = "pbmSpeedTest"

func doTest(
	nodeCN *mongo.Client,
	stg storage.Storage,
	compression compress.CompressionType,
	level *int,
	sizeGb float64,
	collection string,
) (*Results, error) {
	var src storage.Source
	var err error
	if collection != "" {
		src, err = NewCollection(Byte(sizeGb)*GB, nodeCN, collection)
		if err != nil {
			return nil, errors.Wrap(err, "create source")
		}
	} else {
		src = NewRand(Byte(sizeGb) * GB)
	}

	r := &Results{}
	ts := time.Now()
	size, err := storage.Upload(context.Background(), src, stg, compression, level, fileName, -1)
	r.Size = Byte(size)
	if err != nil {
		return nil, errors.Wrap(err, "upload")
	}
	r.Time = time.Since(ts)

	return r, nil
}
