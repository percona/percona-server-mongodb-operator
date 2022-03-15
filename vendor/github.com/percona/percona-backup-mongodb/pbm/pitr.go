package pbm

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	// PITRdefaultSpan oplog slicing time span
	PITRdefaultSpan = time.Minute * 10
	// PITRfsPrefix is a prefix (folder) for PITR chunks on the storage
	PITRfsPrefix = "pbmPitr"
)

// PITRChunk is index metadata for the oplog chunks
type PITRChunk struct {
	RS          string              `bson:"rs"`
	FName       string              `bson:"fname"`
	Compression CompressionType     `bson:"compression"`
	StartTS     primitive.Timestamp `bson:"start_ts"`
	EndTS       primitive.Timestamp `bson:"end_ts"`
	size        int64               `bson:"-"`
}

// IsPITR checks if PITR is enabled
func (p *PBM) IsPITR() (bool, error) {
	cfg, err := p.GetConfig()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}

		return false, errors.Wrap(err, "get config")
	}

	return cfg.PITR.Enabled, nil
}

// PITRrun checks if PITR slicing is running. It looks for PITR locks
// and returns true if there is at least one not stale.
func (p *PBM) PITRrun() (bool, error) {
	l, err := p.GetLocks(&LockHeader{Type: CmdPITR})
	if errors.Is(err, mongo.ErrNoDocuments) || len(l) == 0 {
		return false, nil
	}

	if err != nil {
		return false, errors.Wrap(err, "get locks")
	}

	ct, err := p.ClusterTime()
	if err != nil {
		return false, errors.Wrap(err, "get cluster time")
	}

	for _, lk := range l {
		if lk.Heartbeat.T+StaleFrameSec >= ct.T {
			return true, nil
		}
	}

	return false, nil
}

// PITRLastChunkMeta returns the most recent PITR chunk for the given Replset
func (p *PBM) PITRLastChunkMeta(rs string) (*PITRChunk, error) {
	return p.pitrChunk(rs, -1)
}

// PITRFirstChunkMeta returns the oldest PITR chunk for the given Replset
func (p *PBM) PITRFirstChunkMeta(rs string) (*PITRChunk, error) {
	return p.pitrChunk(rs, 1)
}

func (p *PBM) pitrChunk(rs string, sort int) (*PITRChunk, error) {
	res := p.Conn.Database(DB).Collection(PITRChunksCollection).FindOne(
		p.ctx,
		bson.D{{"rs", rs}},
		options.FindOne().SetSort(bson.D{{"start_ts", sort}}),
	)
	if res.Err() != nil {
		if res.Err() == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrap(res.Err(), "get")
	}

	chnk := new(PITRChunk)
	err := res.Decode(chnk)
	return chnk, errors.Wrap(err, "decode")
}

// PITRGetChunksSlice returns slice of PITR oplog chunks which Start TS
// lies in a given time frame. Returns all chunks if `to` is 0.
func (p *PBM) PITRGetChunksSlice(rs string, from, to primitive.Timestamp) ([]PITRChunk, error) {
	q := bson.D{}
	if rs != "" {
		q = bson.D{{"rs", rs}}
	}
	if to.T > 0 {
		q = append(q, bson.E{"start_ts", bson.M{"$gte": from, "$lte": to}})
	}

	return p.pitrGetChunksSlice(rs, q)
}

func (p *PBM) PITRFindChunk(rs string, ts primitive.Timestamp) (PITRChunk, error) {
	q := bson.D{
		{"rs", rs},
		{"start_ts", bson.M{"$lte": ts}},
		{"end_ts", bson.M{"$gte": ts}},
	}
	o := options.FindOne().SetSort(bson.D{{"start_ts", 1}})

	cur := p.Conn.Database(DB).Collection(PITRChunksCollection).FindOne(p.ctx, q, o)
	if err := cur.Err(); err != nil {
		return PITRChunk{}, err
	}

	var chunk PITRChunk
	err := cur.Decode(&chunk)
	return chunk, err
}

// PITRGetChunksSliceUntil returns slice of PITR oplog chunks that starts up until timestamp (exclusively)
func (p *PBM) PITRGetChunksSliceUntil(rs string, t primitive.Timestamp) ([]PITRChunk, error) {
	q := bson.D{}
	if rs != "" {
		q = bson.D{{"rs", rs}}
	}

	q = append(q, bson.E{"start_ts", bson.M{"$lt": t}})

	return p.pitrGetChunksSlice(rs, q)
}

