package log

import (
	"context"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type discardLoggerImpl struct{}

func (l discardLoggerImpl) NewEvent(typ, name, opid string, epoch primitive.Timestamp) LogEvent {
	return l.NewDefaultEvent()
}

func (discardLoggerImpl) NewDefaultEvent() LogEvent {
	return DiscardEvent
}

func (discardLoggerImpl) Close() {
}

func (discardLoggerImpl) SefBuffer(Buffer) {
}

func (discardLoggerImpl) PauseMgo() {
}

func (discardLoggerImpl) ResumeMgo() {
}

func (discardLoggerImpl) Write([]byte) (int, error) {
	return 0, nil
}

func (discardLoggerImpl) Printf(msg string, args ...any) {
}

func (discardLoggerImpl) Debug(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any) {
}

func (discardLoggerImpl) Info(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any) {
}

func (discardLoggerImpl) Warning(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any) {
}

func (discardLoggerImpl) Error(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any) {
}

func (discardLoggerImpl) Fatal(event, obj, opid string, epoch primitive.Timestamp, msg string, args ...any) {
}

func (discardLoggerImpl) Output(ctx context.Context, e *Entry) error {
	return nil
}

type discardEventImpl struct{}

func (discardEventImpl) Debug(msg string, args ...any)   {}
func (discardEventImpl) Info(msg string, args ...any)    {}
func (discardEventImpl) Warning(msg string, args ...any) {}
func (discardEventImpl) Error(msg string, args ...any)   {}
func (discardEventImpl) Fatal(msg string, args ...any)   {}
