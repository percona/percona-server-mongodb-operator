package main

import (
	"context"
	"fmt"
	stdlog "log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/sync/errgroup"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/util"
)

var logger = stdlog.New(os.Stdout, "", stdlog.Ltime)

type rootOpts struct {
	mURL  string
	fromS string
	tillS string
}

func main() {
	rootOptions := rootOpts{}
	rootCmd := &cobra.Command{
		Use:          "ensure-oplog",
		Short:        "ensure oplog chunks",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if rootOptions.mURL == "" {
				rootOptions.mURL = viper.GetString("mongodb-uri")
			}

			fromTS, err := parseTS(rootOptions.fromS)
			if err != nil {
				return errors.Wrap(err, "parse from time")
			}
			tillTS, err := parseTS(rootOptions.tillS)
			if err != nil {
				return errors.Wrap(err, "parse till time")
			}

			t, err := connTopo(ctx, rootOptions.mURL)
			if err != nil {
				return errors.Wrap(err, "getTopo")
			}

			switch t {
			case topoMongos:
				err = ensureClusterOplog(ctx, rootOptions.mURL, fromTS, tillTS)
			case topoReplset:
				err = ensureReplsetOplog(ctx, rootOptions.mURL, fromTS, tillTS)
			default:
				err = errors.New("unsupported connection")
			}

			if err != nil {
				return err
			}

			return nil
		},
	}

	rootCmd.PersistentFlags().StringVar(&rootOptions.mURL, "mongodb-uri", "", "MongoDB connection string")
	_ = viper.BindPFlag("mongodb-uri", rootCmd.PersistentFlags().Lookup("mongodb-uri"))
	_ = viper.BindEnv("mongodb-uri", "PBM_MONGODB_URI")

	rootCmd.Flags().StringVar(&rootOptions.fromS, "from", "", "first op time")
	rootCmd.Flags().StringVar(&rootOptions.tillS, "till", "", "last op time")

	if err := rootCmd.Execute(); err != nil {
		stdlog.Fatal(err)
	}
}

type topo int

const (
	topoUnknown = topo(iota)
	topoMongos
	topoReplset
)

func connTopo(ctx context.Context, uri string) (topo, error) {
	m, err := connect.MongoConnect(ctx, uri)
	if err != nil {
		return topoUnknown, errors.Wrap(err, "connect")
	}
	defer m.Disconnect(context.Background()) // nolint:errcheck

	r, err := sayHello(ctx, m)
	if err != nil {
		return topoUnknown, errors.Wrap(err, "getShortHello")
	}

	switch {
	case r.Msg == "isdbgrid":
		return topoMongos, nil
	case r.SetName != "":
		return topoReplset, nil
	}

	return topoUnknown, nil
}

func parseTS(t string) (primitive.Timestamp, error) {
	var ts primitive.Timestamp
	if len(t) == 0 {
		return ts, nil
	}

	if tt, ii, ok := strings.Cut(t, ","); ok {
		t, err := strconv.ParseUint(tt, 10, 32)
		if err != nil {
			return ts, errors.Wrap(err, "parse clusterTime T")
		}
		i, err := strconv.ParseUint(ii, 10, 32)
		if err != nil {
			return ts, errors.Wrap(err, "parse clusterTime I")
		}

		ts.T = uint32(t)
		ts.I = uint32(i)
		return ts, nil
	}

	const datetimeFormat = "2006-01-02T15:04:05"
	const dateFormat = "2006-01-02"

	var tsto time.Time
	var err error
	switch len(t) {
	case len(datetimeFormat):
		tsto, err = time.Parse(datetimeFormat, t)
	case len(dateFormat):
		tsto, err = time.Parse(dateFormat, t)
	default:
		err = errors.New("invalid format")
	}

	if err != nil {
		return ts, errors.Wrap(err, "parse date")
	}

	ts.T = uint32(tsto.Unix())
	return ts, nil
}

type hello struct {
	Msg     string
	SetName string
}

func sayHello(ctx context.Context, m *mongo.Client) (*hello, error) {
	res := m.Database("admin").RunCommand(ctx, bson.D{{"hello", 1}})
	if err := res.Err(); err != nil {
		return nil, errors.Wrap(err, "query")
	}

	var r *hello
	err := res.Decode(&r)
	return r, errors.Wrap(err, "decode")
}

