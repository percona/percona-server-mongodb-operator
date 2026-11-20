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
		roles     map[string][]mongo.Role
		user      systemUser
		wantUsers []string
		wantRoles []mongo.Role
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
		"rename when new user already exists with the same roles in another order": {
			existing: []string{"newmonitorusername", "clusterMonitor"},
			roles: map[string][]mongo.Role{
				"newmonitorusername": {{Role: "clusterMonitor", DB: "admin"}, {Role: "read", DB: "local"}},
				"clusterMonitor":     {{Role: "read", DB: "local"}, {Role: "clusterMonitor", DB: "admin"}},
			},
			user:      systemUser{currName: []byte("newmonitorusername"), name: []byte("clusterMonitor"), pass: []byte("new-pass")},
			wantUsers: []string{"clusterMonitor"},
			wantRoles: []mongo.Role{{Role: "clusterMonitor", DB: "admin"}, {Role: "read", DB: "local"}},
		},
		"rename when new user already exists with different roles": {
			existing: []string{"newmonitorusername", "clusterMonitor"},
			roles: map[string][]mongo.Role{
				"clusterMonitor": {{Role: "root", DB: "admin"}},
			},
			user:      systemUser{currName: []byte("newmonitorusername"), name: []byte("clusterMonitor"), pass: []byte("new-pass")},
			wantUsers: []string{"newmonitorusername", "clusterMonitor"},
			wantErr:   true,
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
			for name, roles := range tt.roles {
				c.users[name].Roles = roles
			}
			err := tt.user.updateMongo(t.Context(), c)

			got := make([]string, 0, len(c.users))
			for u := range c.users {
				got = append(got, u)
			}
			assert.ElementsMatch(t, tt.wantUsers, got)

			if tt.wantErr {
				require.Error(t, err)
				for _, name := range tt.existing {
					assert.Equal(t, "old-pass", c.passwords[name])
				}
				return
			}
			require.NoError(t, err)

			assert.Equal(t, "new-pass", c.passwords[string(tt.user.name)])
			wantRoles := tt.wantRoles
			if wantRoles == nil {
				wantRoles = []mongo.Role{{Role: "clusterMonitor", DB: "admin"}}
			}
			assert.ElementsMatch(t, wantRoles, c.users[string(tt.user.name)].Roles)
		})
	}
}
