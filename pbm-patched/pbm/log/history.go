package log

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

type LogRequest struct {
	TimeMin time.Time
	TimeMax time.Time
	LogKeys
}

type Entry struct {
	ObjID   primitive.ObjectID `bson:"-" json:"-"` // to get sense of mgs total ordering while reading logs
	TS      int64              `bson:"ts" json:"ts"`
	Tns     int                `bson:"ns" json:"-"`
	TZone   int                `bson:"tz" json:"-"`
	LogKeys `bson:",inline" json:",inline"`
	Msg     string `bson:"msg" json:"msg"`
}

func (e *Entry) Stringify(f tsFormatFn, showNode, extr bool) string {
	node := ""
	if showNode {
		node = " [" + e.RS + "/" + e.Node + "]"
	}

	var s string
	if e.Event != "" || e.ObjName != "" {
		id := []string{}
		if e.Event != "" {
			id = append(id, string(e.Event))
		}
		if e.ObjName != "" {
			id = append(id, e.ObjName)
		}
		if extr {
			id = append(id, e.OPID)
		}
		s = fmt.Sprintf("%s %s%s [%s] %s", f(e.TS), e.Severity, node, strings.Join(id, "/"), e.Msg)
	} else {
		s = fmt.Sprintf("%s %s%s %s", f(e.TS), e.Severity, node, e.Msg)
	}

	return s
}

func (e *Entry) String() string {
	return e.Stringify(AsLocal, false, false)
}

func (e *Entry) StringNode() string {
	return e.Stringify(AsLocal, true, false)
}

func AsLocal(ts int64) string {
	//nolint:gosmopolitan
	return time.Unix(ts, 0).Local().Format(LogTimeFormat)
}

func AsUTC(ts int64) string {
	//nolint:gosmopolitan
	return time.Unix(ts, 0).UTC().Format(LogTimeFormat)
}

type LogKeys struct {
	Severity Severity            `bson:"s" json:"s"`
	RS       string              `bson:"rs" json:"rs"`
	Node     string              `bson:"node" json:"node"`
	Event    string              `bson:"e" json:"e"`
	ObjName  string              `bson:"eobj" json:"eobj"`
	Epoch    primitive.Timestamp `bson:"ep,omitempty" json:"ep,omitempty"`
	OPID     string              `bson:"opid,omitempty" json:"opid,omitempty"`
}

type Entries struct {
	Data     []Entry `json:"data"`
	ShowNode bool    `json:"-"`
	Extr     bool    `json:"-"`
	loc      *time.Location
}

func (e *Entries) SetLocation(l string) error {
	var err error
	e.loc, err = time.LoadLocation(l)
	return err
}

func (e Entries) MarshalJSON() ([]byte, error) {
	data := e.Data
	if data == nil {
		data = []Entry{}
	}
	return json.Marshal(data)
}

func (e Entries) String() string {
	if e.loc == nil {
		e.loc = time.UTC
	}

	f := func(ts int64) string {
		return time.Unix(ts, 0).In(e.loc).Format(time.RFC3339)
	}

	s := ""
	for _, entry := range e.Data {
		s += entry.Stringify(f, e.ShowNode, e.Extr) + "\n"
	}

	return s
}

func buildLogFilter(r *LogRequest, exactSeverity bool) bson.D {
	filter := bson.D{bson.E{"s", bson.M{"$lte": r.Severity}}}
	if exactSeverity {
		filter = bson.D{bson.E{"s", r.Severity}}
	}

	if r.RS != "" {
		filter = append(filter, bson.E{"rs", r.RS})
	}
	if r.Node != "" {
		filter = append(filter, bson.E{"node", r.Node})
	}
	if r.Event != "" {
		filter = append(filter, bson.E{"e", r.Event})
	}
	if r.ObjName != "" {
		filter = append(filter, bson.E{"eobj", r.ObjName})
	}
	if r.Epoch.T > 0 {
		filter = append(filter, bson.E{"ep", r.Epoch})
	}
	if r.OPID != "" {
		filter = append(filter, bson.E{"opid", r.OPID})
	}
	if !r.TimeMin.IsZero() {
		filter = append(filter, bson.E{"ts", bson.M{"$gte": r.TimeMin.Unix()}})
	}
	if !r.TimeMax.IsZero() {
		filter = append(filter, bson.E{"ts", bson.M{"$lte": r.TimeMax.Unix()}})
	}

	return filter
}

