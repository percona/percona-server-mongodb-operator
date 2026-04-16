package log

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// eventImpl provides logging for some event (backup, restore)
type eventImpl struct {
	l    *loggerImpl
	typ  string
	obj  string
	ep   primitive.Timestamp
	opid string
}

func (e *eventImpl) Debug(msg string, args ...any) {
	e.l.Debug(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

func (e *eventImpl) Info(msg string, args ...any) {
	e.l.Info(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

func (e *eventImpl) Warning(msg string, args ...any) {
	e.l.Warning(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

func (e *eventImpl) Error(msg string, args ...any) {
	e.l.Error(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

func (e *eventImpl) Fatal(msg string, args ...any) {
	e.l.Fatal(e.typ, e.obj, e.opid, e.ep, msg, args...)
}

func (e *eventImpl) GetLogger() Logger {
	return e.l
}
