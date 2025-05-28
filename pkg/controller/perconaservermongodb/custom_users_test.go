package perconaservermongodb

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
			expectedErr:     errors.New("creating user with reserved user name root is forbidden"),
		},
		"not unique username": {
			user:            &api.User{Name: "useradmin", Roles: []api.UserRole{{Name: "rolename", DB: "testdb"}}, DB: "testdb"},
			sysUserNames:    map[string]struct{}{},
			uniqueUserNames: map[string]struct{}{"useradmin": {}},
			expectedErr:     errors.New("username useradmin should be unique"),
		},
		"no roles defined": {
			user:            &api.User{Name: "john", Roles: []api.UserRole{}, DB: "testdb"},
			sysUserNames:    map[string]struct{}{},
			uniqueUserNames: map[string]struct{}{},
			expectedErr:     errors.New("user john must have at least one role"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateUser(tt.user, tt.sysUserNames, tt.uniqueUserNames)
			if tt.expectedErr != nil {
				assert.EqualError(t, err, tt.expectedErr.Error())
			} else {
				assert.Equal(t, tt.user, tt.actualUser)
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetCustomUserSecret(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	assert.NoError(t, err)
	err = api.SchemeBuilder.AddToScheme(scheme)
	assert.NoError(t, err)

	ns := "test-ns"
	passKey := "password"

	tests := map[string]struct {
		crName            string
		client            func() client.Client
		user              *api.User
		hasExistingSecret bool
		errMsg            string
	}{
		"create default secret if not exists": {
			crName: "my-cluster-create-default-secret",
			client: func() client.Client {
				return fake.NewClientBuilder().WithScheme(scheme).Build()
			},
			user: &api.User{},
		},
		"user has custom secret reference that exists": {
			crName: "my-cluster-user-has-secret",
			client: func() client.Client {
				existingSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "custom-secret",
						Namespace: ns,
					},
					Data: map[string][]byte{
						passKey: []byte("existing-password"),
					},
				}

				return fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingSecret).Build()
			},
			user: &api.User{
				PasswordSecretRef: &api.SecretKeySelector{
					Name: "custom-secret",
				},
			},
			hasExistingSecret: true,
		},
		"user has custom secret reference but secret does not exist": {
			crName: "my-cluster-has-missing-secret",
			client: func() client.Client {
				return fake.NewClientBuilder().WithScheme(scheme).Build()
			},
			user: &api.User{
				PasswordSecretRef: &api.SecretKeySelector{
					Name: "missing-secret",
				},
			},
			errMsg: "failed to get user secret",
		},
		"existing default secret missing password key, create new": {
			crName: "my-cluster-existing-secret-missing-password",
			client: func() client.Client {
				defaultSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-cluster-existing-secret-missing-password-custom-user-secret",
						Namespace: ns,
					},
					Data: map[string][]byte{},
				}

				return fake.NewClientBuilder().WithScheme(scheme).WithObjects(defaultSecret).Build()
			},
			user: &api.User{},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.crName,
					Namespace: ns,
				},
			}

			secret, err := getCustomUserSecret(ctx, tt.client(), cr, tt.user, passKey)
			if tt.hasExistingSecret && tt.errMsg == "" {
				assert.NoError(t, err)
				assert.Equal(t, secret.Name, "custom-secret")
				assert.Equal(t, string(secret.Data[passKey]), "existing-password")
				return
			}
			if !tt.hasExistingSecret && tt.errMsg == "" {
				assert.NoError(t, err)
				assert.Equal(t, secret.Name, tt.crName+"-custom-user-secret")
				assert.NotEmpty(t, string(secret.Data[passKey]))
			}
			if tt.errMsg != "" {
				assert.Nil(t, secret)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			}

		})
	}
}
