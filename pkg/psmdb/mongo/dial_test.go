package mongo

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/auth"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestIsTransientError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err      error
		expected bool
	}{
		"nil": {
			err:      nil,
			expected: false,
		},
		"EOF": {
			err:      io.EOF,
			expected: true,
		},
		"wrapped EOF": {
			err:      errors.Wrap(topology.ConnectionError{ConnectionID: "conn-1", Wrapped: io.EOF}, "ping mongo"),
			expected: true,
		},
		"unexpected EOF": {
			err:      io.ErrUnexpectedEOF,
			expected: true,
		},
		"server selection": {
			err:      topology.ServerSelectionError{Wrapped: errors.New("server selection timeout")},
			expected: true,
		},
		"deadline exceeded": {
			err:      context.DeadlineExceeded,
			expected: true,
		},
		"network error label": {
			err:      mongo.CommandError{Code: 6, Message: "host unreachable", Labels: []string{"NetworkError"}},
			expected: true,
		},
		"context canceled": {
			err:      context.Canceled,
			expected: false,
		},
		"auth failure": {
			err:      topology.ConnectionError{ConnectionID: "conn-1", Wrapped: &auth.Error{}},
			expected: false,
		},
		"auth failure behind server selection": {
			err:      topology.ServerSelectionError{Wrapped: &auth.Error{}},
			expected: false,
		},
		"unauthorized command": {
			err:      mongo.CommandError{Code: 13, Message: "Unauthorized"},
			expected: false,
		},
		"arbitrary error": {
			err:      errors.New("something went wrong"),
			expected: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.expected, isTransientError(tt.err))
		})
	}
}

func TestOptionsAppName(t *testing.T) {
	t.Parallel()

	assert.Nil(t, (&Config{}).Options().AppName)

	opts := (&Config{AppName: "psmdb-operator"}).Options()
	require.NotNil(t, opts.AppName)
	assert.Equal(t, "psmdb-operator", *opts.AppName)
}

// closingListener returns the address of a listener which accepts and
// immediately closes connections.
func closingListener(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	return ln.Addr().String()
}

// TestDialRetriesTransientErrors points Dial at a listener which accepts and
// immediately closes connections, the shape of the failure a service mesh
// produces while it still rejects traffic to a new pod.
func TestDialRetriesTransientErrors(t *testing.T) {
	conf := &Config{
		Hosts:   []string{closingListener(t)},
		Direct:  true,
		Timeout: 200 * time.Millisecond,
	}

	ctx := context.Background()

	start := time.Now()
	_, err := Dial(ctx, conf)
	singleAttempt := time.Since(start)
	require.Error(t, err)
	assert.ErrorContains(t, err, "ping mongo")

	start = time.Now()
	_, err = Dial(ctx, conf, WithBackoff(&wait.Backoff{Steps: 4, Duration: 100 * time.Millisecond, Factor: 1.0}))
	withRetries := time.Since(start)
	require.Error(t, err)

	assert.Greater(t, withRetries, 2*singleAttempt, "Dial should have retried the ping")
}

func TestDialDoesNotRetryWhenContextIsDone(t *testing.T) {
	conf := &Config{
		Hosts:   []string{closingListener(t)},
		Direct:  true,
		Timeout: 10 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Dial(ctx, conf, WithBackoff(&wait.Backoff{Steps: 10, Duration: time.Second, Factor: 2.0}))
	require.Error(t, err)

	assert.Less(t, time.Since(start), 3*time.Second, "Dial should stop retrying once ctx is done")
}
