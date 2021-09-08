package pbm

import (
	"fmt"
	"path"
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

// PITRGetChunkContains returns a pitr slice chunk that belongs to the
// given replica set and contains the given timestamp
func (p *PBM) PITRGetChunkContains(rs string, ts primitive.Timestamp) (*PITRChunk, error) {
	res := p.Conn.Database(DB).Collection(PITRChunksCollection).FindOne(
		p.ctx,
		bson.D{
			{"rs", rs},
			{"start_ts", bson.M{"$lte": ts}},
			{"end_ts", bson.M{"$gte": ts}},
		},
	)
	if res.Err() != nil {
		return nil, errors.Wrap(res.Err(), "get")
	}

	chnk := new(PITRChunk)
	err := res.Decode(chnk)
	return chnk, errors.Wrap(err, "decode")
}

// PITRGetChunksSlice returns slice of PITR oplog chunks which Start TS
// lies in a given time frame
func (p *PBM) PITRGetChunksSlice(rs string, from, to primitive.Timestamp) ([]PITRChunk, error) {
	q := bson.D{}
	if rs != "" {
		q = bson.D{{"rs", rs}}
	}
	q = append(q, bson.E{"start_ts", bson.M{"$gte": from, "$lte": to}})

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
	Start uint32
	End   uint32
	Size  int64
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
func (p *PBM) PITRGetValidTimelines(rs string, until int64, flist map[string]int64) (tlines []Timeline, err error) {
	fch, err := p.PITRFirstChunkMeta(rs)
	if err != nil {
		return nil, errors.Wrap(err, "get the oldest chunk")
	}
	if fch == nil {
		return nil, nil
	}

	slices, err := p.PITRGetChunksSlice(rs, fch.StartTS, primitive.Timestamp{T: uint32(until), I: 0})
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

func gettimelines(slices []PITRChunk) (tlines []Timeline) {
	var tl Timeline
	var prevEnd uint32
	for _, s := range slices {
		if prevEnd != 0 && prevEnd != s.StartTS.T {
			tlines = append(tlines, tl)
			tl = Timeline{}
		}
		if tl.Start == 0 {
			tl.Start = s.StartTS.T
		}
		prevEnd = s.EndTS.T
		tl.End = s.EndTS.T
		tl.Size += s.size
	}

	tlines = append(tlines, tl)

	return tlines
}

type tlineMerge struct {
	tl    Timeline
	repls int
}

// MergeTimelines merges overlapping sets on timelines
// it preresumes timelines already sorted and don't start from 0
func MergeTimelines(tlns ...[]Timeline) []Timeline {
	if len(tlns) == 0 {
		return nil
	}
	if len(tlns) == 1 {
		return tlns[0]
	}

	// defining the base timeline set
	// (set with the less anount of timelines)
	ln := len(tlns[0])
	ri := 0
	for i, tl := range tlns {
		if len(tl) < ln {
			ln = len(tl)
			ri = i
		}
	}
	if ri != 0 {
		tlns[0], tlns[ri] = tlns[ri], tlns[0]
	}

	rtl := make([]tlineMerge, ln)
	for i, v := range tlns[0] {
		rtl[i] = tlineMerge{tl: v, repls: 1}
	}

	// for each timeilne in the base set we're looking
	// for the overlaping timeline in the rest of the sets
	for i := range rtl {
	RSLOOP:
		for j := 1; j < len(tlns); j++ {
			tl := tlns[j]

			for _, t := range tl {
				if rtl[i].tl.End <= t.Start || t.End <= rtl[i].tl.Start {
					continue
				}
				if t.Start > rtl[i].tl.Start {
					rtl[i].tl.Start = t.Start
				}

				if t.End < rtl[i].tl.End {
					rtl[i].tl.End = t.End
				}

				rtl[i].repls++
				continue RSLOOP
			}
		}

	}

	tlines := []Timeline{}
	for _, v := range rtl {
		if v.repls == len(tlns) {
			tlines = append(tlines, v.tl)
		}
	}

	return tlines
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
