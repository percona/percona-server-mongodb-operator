package perconaservermongodb

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func TestRolesChanged(t *testing.T) {
	r2 := &mongo.Role{
		Privileges: []mongo.RolePrivilege{
			{
				Resource: map[string]interface{}{
					"db":         "test",
					"collection": "test",
				},
				Actions: []string{"find"},
			},
			{
				Resource: map[string]interface{}{
					"db":         "test-two",
					"collection": "test-two",
				},
				Actions: []string{"find", "insert"},
			},
		},
		AuthenticationRestrictions: []mongo.RoleAuthenticationRestriction{
			{
				ClientSource: []string{"localhost", "111.111.111.111"},
			},
			{
				ServerAddress: []string{"localhost", "10.10.10.10"},
				ClientSource:  []string{"localhost", "111.111.111.111"},
			},
		},
		Roles: []mongo.InheritenceRole{
			{
				Role: "read",
				DB:   "test",
			},
			{
				Role: "insert",
				DB:   "test",
			},
		},
	}

	tests := []struct {
		name string
		r1   *mongo.Role
		r2   *mongo.Role
		want bool
	}{
		{
			name: "Roles the same",
			want: false,
			r1: &mongo.Role{
				Privileges: []mongo.RolePrivilege{
					{
						Resource: map[string]interface{}{
							"collection": "test",
							"db":         "test",
						},
						Actions: []string{"find"},
					},
					{
						Resource: map[string]interface{}{
							"db":         "test-two",
							"collection": "test-two",
						},
						Actions: []string{"insert", "find"},
					},
				},
				AuthenticationRestrictions: []mongo.RoleAuthenticationRestriction{
					{
						ClientSource: []string{"111.111.111.111", "localhost"},
					},
					{
						ServerAddress: []string{"10.10.10.10", "localhost"},
						ClientSource:  []string{"localhost", "111.111.111.111"},
					},
				},
				Roles: []mongo.InheritenceRole{
					{
						Role: "read",
						DB:   "test",
					},
					{
						Role: "insert",
						DB:   "test",
					},
				},
			},
			r2: r2,
		},
		{
			name: "Roles different",
			want: true,
			r1: &mongo.Role{
				Privileges: []mongo.RolePrivilege{
					{
						Resource: map[string]interface{}{
							"collection": "test",
							"db":         "test",
						},
						Actions: []string{"find", "update"},
					},
					{
						Resource: map[string]interface{}{
							"db":         "test-two",
							"collection": "test-two-different",
						},
						Actions: []string{"insert"},
					},
				},
				AuthenticationRestrictions: []mongo.RoleAuthenticationRestriction{
					{
						ClientSource: []string{"111.111.111.111", "localhost"},
					},
					{
						ServerAddress: []string{"10.10.10.10", "localhost"},
						ClientSource:  []string{"localhost", "111.111.111.111"},
					},
				},
				Roles: []mongo.InheritenceRole{
					{
						Role: "read",
						DB:   "test",
					},
					{
						Role: "update",
						DB:   "test-two",
					},
					{
						Role: "insert",
						DB:   "test",
					},
				},
			},
			r2: r2,
		},
		{
			name: "Privileges different",
			want: true,
			r1: &mongo.Role{
				Privileges: []mongo.RolePrivilege{
					{
						Resource: map[string]interface{}{
							"collection": "test",
							"db":         "test",
						},
						Actions: []string{"find", "update"},
					},
					{
						Resource: map[string]interface{}{
							"db":         "test-two",
							"collection": "test-two-different",
						},
						Actions: []string{"insert"},
					},
				},
				AuthenticationRestrictions: []mongo.RoleAuthenticationRestriction{
					{
						ClientSource: []string{"111.111.111.111", "localhost"},
					},
					{
						ServerAddress: []string{"10.10.10.10", "localhost"},
						ClientSource:  []string{"localhost", "111.111.111.111"},
					},
				},
				Roles: []mongo.InheritenceRole{
					{
						Role: "read",
						DB:   "test",
					},
					{
						Role: "insert",
						DB:   "test",
					},
				},
			},
			r2: r2,
		},
		{
			name: "AuthenticationRestrictions different",
			want: true,
			r1: &mongo.Role{
				Privileges: []mongo.RolePrivilege{
					{
						Resource: map[string]interface{}{
							"db":         "test",
							"collection": "test",
						},
						Actions: []string{"find"},
					},
					{
						Resource: map[string]interface{}{
							"collection": "test-two",
							"db":         "test-two",
						},
						Actions: []string{"insert", "find"},
					},
				},
				AuthenticationRestrictions: []mongo.RoleAuthenticationRestriction{
					{
						ServerAddress: []string{"1.1.1.1", "localhost"},
					},
					{
						ClientSource: []string{"localhost"},
					},
				},
				Roles: []mongo.InheritenceRole{
					{
						Role: "read",
						DB:   "test",
					},
					{
						Role: "insert",
						DB:   "test",
					},
				},
			},
			r2: r2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rolesChanged(tt.r1, tt.r2); got != tt.want {
				t.Errorf("rolesChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateUser(t *testing.T) {
	ctx := context.Background()

	tests := map[string]struct {
		user            *api.User
		actualUser      *api.User
		sysUserNames    map[string]struct{}
		uniqueUserNames map[string]struct{}
		expectedErr     error
	}{
		"invalid input for sysUserNames and uniqueUserNames": {
			user:        &api.User{Name: "john", Roles: []api.UserRole{{Name: "rolename", DB: "testdb"}}, DB: "testdb"},
			expectedErr: errors.New("invalid sys or unique usernames config"),
		},
		"valid non-existing username": {
			user:            &api.User{Name: "john", Roles: []api.UserRole{{Name: "rolename", DB: "testdb"}}, DB: "testdb"},
			actualUser:      &api.User{Name: "john", Roles: []api.UserRole{{Name: "rolename", DB: "testdb"}}, DB: "testdb"},
			sysUserNames:    map[string]struct{}{},
			uniqueUserNames: map[string]struct{}{},
		},
		"valid non-existing username, missing db and password secret ref": {
			user: &api.User{Name: "john", Roles: []api.UserRole{{Name: "rolename"}}, PasswordSecretRef: &api.SecretKeySelector{}},
			actualUser: &api.User{
				Name:              "john",
				Roles:             []api.UserRole{{Name: "rolename"}},
				DB:                "admin",
				PasswordSecretRef: &api.SecretKeySelector{Key: "password"},
			},
			sysUserNames:    map[string]struct{}{},
			uniqueUserNames: map[string]struct{}{},
		},
		"sys reserved username": {
			user:            &api.User{Name: "root", Roles: []api.UserRole{{Name: "rolename", DB: "testdb"}}, DB: "testdb"},
			sysUserNames:    map[string]struct{}{"root": {}},
			uniqueUserNames: map[string]struct{}{},
			expectedErr:     errors.New("sys reserved username"),
		},
		"not unique username": {
			user:            &api.User{Name: "useradmin", Roles: []api.UserRole{{Name: "rolename", DB: "testdb"}}, DB: "testdb"},
			sysUserNames:    map[string]struct{}{},
			uniqueUserNames: map[string]struct{}{"useradmin": {}},
			expectedErr:     errors.New("username should be unique"),
		},
		"no roles defined": {
			user:            &api.User{Name: "john", Roles: []api.UserRole{}, DB: "testdb"},
			sysUserNames:    map[string]struct{}{},
			uniqueUserNames: map[string]struct{}{},
			expectedErr:     errors.New("user must have at least one role"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateUser(ctx, tt.user, tt.sysUserNames, tt.uniqueUserNames)
			if tt.expectedErr != nil {
				assert.EqualError(t, err, tt.expectedErr.Error())
			} else {
				assert.Equal(t, tt.user, tt.actualUser)
				assert.NoError(t, err)
			}
		})
	}
}
