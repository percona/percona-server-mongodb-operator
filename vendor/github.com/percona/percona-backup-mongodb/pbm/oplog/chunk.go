package oplog

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

// OplogChunk is index metadata for the oplog chunks
type OplogChunk struct {
	RS          string                   `bson:"rs"`
	FName       string                   `bson:"fname"`
	Compression compress.CompressionType `bson:"compression"`
	StartTS     primitive.Timestamp      `bson:"start_ts"`
	EndTS       primitive.Timestamp      `bson:"end_ts"`
	Size        int64                    `bson:"size"`
}

// PITRLastChunkMeta returns the most recent PITR chunk for the given Replset
func PITRLastChunkMeta(ctx context.Context, m connect.Client, rs string) (*OplogChunk, error) {
	return pitrChunk(ctx, m, rs, -1)
}

// PITRFirstChunkMeta returns the oldest PITR chunk for the given Replset
func PITRFirstChunkMeta(ctx context.Context, m connect.Client, rs string) (*OplogChunk, error) {
	return pitrChunk(ctx, m, rs, 1)
}

func pitrChunk(ctx context.Context, m connect.Client, rs string, sort int) (*OplogChunk, error) {
	res := m.PITRChunksCollection().FindOne(
		ctx,
		bson.D{{"rs", rs}},
		options.FindOne().SetSort(bson.D{{"start_ts", sort}}),
	)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "get")
	}

	chnk := &OplogChunk{}
	err := res.Decode(chnk)
	return chnk, errors.Wrap(err, "decode")
}

func AllOplogRSNames(ctx context.Context, m connect.Client, from, to primitive.Timestamp) ([]string, error) {
	q := bson.M{
		"start_ts": bson.M{"$lte": to},
	}
	if !from.IsZero() {
		q["end_ts"] = bson.M{"$gte": from}
	}

	res, err := m.PITRChunksCollection().Distinct(ctx, "rs", q)
	if err != nil {
		return nil, errors.Wrapf(err, "query")
	}

	rv := make([]string, len(res))
	for i, rs := range res {
		rv[i] = rs.(string)
	}

	return rv, nil
}

// PITRGetChunksSlice returns slice of PITR oplog chunks which Start TS
// lies in a given time frame. Returns all chunks if `to` is 0.
func PITRGetChunksSlice(
	ctx context.Context,
	m connect.Client,
	rs string,
	from, to primitive.Timestamp,
) ([]OplogChunk, error) {
	q := bson.D{}
	if rs != "" {
		q = bson.D{{"rs", rs}}
	}
	if !from.IsZero() {
		q = append(q, bson.E{"end_ts", bson.M{"$gte": from}})
	}
	if !to.IsZero() {
		q = append(q, bson.E{"start_ts", bson.M{"$lte": to}})
	}

	return pitrGetChunksSlice(ctx, m, q)
}

// PITRGetChunksSliceUntil returns slice of PITR oplog chunks that starts up until timestamp (exclusively)
func PITRGetChunksSliceUntil(
	ctx context.Context,
	m connect.Client,
	rs string,
	t primitive.Timestamp,
) ([]OplogChunk, error) {
	q := bson.D{}
	if rs != "" {
		q = bson.D{{"rs", rs}}
	}

	q = append(q, bson.E{"start_ts", bson.M{"$lt": t}})

	return pitrGetChunksSlice(ctx, m, q)
}

func pitrGetChunksSlice(ctx context.Context, m connect.Client, q bson.D) ([]OplogChunk, error) {
	cur, err := m.PITRChunksCollection().Find(
		ctx,
		q,
		options.Find().SetSort(bson.D{{"start_ts", 1}}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "get cursor")
	}
	defer cur.Close(ctx)

	chnks := []OplogChunk{}
	for cur.Next(ctx) {
		var chnk OplogChunk
		err := cur.Decode(&chnk)
		if err != nil {
			return nil, errors.Wrap(err, "decode chunk")
		}

		chnks = append(chnks, chnk)
	}

	return chnks, cur.Err()
}

// PITRGetChunkStarts returns a pitr slice chunk that belongs to the
// given replica set and start from the given timestamp
func PITRGetChunkStarts(
	ctx context.Context,
	m connect.Client,
	rs string,
	ts primitive.Timestamp,
) (*OplogChunk, error) {
	res := m.PITRChunksCollection().FindOne(
		ctx,
		bson.D{
			{"rs", rs},
			{"start_ts", ts},
		},
	)
	if res.Err() != nil {
		return nil, errors.Wrap(res.Err(), "get")
	}

	chnk := &OplogChunk{}
	err := res.Decode(chnk)
	return chnk, errors.Wrap(err, "decode")
}

