package healthcheck

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/auth"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/db"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

// A system user change reaches the mounted users secret up to a minute after
// MongoDB, so the probe can fail to authenticate against a healthy mongod.
func TestLivenessDialErrors(t *testing.T) {
	orig := dial
	t.Cleanup(func() { dial = orig })

	tests := map[string]struct {
		dialErr error
		wantErr bool
	}{
		"authentication failed": {
			dialErr: errors.Wrap(errors.Wrap(&auth.Error{}, "ping mongo"), "failed to dial mongo"),
			wantErr: false,
		},
		"server unreachable": {
			dialErr: errors.Wrap(errors.New("server selection error: context deadline exceeded"), "ping mongo"),
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dial = func(context.Context, *db.Config) (mongo.Client, error) {
				return nil, tt.dialErr
			}

			state, err := HealthCheckMongodLiveness(t.Context(), &db.Config{})
			if tt.wantErr {
				require.Error(t, err, "mongod liveness")
			} else {
				require.NoError(t, err, "mongod liveness")
				assert.Nil(t, state, "no replication state without a connection")
			}

			err = HealthCheckMongosLiveness(t.Context(), &db.Config{})
			if tt.wantErr {
				require.Error(t, err, "mongos liveness")
			} else {
				require.NoError(t, err, "mongos liveness")
			}
		})
	}
}
