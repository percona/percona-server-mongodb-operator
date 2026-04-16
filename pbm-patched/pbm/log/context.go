package log

import "context"

type logTag string

const (
	loggerTag   logTag = "pbm:logger"
	logEventTag logTag = "pbm:log:event"
)

func FromContext(ctx context.Context) Logger {
	val := ctx.Value(loggerTag)
	if val == nil {
		return DiscardLogger
	}

	ev, ok := val.(Logger)
	if !ok {
		return DiscardLogger
	}

	return ev
}

func SetLoggerToContext(ctx context.Context, ev Logger) context.Context {
	return context.WithValue(ctx, loggerTag, ev)
}

func LogEventFromContext(ctx context.Context) LogEvent {
	val := ctx.Value(logEventTag)
	if val == nil {
		return FromContext(ctx).NewDefaultEvent()
	}

	ev, ok := val.(LogEvent)
	if !ok {
		return FromContext(ctx).NewDefaultEvent()
	}

	return ev
}

func SetLogEventToContext(ctx context.Context, ev LogEvent) context.Context {
	return context.WithValue(ctx, logEventTag, ev)
}

func Copy(to, from context.Context) context.Context {
	to = SetLoggerToContext(to, FromContext(from))
	to = SetLogEventToContext(to, LogEventFromContext(from))
	return to
}