func ensureClusterOplog(ctx context.Context, uri string, from, till primitive.Timestamp) error {
	logger.Printf("[%s] ensuring cluster oplog: %s - %s",
		uri, formatTimestamp(from), formatTimestamp(from))

	m, err := connect.MongoConnect(ctx, uri)
	if err != nil {
		return errors.Wrap(err, "connect")
	}
	defer m.Disconnect(context.Background()) // nolint:errcheck

	res := m.Database("admin").RunCommand(ctx, bson.D{{"getShardMap", 1}})
	if err := res.Err(); err != nil {
		return errors.Wrap(err, "getShardMap: query")
	}

	var r struct{ ConnStrings map[string]string }
	if err := res.Decode(&r); err != nil {
		return errors.Wrap(err, "getShardMap: decode")
	}

	eg, gc := errgroup.WithContext(ctx)
	for hosts := range r.ConnStrings {
		id, rsURI, _ := strings.Cut(hosts, "/")

		eg.Go(func() error {
			err := ensureReplsetOplog(gc, rsURI, from, till)
			return errors.Wrapf(err, "[%s] ensure oplog", id)
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}

	logger.Printf("[%s] ensured cluster oplog: %s - %s",
		uri, formatTimestamp(from), formatTimestamp(from))

	return nil
}

func ensureReplsetOplog(ctx context.Context, uri string, from, till primitive.Timestamp) error {
	logger.Printf("[%s] ensure replset oplog: %s - %s",
		uri, formatTimestamp(from), formatTimestamp(from))

	m, err := connect.MongoConnect(ctx, uri)
	if err != nil {
		return errors.Wrap(err, "connect")
	}

	info, err := sayHello(ctx, m)
	if err != nil {
		return errors.Wrap(err, "get node info")
	}
	if info.SetName == "" {
		return errors.New("cannot ensure oplog in standalone mode")
	}

	firstOpT, err := findPreviousOplogTS(ctx, m, from)
	if err != nil {
		return errors.Wrap(err, "lookup first oplog record")
	}

	lastOpT, err := findFollowingOplogTS(ctx, m, till)
	if err != nil {
		return errors.Wrap(err, "lookup first oplog record")
	}

	logger.Printf("[%s] ensuring replset oplog (actual): %s - %s",
		uri, formatTimestamp(firstOpT), formatTimestamp(lastOpT))

	conn, err := connect.Connect(ctx, uri, "ensure-oplog")
	if err != nil {
		return errors.Wrap(err, "connect to PBM")
	}

	chunks, err := oplog.PITRGetChunksSlice(ctx, conn, info.SetName, firstOpT, lastOpT)
	if err != nil {
		return errors.Wrap(err, "get chunks")
	}

	missedChunks := findChunkRanges(chunks, firstOpT, lastOpT)
	if len(missedChunks) == 0 {
		logger.Printf("[%s] no missed chunk: %s - %s",
			uri, formatTimestamp(firstOpT), formatTimestamp(lastOpT))
		return nil
	}

	cfg, err := config.GetConfig(ctx, conn)
	if err != nil {
		return errors.Wrap(err, "get config")
	}

	stg, err := util.StorageFromConfig(&cfg.Storage, "", log.FromContext(ctx).NewDefaultEvent())
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	compression := defs.DefaultCompression
	compressionLevel := (*int)(nil)
	if cfg.PITR != nil {
		compression = compress.CompressionType(cfg.PITR.Compression)
		compressionLevel = cfg.PITR.CompressionLevel
	}

	for _, t := range missedChunks {
		logger.Printf("[%s] ensure missed chunk: %s - %s",
			uri, formatTimestamp(t.from), formatTimestamp(t.till))

		filename := oplog.FormatChunkFilepath(info.SetName, t.from, t.till, compression)
		o := oplog.NewOplogBackup(m)
		o.SetTailingSpan(t.from, t.till)

		n, err := storage.Upload(ctx, o, stg, compression, compressionLevel, filename, -1)
		if err != nil {
			return errors.Wrapf(err, "failed to upload %s - %s chunk",
				formatTimestamp(t.from), formatTimestamp(t.till))
		}

		logger.Printf("[%s] uploaded chunk: %s - %s (%d bytes)",
			uri, formatTimestamp(t.from), formatTimestamp(t.till), n)

		meta := oplog.OplogChunk{
			RS:          info.SetName,
			FName:       filename,
			Compression: compression,
			StartTS:     t.from,
			EndTS:       t.till,
		}

		if err := oplog.PITRAddChunk(ctx, conn, meta); err != nil {
			return errors.Wrapf(err, "failed to save %s - %s chunk meta",
				formatTimestamp(t.from), formatTimestamp(t.till))
		}

		logger.Printf("[%s] saved chunk meta: %s - %s",
			uri, formatTimestamp(t.from), formatTimestamp(t.till))
	}

	logger.Printf("[%s] ensured replset oplog: %s - %s",
		uri, formatTimestamp(firstOpT), formatTimestamp(lastOpT))

	return nil
}

func findPreviousOplogTS(ctx context.Context, m *mongo.Client, ts primitive.Timestamp) (primitive.Timestamp, error) {
	f := bson.M{}
	if !ts.IsZero() {
		f["ts"] = bson.M{"$lte": ts}
	}
	o := options.FindOne().SetSort(bson.D{{"ts", 1}})
	res := m.Database("local").Collection("oplog.rs").FindOne(ctx, f, o)
	return findOplogTSHelper(res)
}

func findFollowingOplogTS(ctx context.Context, m *mongo.Client, ts primitive.Timestamp) (primitive.Timestamp, error) {
	f := bson.M{}
	if !ts.IsZero() {
		f["ts"] = bson.M{"$gte": ts}
	}
	o := options.FindOne().SetSort(bson.D{{"ts", -1}})
	res := m.Database("local").Collection("oplog.rs").FindOne(ctx, f, o)
	return findOplogTSHelper(res)
}

func findOplogTSHelper(res *mongo.SingleResult) (primitive.Timestamp, error) {
	if err := res.Err(); err != nil {
		return primitive.Timestamp{}, err
	}

	var v struct{ TS primitive.Timestamp }
	if err := res.Decode(&v); err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "decode")
	}

	return v.TS, nil
}

type timerange struct {
	from, till primitive.Timestamp
}

func findChunkRanges(rs []oplog.OplogChunk, from, till primitive.Timestamp) []timerange {
	if len(rs) == 0 {
		return []timerange{{from, till}}
	}

	rv := []timerange{}

	c := rs[0]
	if from.Compare(c.StartTS) == -1 {
		rv = append(rv, timerange{from, c.StartTS})
	}

	endTS := c.EndTS
	for _, c = range rs[1:] {
		if endTS.Compare(c.StartTS) == -1 {
			rv = append(rv, timerange{endTS, c.StartTS})
		}
		if till.Compare(c.EndTS) != 1 {
			return rv
		}

		endTS = c.EndTS
	}

	if endTS.Compare(till) == -1 {
		rv = append(rv, timerange{endTS, till})
	}

	return rv
}

func formatTimestamp(t primitive.Timestamp) string {
	return fmt.Sprintf("%d,%d", t.T, t.I)
}