// PITRAddChunk stores PITR chunk metadata
func PITRAddChunk(ctx context.Context, m connect.Client, c OplogChunk) error {
	_, err := m.PITRChunksCollection().InsertOne(ctx, c)

	return err
}

// PITRGetValidTimelines returns time ranges valid for PITR restore
// for the given replicaset. We don't check for any "restore intrusions"
// or other integrity issues since it's guaranteed be the slicer that
// any saved chunk already belongs to some valid timeline,
// the slice wouldn't be done otherwise.
// `flist` is a cache of chunk sizes.
func PITRGetValidTimelines(
	ctx context.Context,
	m connect.Client,
	rs string,
	until primitive.Timestamp,
) ([]Timeline, error) {
	fch, err := PITRFirstChunkMeta(ctx, m, rs)
	if err != nil && !errors.Is(err, errors.ErrNotFound) {
		return nil, errors.Wrap(err, "get the oldest chunk")
	}
	if fch == nil {
		return nil, nil
	}

	return PITRGetValidTimelinesBetween(ctx, m, rs, fch.StartTS, until)
}

func PITRGetValidTimelinesBetween(
	ctx context.Context,
	m connect.Client,
	rs string,
	from primitive.Timestamp,
	until primitive.Timestamp,
) ([]Timeline, error) {
	slices, err := PITRGetChunksSlice(ctx, m, rs, from, until)
	if err != nil {
		return nil, errors.Wrap(err, "get slice")
	}

	return gettimelines(slices), nil
}

// PITRTimelines returns cluster-wide time ranges valid for PITR restore
func PITRTimelines(ctx context.Context, m connect.Client) ([]Timeline, error) {
	now, err := topo.GetClusterTime(ctx, m)
	if err != nil {
		return nil, errors.Wrap(err, "get cluster time")
	}

	return PITRTimelinesBetween(ctx, m, primitive.Timestamp{}, now)
}

func PITRTimelinesBetween(ctx context.Context, m connect.Client, from, until primitive.Timestamp) ([]Timeline, error) {
	shards, err := topo.ClusterMembers(ctx, m.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "get cluster members")
	}

	var tlns [][]Timeline
	for _, s := range shards {
		t, err := PITRGetValidTimelinesBetween(ctx, m, s.RS, from, until)
		if err != nil {
			return nil, errors.Wrapf(err, "get PITR timelines for %s replset", s.RS)
		}
		if len(t) != 0 {
			tlns = append(tlns, t)
		}
	}

	return MergeTimelines(tlns...), nil
}

func gettimelines(slices []OplogChunk) []Timeline {
	var tl Timeline
	var prevEnd primitive.Timestamp
	tlines := []Timeline{}

	for _, s := range slices {
		if prevEnd.T != 0 && prevEnd.Compare(s.StartTS) == -1 {
			tlines = append(tlines, tl)
			tl = Timeline{}
		}
		if tl.Start == 0 {
			tl.Start = s.StartTS.T
		}
		prevEnd = s.EndTS
		tl.End = s.EndTS.T
		tl.Size += s.Size
	}

	tlines = append(tlines, tl)

	return tlines
}

type gap struct {
	s, e uint32
}

type gaps []gap

func (x gaps) Len() int { return len(x) }
func (x gaps) Less(i, j int) bool {
	return x[i].s < x[j].s || (x[i].s == x[j].s && x[i].e < x[j].e)
}
func (x gaps) Swap(i, j int) { x[i], x[j] = x[j], x[i] }

