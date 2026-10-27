package healthcheck

import (
	"testing"

	"github.com/pkg/errors"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
)

func TestIsNotYetInitialized(t *testing.T) {
	notInitialized := mongodriver.CommandError{Code: 94, Name: "NotYetInitialized", Message: "no replset config has been received"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "command error", err: notInitialized, want: true},
		{name: "wrapped command error", err: errors.Wrap(notInitialized, "replSetGetStatus"), want: true},
		{name: "wrapped message", err: errors.Wrap(errors.New(ErrNoReplsetConfigStr), "replSetGetStatus"), want: true},
		{name: "other command error", err: errors.Wrap(mongodriver.CommandError{Code: 13, Name: "Unauthorized"}, "replSetGetStatus"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNotYetInitialized(tt.err); got != tt.want {
				t.Errorf("isNotYetInitialized(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
