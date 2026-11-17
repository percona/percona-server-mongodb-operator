package perconaservermongodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	mongoFake "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo/fake"
)

type userStoreClient struct {
	mongo.Client
	users     map[string]*mongo.User
	passwords map[string]string
}

func newUserStoreClient(users ...string) *userStoreClient {
	c := &userStoreClient{
		Client:    mongoFake.NewClient(),
		users:     map[string]*mongo.User{},
		passwords: map[string]string{},
	}
	for _, name := range users {
		c.users[name] = &mongo.User{DB: "admin", Roles: []mongo.Role{{Role: "clusterMonitor", DB: "admin"}}}
		c.passwords[name] = "old-pass"
	}
	return c
}

func (c *userStoreClient) GetUserInfo(_ context.Context, username, _ string) (*mongo.User, error) {
	return c.users[username], nil
}

func (c *userStoreClient) CreateUser(_ context.Context, db, user, pwd string, roles ...mongo.Role) error {
	if _, ok := c.users[user]; ok {
		return assert.AnError
	}
	c.users[user] = &mongo.User{DB: db, Roles: roles}
	c.passwords[user] = pwd
	return nil
}

func (c *userStoreClient) UpdateUserPass(_ context.Context, _, name, pass string) error {
	if _, ok := c.users[name]; !ok {
		return assert.AnError
	}
	c.passwords[name] = pass
	return nil
}

func (c *userStoreClient) DropUser(_ context.Context, _, name string) error {
	if _, ok := c.users[name]; !ok {
		return assert.AnError
	}
	delete(c.users, name)
	delete(c.passwords, name)
	return nil
}

func TestSystemUserUpdateMongo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		existing  []string
		user      systemUser
		wantUsers []string
		wantErr   bool
	}{
		"password change": {
			existing:  []string{"clusterMonitor"},
			user:      systemUser{currName: []byte("clusterMonitor"), name: []byte("clusterMonitor"), pass: []byte("new-pass")},
			wantUsers: []string{"clusterMonitor"},
		},
		"rename": {
			existing:  []string{"newmonitorusername"},
			user:      systemUser{currName: []byte("newmonitorusername"), name: []byte("clusterMonitor"), pass: []byte("new-pass")},
			wantUsers: []string{"clusterMonitor"},
		},
		"rename when new user already exists": {
			existing:  []string{"newmonitorusername", "clusterMonitor"},
			user:      systemUser{currName: []byte("newmonitorusername"), name: []byte("clusterMonitor"), pass: []byte("new-pass")},
			wantUsers: []string{"clusterMonitor"},
		},
		"rename when current user is already dropped": {
			existing:  []string{"clusterMonitor"},
			user:      systemUser{currName: []byte("newmonitorusername"), name: []byte("clusterMonitor"), pass: []byte("new-pass")},
			wantUsers: []string{"clusterMonitor"},
		},
		"rename when neither user exists": {
			user:    systemUser{currName: []byte("newmonitorusername"), name: []byte("clusterMonitor"), pass: []byte("new-pass")},
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newUserStoreClient(tt.existing...)
			err := tt.user.updateMongo(t.Context(), c)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			got := make([]string, 0, len(c.users))
			for u := range c.users {
				got = append(got, u)
			}
			assert.ElementsMatch(t, tt.wantUsers, got)
			assert.Equal(t, "new-pass", c.passwords[string(tt.user.name)])
			assert.Equal(t, []mongo.Role{{Role: "clusterMonitor", DB: "admin"}}, c.users[string(tt.user.name)].Roles)
		})
	}
}
