package log

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Logger struct {
	cn   *mongo.Collection
	out  io.Writer
	rs   string
	node string
}

type Entry struct {
	ObjID   primitive.ObjectID `bson:"-" json:"-"` // to get sense of mgs total ordering while reading logs
	TS      int64              `bson:"ts" json:"ts"`
	Tns     int                `bson:"ns" json:"-"`
	TZone   int                `bson:"tz" json:"-"`
	LogKeys `bson:",inline" json:",inline"`
	Msg     string `bson:"msg" json:"msg"`
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

// LogTimeFormat is a date-time format to be displayed in the log output
const LogTimeFormat = "2006-01-02T15:04:05.000-0700"

func tsLocal(ts int64) string {
	return time.Unix(ts, 0).Local().Format(LogTimeFormat)
}

func tsUTC(ts int64) string {
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

func (e *Entry) String() (s string) {
	return e.string(tsLocal, false, false)
}

func (e *Entry) StringNode() (s string) {
	return e.string(tsLocal, true, false)
}

type tsformatf func(ts int64) string

func (e *Entry) string(f tsformatf, showNode, extr bool) (s string) {
	node := ""
	if showNode {
		node = " [" + e.RS + "/" + e.Node + "]"
	}

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

type Severity int

const (
	Fatal Severity = iota
	Error
	Warning
	Info
	Debug
)

func (s Severity) String() string {
	switch s {
	case Fatal:
		return "F"
	case Error:
		return "E"
	case Warning:
		return "W"
	case Info:
		return "I"
	case Debug:
		return "D"
	default:
		return ""
	}
}

func New(cn *mongo.Collection, rs, node string) *Logger {
	return &Logger{
		cn:   cn,
		out:  os.Stderr,
		rs:   rs,
		node: node,
	}
}

// SetOut set io output for the logs
func (l *Logger) SetOut(w io.Writer) {
	l.out = w
}

func (l *Logger) output(s Severity, event string, obj, opid string, epoch primitive.Timestamp, msg string, args ...interface{}) {
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	_, tz := time.Now().Local().Zone()
	t := time.Now().UTC()

	e := &Entry{
		TS:    t.Unix(),
		Tns:   t.Nanosecond(),
		TZone: tz,
		LogKeys: LogKeys{
			RS:       l.rs,
			Node:     l.node,
			Severity: s,
			Event:    event,
			ObjName:  obj,
			OPID:     opid,
			Epoch:    epoch,
		},
		Msg: msg,
	}

	err := l.Output(e)
	if err != nil {
		log.Printf("[ERROR] wrting log: %v, entry: %s", err, e)
	}
}

func (l *Logger) Printf(msg string, args ...interface{}) {
	l.output(Info, "", "", "", primitive.Timestamp{}, msg, args...)
}

func (l *Logger) Debug(event string, obj, opid string, epoch primitive.Timestamp, msg string, args ...interface{}) {
	l.output(Debug, event, obj, opid, epoch, msg, args...)
}

func (l *Logger) Info(event string, obj, opid string, epoch primitive.Timestamp, msg string, args ...interface{}) {
	l.output(Info, event, obj, opid, epoch, msg, args...)
}

func (l *Logger) Warning(event string, obj, opid string, epoch primitive.Timestamp, msg string, args ...interface{}) {
	l.output(Warning, event, obj, opid, epoch, msg, args...)
}

func (l *Logger) Error(event string, obj, opid string, epoch primitive.Timestamp, msg string, args ...interface{}) {
	l.output(Error, event, obj, opid, epoch, msg, args...)
}

func (l *Logger) Fatal(event string, obj, opid string, epoch primitive.Timestamp, msg string, args ...interface{}) {
	l.output(Fatal, event, obj, opid, epoch, msg, args...)
}

func (l *Logger) Output(e *Entry) error {
	var rerr error

	if l.cn != nil {
		_, err := l.cn.InsertOne(context.TODO(), e)
		if err != nil {
			rerr = errors.Wrap(err, "db")
		}
	}

	if l.out != nil {
		_, err := l.out.Write(append([]byte(e.String()), '\n'))

		err = errors.Wrap(err, "io")
		if rerr != nil {
			rerr = errors.Errorf("%v, %v", rerr, err)
		} else {
			rerr = err
		}
	}

	return rerr
}

// Event provides logging for some event (backup, restore)
type Event struct {
	l    *Logger
	typ  string
	obj  string
	ep   primitive.Timestamp
	opid string
}

func (l *Logger) NewEvent(typ, name, opid string, epoch primitive.Timestamp) *Event {
	return &Event{
		l:    l,
		typ:  typ,
		obj:  name,
		ep:   epoch,
		opid: opid,
	}
}

func (e *Event) Debug(msg string, args ...interface{}) {
	e.l.Debug(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

func (e *Event) Info(msg string, args ...interface{}) {
	e.l.Info(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

func (e *Event) Warning(msg string, args ...interface{}) {
	e.l.Warning(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

func (e *Event) Error(msg string, args ...interface{}) {
	e.l.Error(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

func (e *Event) Fatal(msg string, args ...interface{}) {
	e.l.Fatal(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

type LogRequest struct {
	TimeMin time.Time
	TimeMax time.Time
	LogKeys
}

type Entries struct {
	Data     []Entry `json:"data"`
	ShowNode bool    `json:"-"`
	Extr     bool    `json:"-"`
}

func (e Entries) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.Data)
}

func (e Entries) String() (s string) {
	for _, entry := range e.Data {
		s += entry.string(tsUTC, e.ShowNode, e.Extr) + "\n"
	}

	return s
}

func Get(cn *mongo.Collection, r *LogRequest, limit int64, exactSeverity bool) (*Entries, error) {
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

	cur, err := cn.Find(
		context.TODO(),
		filter,
		options.Find().SetLimit(limit).SetSort(bson.D{{"ts", -1}, {"ns", -1}}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "get list from mongo")
	}
	defer cur.Close(context.TODO())

	e := new(Entries)
	for cur.Next(context.TODO()) {
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
