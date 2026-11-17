package healthcheck

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	mgo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/auth"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/db"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

// A system user change reaches the mounted users secret up to a minute after
// MongoDB, so the probe can be refused by a healthy mongod. Only a rejected
// login may pass: other handshake errors can come from a hung server.
func TestLivenessDialErrors(t *testing.T) {
	orig := dial
	t.Cleanup(func() { dial = orig })

	wrap := func(err error) error {
		return errors.Wrap(errors.Wrap(err, "ping mongo"), "failed to dial mongo")
	}

	tests := map[string]struct {
		dialErr error
		alive   bool
	}{
		"login rejected, command error": {
			dialErr: wrap(mgo.CommandError{Code: 18, Name: "AuthenticationFailed", Message: "Authentication failed."}),
			alive:   true,
		},
		"login rejected, driver error": {
			dialErr: wrap(driver.Error{Code: 18, Message: "Authentication failed."}),
			alive:   true,
		},
		"handshake failed without a server reply": {
			dialErr: wrap(&auth.Error{}),
			alive:   false,
		},
		"server unreachable": {
			dialErr: wrap(errors.New("server selection error: context deadline exceeded")),
			alive:   false,
		},
	}

	for name, tt := range tests {
		dial = func(context.Context, *db.Config) (mongo.Client, error) {
			return nil, tt.dialErr
		}

		t.Run(name+"/mongod", func(t *testing.T) {
			state, err := HealthCheckMongodLiveness(t.Context(), &db.Config{})
			if tt.alive {
				assert.NoError(t, err)
				assert.Nil(t, state, "no replication state without a login")
			} else {
				assert.Error(t, err)
			}
		})

		t.Run(name+"/mongos", func(t *testing.T) {
			err := HealthCheckMongosLiveness(t.Context(), &db.Config{})
			if tt.alive {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
