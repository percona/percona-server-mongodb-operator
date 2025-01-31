package log

import (
	"context"
	"io"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// LogTimeFormat is a date-time format to be displayed in the log output
const LogTimeFormat = "2006-01-02T15:04:05.000-0700"

var (
	DiscardLogger = &discardLoggerImpl{}
	DiscardEvent  = &discardEventImpl{}
)

type Logger interface {
	NewEvent(typ, name, opid string, epoch primitive.Timestamp) LogEvent
	NewDefaultEvent() LogEvent

	Close()

	SefBuffer(Buffer)

	PauseMgo()
	ResumeMgo()

	Write(p []byte) (n int, err error)

	Printf(msg string, args ...any)
	Debug(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any)
	Info(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any)
	Warning(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any)
	Error(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any)
	Fatal(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any)
	Output(ctx context.Context, e *Entry) error
}

type LogEvent interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warning(msg string, args ...any)
	Error(msg string, args ...any)
	Fatal(msg string, args ...any)
}

type Buffer interface {
	io.Writer
	Flush() error
}

type tsFormatFn func(ts int64) string

type Severity int

const (
	Fatal Severity = iota
	Error
	Warning
	Info
	Debug
)

const (
	F = "F"
	E = "E"
	W = "W"
	I = "I"
	D = "D"
)

func (s Severity) String() string {
	switch s {
	case Fatal:
		return F
	case Error:
		return E
	case Warning:
		return W
	case Info:
		return I
	case Debug:
		return D
	default:
		return ""
	}
}

func strToSeverity(s string) Severity {
	switch s {
	case F:
		return Fatal
	case E:
		return Error
	case W:
		return Warning
	case I:
		return Info
	case D:
		return Debug
	default:
		return Debug
	}
}
