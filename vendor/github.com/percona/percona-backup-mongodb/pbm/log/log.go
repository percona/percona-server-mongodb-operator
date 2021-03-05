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

type LogEntry struct {
	ObjID   primitive.ObjectID `bson:"-" json:"-"` // to get sense of mgs total ordering while reading logs
	Time    string             `bson:"-" json:"t"`
	TS      int64              `bson:"ts" json:"-"`
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

func (e *LogEntry) String() (s string) {
	return e.string(tsLocal, false)
}

func (e *LogEntry) StringNode() (s string) {
	return e.string(tsLocal, true)
}

type tsformatf func(ts int64) string

func (e *LogEntry) string(f tsformatf, showNode bool) (s string) {
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

	e := &LogEntry{
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

func (l *Logger) Output(e *LogEntry) error {
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

type OutFormat int

const (
	FormatText OutFormat = iota
	FormatJSON
)

func (l *Logger) PrintLogs(to io.Writer, f OutFormat, r *LogRequest, limit int64, showNode bool) error {
	cur, err := l.Get(r, limit, false)
	if err != nil {
		return errors.Wrap(err, "get list from mongo")
	}

	var fn entryFn

	switch f {
	case FormatJSON:
		enc := json.NewEncoder(to)
		fn = func(e LogEntry) error {
			e.Time = tsUTC(e.TS)
			err := enc.Encode(e)
			return errors.Wrap(err, "json encode message")
		}
	default:
		fn = func(e LogEntry) error {
			_, err := io.WriteString(to, e.string(tsUTC, showNode)+"\n")
			return errors.Wrap(err, "write message")
		}
	}
	return processList(cur, fn)
}

type entryFn func(e LogEntry) error

func processList(e []LogEntry, fn entryFn) error {
	for i := len(e) - 1; i >= 0; i-- {
		err := fn(e[i])
		if err != nil {
			return errors.Wrap(err, "process message")
		}
	}
	return nil
}

func (l *Logger) Get(r *LogRequest, limit int64, exactSeverity bool) ([]LogEntry, error) {
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

	cur, err := l.cn.Find(
		context.TODO(),
		filter,
		options.Find().SetLimit(limit).SetSort(bson.D{{"ts", -1}, {"ns", -1}}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "get list from mongo")
	}
	defer cur.Close(context.TODO())

	logs := []LogEntry{}
	for cur.Next(context.TODO()) {
		l := LogEntry{}
		err := cur.Decode(&l)
		if err != nil {
			return nil, errors.Wrap(err, "message decode")
		}
		if id, ok := cur.Current.Lookup("_id").ObjectIDOK(); ok {
			l.ObjID = id
		}
		logs = append(logs, l)
	}

	return logs, nil
}
