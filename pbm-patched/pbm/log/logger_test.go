package log

import (
	"io"
	"os"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestLoggerConstructor(t *testing.T) {
	t.Run("logger logs without db connection", func(t *testing.T) {
		t.Run("logger with opts", func(t *testing.T) {
			f := "db-conn-nil"
			l := NewWithOpts(nil, "rs", "node", &Opts{LogPath: f})
			defer os.Remove(f)

			l.Debug("", "", "", primitive.Timestamp{}, "msg: %v", "nil conn")

			lEntry, _ := os.ReadFile(f)
			if !strings.Contains(string(lEntry), "msg: nil conn") {
				t.Errorf("expected: 'msg: nil conn', got: %q", string(lEntry))
			}
		})

		t.Run("logger without opts", func(t *testing.T) {
			old := os.Stderr
			r, w, _ := os.Pipe()
			os.Stderr = w
			defer func() { os.Stderr = old }()

			l := New(nil, "rs", "node").NewDefaultEvent()
			msg := "msg from logger without opts"

			l.Info(msg)

			w.Close()
			stdErrOut, _ := io.ReadAll(r)

			if !strings.Contains(string(stdErrOut), msg) {
				t.Errorf("expected: %q, got: %q", msg, string(stdErrOut))
			}
		})
	})
}