func (p *PBM) pitrGetChunksSlice(rs string, q bson.D) ([]PITRChunk, error) {
	cur, err := p.Conn.Database(DB).Collection(PITRChunksCollection).Find(
		p.ctx,
		q,
		options.Find().SetSort(bson.D{{"start_ts", 1}}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "get cursor")
	}
	defer cur.Close(p.ctx)

	chnks := []PITRChunk{}
	for cur.Next(p.ctx) {
		var chnk PITRChunk
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
func (p *PBM) PITRGetChunkStarts(rs string, ts primitive.Timestamp) (*PITRChunk, error) {
	res := p.Conn.Database(DB).Collection(PITRChunksCollection).FindOne(
		p.ctx,
		bson.D{
			{"rs", rs},
			{"start_ts", ts},
		},
	)
	if res.Err() != nil {
		return nil, errors.Wrap(res.Err(), "get")
	}

	chnk := new(PITRChunk)
	err := res.Decode(chnk)
	return chnk, errors.Wrap(err, "decode")
}

// PITRAddChunk stores PITR chunk metadata
func (p *PBM) PITRAddChunk(c PITRChunk) error {
	_, err := p.Conn.Database(DB).Collection(PITRChunksCollection).InsertOne(p.ctx, c)

	return err
}

type Timeline struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
	Size  int64  `json:"-"`
}

const tlTimeFormat = "2006-01-02T15:04:05"

func (t Timeline) String() string {
	ts := time.Unix(int64(t.Start), 0).UTC()
	te := time.Unix(int64(t.End), 0).UTC()
	return fmt.Sprintf("%s - %s", ts.Format(tlTimeFormat), te.Format(tlTimeFormat))
}

// PITRGetValidTimelines returns time ranges valid for PITR restore
// for the given replicaset. We don't check for any "restore intrusions"
// or other integrity issues since it's guaranteed be the slicer that
// any saved chunk already belongs to some valid timeline,
// the slice wouldn't be done otherwise.
// `flist` is a cache of chunk sizes.
func (p *PBM) PITRGetValidTimelines(rs string, until primitive.Timestamp, flist map[string]int64) (tlines []Timeline, err error) {
	fch, err := p.PITRFirstChunkMeta(rs)
	if err != nil {
		return nil, errors.Wrap(err, "get the oldest chunk")
	}
	if fch == nil {
		return nil, nil
	}

	slices, err := p.PITRGetChunksSlice(rs, fch.StartTS, until)
	if err != nil {
		return nil, errors.Wrap(err, "get slice")
	}

	if flist != nil {
		for i, s := range slices {
			slices[i].size = flist[s.FName]
		}
	}

	return gettimelines(slices), nil
}

// PITRTimelines returns cluster-wide time ranges valid for PITR restore
func (p *PBM) PITRTimelines() (tlines []Timeline, err error) {
	shards, err := p.ClusterMembers(nil)
	if err != nil {
		return nil, errors.Wrap(err, "get cluster members")
	}

	now, err := p.ClusterTime()
	if err != nil {
		return nil, errors.Wrap(err, "get cluster time")
	}

	var tlns [][]Timeline
	for _, s := range shards {
		t, err := p.PITRGetValidTimelines(s.RS, now, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "get PITR timelines for %s replset", s.RS)
		}
		if len(t) != 0 {
			tlns = append(tlns, t)
		}
	}

	return MergeTimelines(tlns...), nil
}

func gettimelines(slices []PITRChunk) (tlines []Timeline) {
	var tl Timeline
	var prevEnd primitive.Timestamp
	for _, s := range slices {
		if prevEnd.T != 0 && primitive.CompareTimestamp(prevEnd, s.StartTS) == -1 {
			tlines = append(tlines, tl)
			tl = Timeline{}
		}
		if tl.Start == 0 {
			tl.Start = s.StartTS.T
		}
		prevEnd = s.EndTS
		tl.End = s.EndTS.T
		tl.Size += s.size
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

	// if no gaps, just return avaliable range
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

	// split avaliable timeline with gaps
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

// PITRmetaFromFName parses given file name and returns PITRChunk metadata
// it returns nil if file wasn't parse successfully (e.g. wrong format)
// current fromat is 20200715155939-0.20200715160029-1.oplog.snappy
//
// !!! should be agreed with pbm/pitr.chunkPath()
func PITRmetaFromFName(f string) *PITRChunk {
	ppath := strings.Split(f, "/")
	if len(ppath) < 2 {
		return nil
	}
	chnk := &PITRChunk{}
	chnk.RS = ppath[0]
	chnk.FName = path.Join(PITRfsPrefix, f)

	fname := ppath[len(ppath)-1]
	fparts := strings.Split(fname, ".")
	if len(fparts) < 3 || fparts[2] != "oplog" {
		return nil
	}
	if len(fparts) == 4 {
		chnk.Compression = FileCompression(fparts[3])
	} else {
		chnk.Compression = CompressionTypeNone
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