func fetch(
	ctx context.Context,
	m connect.Client,
	r *LogRequest,
	limit int64,
	exactSeverity bool,
) (*Entries, error) {
	filter := buildLogFilter(r, exactSeverity)
	cur, err := m.LogCollection().Find(
		ctx,
		filter,
		options.Find().SetLimit(limit).SetSort(bson.D{{"ts", -1}, {"ns", -1}}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "get list from mongo")
	}
	defer cur.Close(ctx)

	e := &Entries{}
	for cur.Next(ctx) {
		l := Entry{}
		err := cur.Decode(&l)
		if err != nil {
			return nil, errors.Wrap(err, "message decode")
		}
		if id, ok := cur.Current.Lookup("_id").ObjectIDOK(); ok {
			l.ObjID = id
		}
		e.Data = append(e.Data, l)
	}

	return e, nil
}

func CommandLastError(ctx context.Context, cc connect.Client, cid string) (string, error) {
	filter := buildLogFilter(&LogRequest{LogKeys: LogKeys{OPID: cid, Severity: Error}}, false)
	opts := options.FindOne().
		SetSort(bson.D{{"$natural", -1}}).
		SetProjection(bson.D{{"msg", 1}})
	res := cc.LogCollection().FindOne(ctx, filter, opts)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", nil
		}
		return "", errors.Wrap(err, "find one")
	}

	l := Entry{}
	err := res.Decode(&l)
	if err != nil {
		return "", errors.Wrap(err, "message decode")
	}

	return l.Msg, nil
}

func Follow(
	ctx context.Context,
	conn connect.Client,
	r *LogRequest,
	exactSeverity bool,
) (<-chan *Entry, <-chan error) {
	filter := buildLogFilter(r, exactSeverity)
	outC, errC := make(chan *Entry), make(chan error)

	go func() {
		defer close(errC)
		defer close(outC)

		opt := options.Find().SetCursorType(options.TailableAwait)

		cur, err := conn.LogCollection().Find(ctx, filter, opt)
		if err != nil {
			errC <- errors.Wrap(err, "query")
			return
		}
		defer cur.Close(context.Background())

		for cur.Next(ctx) {
			e := &Entry{}
			if err := cur.Decode(e); err != nil {
				errC <- errors.Wrap(err, "decode")
				return
			}

			e.ObjID, _ = cur.Current.Lookup("_id").ObjectIDOK()
			outC <- e
		}

		if err := cur.Err(); err != nil {
			errC <- err
		}
	}()

	return outC, errC
}

func LogGet(ctx context.Context, m connect.Client, r *LogRequest, limit int64) (*Entries, error) {
	return fetch(ctx, m, r, limit, false)
}

func LogGetExactSeverity(ctx context.Context, m connect.Client, r *LogRequest, limit int64) (*Entries, error) {
	return fetch(ctx, m, r, limit, true)
}

func GetFirstTSForOPID(ctx context.Context, conn connect.Client, opid string) (int64, error) {
	return getTSForOPIDImpl(ctx, conn, opid, 1)
}

func GetLastTSForOPID(ctx context.Context, conn connect.Client, opid string) (int64, error) {
	return getTSForOPIDImpl(ctx, conn, opid, -1)
}

func CommandLogCursor(
	ctx context.Context,
	conn connect.Client,
	opid string,
) (*Cursor, error) {
	from, err := GetFirstTSForOPID(ctx, conn, opid)
	if err != nil {
		return nil, errors.Wrap(err, "get first opid ts")
	}
	till, err := GetLastTSForOPID(ctx, conn, opid)
	if err != nil {
		return nil, errors.Wrap(err, "get last opid ts")
	}

	cur, err := conn.LogCollection().Find(ctx, bson.D{{"ts", bson.M{"$gte": from, "$lte": till}}})
	if err != nil {
		return nil, errors.Wrap(err, "log: create cursor")
	}

	return &Cursor{cur: cur}, nil
}

type Cursor struct {
	cur *mongo.Cursor
}

func (c *Cursor) Close(ctx context.Context) error {
	return c.cur.Close(ctx)
}

func (c *Cursor) Err() error {
	return c.cur.Err()
}

func (c *Cursor) Next(ctx context.Context) bool {
	return c.cur.Next(ctx)
}

func (c *Cursor) Record() (*Entry, error) {
	var e *Entry
	err := c.cur.Decode(&e)
	return e, err
}

func getTSForOPIDImpl(
	ctx context.Context,
	conn connect.Client,
	opid string,
	sort int,
) (int64, error) {
	raw, err := conn.LogCollection().FindOne(ctx,
		bson.D{{"opid", opid}},
		options.FindOne().SetSort(bson.D{{"ts", sort}}).SetProjection(bson.D{{"ts", 1}})).
		Raw()
	if err != nil {
		return 0, err
	}

	ts, _ := raw.Lookup("ts").AsInt64OK()
	return ts, nil
}
