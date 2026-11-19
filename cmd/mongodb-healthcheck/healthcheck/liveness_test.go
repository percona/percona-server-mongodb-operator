package healthcheck

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	mgo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/auth"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/db"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

// livenessDialErrors are the dial errors both liveness checks get. Only a
// rejected login (code 18) means the server is alive.
var livenessDialErrors = map[string]struct {
	dialErr error
	alive   bool
}{
	"login rejected, command error": {
		dialErr: wrapDialErr(mgo.CommandError{Code: 18, Name: "AuthenticationFailed", Message: "Authentication failed."}),
		alive:   true,
	},
	"login rejected, driver error": {
		dialErr: wrapDialErr(driver.Error{Code: 18, Message: "Authentication failed."}),
		alive:   true,
	},
	"login rejected, inside a connection error": {
		dialErr: wrapDialErr(topology.ConnectionError{Wrapped: driver.Error{Code: 18, Message: "Authentication failed."}}),
		alive:   true,
	},
	"server error, other code": {
		dialErr: wrapDialErr(mgo.CommandError{Code: 13, Name: "Unauthorized", Message: "command requires authentication"}),
		alive:   false,
	},
	"server shutting down": {
		dialErr: wrapDialErr(driver.Error{Code: 11600, Message: "interrupted at shutdown"}),
		alive:   false,
	},
	"handshake failed without a server reply": {
		dialErr: wrapDialErr(&auth.Error{}),
		alive:   false,
	},
	"server unreachable": {
		dialErr: wrapDialErr(errors.New("server selection error: context deadline exceeded")),
		alive:   false,
	},
}

// wrapDialErr wraps err the way db.Dial does.
func wrapDialErr(err error) error {
	return errors.Wrap(errors.Wrap(err, "ping mongo"), "failed to dial mongo")
}

// stubDial makes dial return err until the test ends.
func stubDial(t *testing.T, err error) {
	t.Helper()

	orig := dial
	t.Cleanup(func() { dial = orig })
	dial = func(context.Context, *db.Config) (mongo.Client, error) {
		return nil, err
	}
}

// The tests below are not parallel: they replace the package-level dial.

func TestHealthCheckMongodLiveness(t *testing.T) {
	for name, tt := range livenessDialErrors {
		t.Run(name, func(t *testing.T) {
			stubDial(t, tt.dialErr)

			state, err := HealthCheckMongodLiveness(t.Context(), &db.Config{})
			if !tt.alive {
				assert.ErrorIs(t, err, tt.dialErr)
				assert.Nil(t, state)
				return
			}
			assert.NoError(t, err)
			assert.Nil(t, state, "no replication state without a login")
		})
	}
}

func TestHealthCheckMongosLiveness(t *testing.T) {
	for name, tt := range livenessDialErrors {
		t.Run(name, func(t *testing.T) {
			stubDial(t, tt.dialErr)

			err := HealthCheckMongosLiveness(t.Context(), &db.Config{})
			if !tt.alive {
				assert.ErrorIs(t, err, tt.dialErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