// MergeTimelines merges overlapping sets on timelines
// it presumes timelines are sorted and don't start from 0
func MergeTimelines(tlns ...[]Timeline) []Timeline {
	// fast paths
	if len(tlns) == 0 {
		return nil
	}
	if len(tlns) == 1 {
		return tlns[0]
	}

	// First, we define the avaliagble range. It equals to the beginning of the latest start of the first
	// timeline of any set and to the earliest end of the last timeline of any set. Then define timelines' gaps
	// merge overlapping and apply resulted gap on the avaliagble range.
	//
	// given timelines:
	// 1 2 3 4     7 8 10 11          16 17 18 19 20
	//     3 4 5 6 7 8 10 11 12    15 16 17
	// 1 2 3 4 5 6 7 8 10 11 12       16 17 18
	//
	// aavliable range:
	//     3 4 5 6 7 8 10 11 12 13 15 16 17
	// merged gaps:
	//         5 6           12 13 15       18 19 20
	// result:
	//     3 4     7 8 10 11          16 17
	//

	// limits of the avaliagble range
	// `start` is the lates start the timelines range
	// `end` - is the earliest end
	var start, end uint32

	// iterating through the timelines  1) define `start` and `end`,
	// 2) defiene gaps and add them into slice.
	var g gaps
	for _, tln := range tlns {
		if len(tln) == 0 {
			continue
		}

		if tln[0].Start > start {
			start = tln[0].Start
		}

		if end == 0 || tln[len(tln)-1].End < end {
			end = tln[len(tln)-1].End
		}

		if len(tln) == 1 {
			continue
		}
		var ls uint32
		for i, t := range tln {
			if i == 0 {
				ls = t.End
				continue
			}
			g = append(g, gap{ls, t.Start})
			ls = t.End
		}
	}
	sort.Sort(g)

	// if no gaps, just return available range
	if len(g) == 0 {
		return []Timeline{{Start: start, End: end}}
	}

	// merge overlapping gaps
	var g2 gaps
	var cend uint32
	for _, gp := range g {
		if gp.e <= start {
			continue
		}
		if gp.s >= end {
			break
		}

		if len(g2) > 0 {
			cend = g2[len(g2)-1].e
		}

		if gp.s > cend {
			g2 = append(g2, gp)
			continue
		}
		if gp.e > cend {
			g2[len(g2)-1].e = gp.e
		}
	}

	// split available timeline with gaps
	var ret []Timeline
	for _, g := range g2 {
		if start < g.s {
			ret = append(ret, Timeline{Start: start, End: g.s})
		}
		start = g.e
	}
	if start < end {
		ret = append(ret, Timeline{Start: start, End: end})
	}

	return ret
}

// FormatChunkFilepath returns filepath for a oplog chunk.
// Current format is 20200715155939-0.20200715160029-1.oplog.snappy
//
// !!! should be agreed with oplog.MakeChunkMetaFromFilepath()
func FormatChunkFilepath(rs string, first, last primitive.Timestamp, c compress.CompressionType) string {
	ft := time.Unix(int64(first.T), 0).UTC()
	lt := time.Unix(int64(last.T), 0).UTC()

	name := strings.Builder{}
	name.WriteString(defs.PITRfsPrefix)
	name.WriteString("/")
	name.WriteString(rs)
	name.WriteString("/")
	name.WriteString(ft.Format("20060102"))
	name.WriteString("/")
	name.WriteString(ft.Format("20060102150405"))
	name.WriteString("-")
	name.WriteString(strconv.Itoa(int(first.I)))
	name.WriteString(".")
	name.WriteString(lt.Format("20060102150405"))
	name.WriteString("-")
	name.WriteString(strconv.Itoa(int(last.I)))
	name.WriteString(".oplog")
	name.WriteString(c.Suffix())

	return name.String()
}

// MakeChunkMetaFromFilepath parses given file name and returns PITRChunk metadata
// it returns nil if file wasn't parse successfully (e.g. wrong format)
// current format is 20200715155939-0.20200715160029-1.oplog.snappy
//
// !!! should be agreed with oplog.FormatChunkFilepath()
func MakeChunkMetaFromFilepath(f string) *OplogChunk {
	ppath := strings.Split(f, "/")
	if len(ppath) < 2 {
		return nil
	}
	chnk := &OplogChunk{}
	chnk.RS = ppath[0]
	chnk.FName = path.Join(defs.PITRfsPrefix, f)

	fname := ppath[len(ppath)-1]
	fparts := strings.Split(fname, ".")
	if len(fparts) < 3 || fparts[2] != "oplog" {
		return nil
	}
	if len(fparts) == 4 {
		chnk.Compression = compress.FileCompression(fparts[3])
	} else {
		chnk.Compression = compress.CompressionTypeNone
	}

	start := pitrParseTS(fparts[0])
	if start == nil {
		return nil
	}
	end := pitrParseTS(fparts[1])
	if end == nil {
		return nil
	}

	chnk.StartTS = *start
	chnk.EndTS = *end

	return chnk
}

func pitrParseTS(tstr string) *primitive.Timestamp {
	tparts := strings.Split(tstr, "-")
	t, err := time.Parse("20060102150405", tparts[0])
	if err != nil {
		// just skip this file
		return nil
	}
	ts := primitive.Timestamp{T: uint32(t.Unix())}
	if len(tparts) > 1 {
		ti, err := strconv.Atoi(tparts[1])
		if err != nil {
			// just skip this file
			return nil
		}
		ts.I = uint32(ti)
	}

	return &ts
}

func HasSingleTimelineToCover(chunks []OplogChunk, from, till uint32) bool {
	rss := make(map[string][]OplogChunk)
	for _, c := range chunks {
		rss[c.RS] = append(rss[c.RS], c)
	}
	tlns := make([][]Timeline, 0, len(rss))
	for _, slices := range rss {
		tlns = append(tlns, gettimelines(slices))
	}

	for _, r := range MergeTimelines(tlns...) {
		if r.Start <= from && till <= r.End {
			return true
		}
	}

	return false
}
