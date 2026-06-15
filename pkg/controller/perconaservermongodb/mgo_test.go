package perconaservermongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func TestCompareTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		mongoTags    mongo.ReplsetTags
		selectorTags api.PrimaryPreferTagSelectorSpec
		expected     bool
	}{
		{
			name:         "empty tags",
			mongoTags:    mongo.ReplsetTags{},
			selectorTags: api.PrimaryPreferTagSelectorSpec{},
			expected:     false,
		},
		{
			name:         "selector with podName",
			mongoTags:    mongo.ReplsetTags{},
			selectorTags: api.PrimaryPreferTagSelectorSpec{"podName": "test"},
			expected:     false,
		},
		{
			name:         "match selector with podName",
			mongoTags:    mongo.ReplsetTags{"podName": "test"},
			selectorTags: api.PrimaryPreferTagSelectorSpec{"podName": "test"},
			expected:     true,
		},
		{
			name:         "match selector with podName and other tags",
			mongoTags:    mongo.ReplsetTags{"podName": "test", "other": "tag"},
			selectorTags: api.PrimaryPreferTagSelectorSpec{"podName": "test"},
			expected:     true,
		},
		{
			name:         "match two selectors with podName and other tags",
			mongoTags:    mongo.ReplsetTags{"podName": "test", "other": "tag"},
			selectorTags: api.PrimaryPreferTagSelectorSpec{"podName": "test", "other": "tag"},
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareTags(tt.mongoTags, tt.selectorTags); got != tt.expected {
				t.Errorf("compareTags() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDefaultRWConcern(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		spec              *api.DefaultRWConcern
		wantReadConcern   string
		wantWriteConcern  string
	}{
		"nil spec falls back to majority": {
			spec:             nil,
			wantReadConcern:  mongo.DefaultReadConcern,
			wantWriteConcern: mongo.DefaultWriteConcern,
		},
		"empty fields fall back to majority": {
			spec:             &api.DefaultRWConcern{},
			wantReadConcern:  mongo.DefaultReadConcern,
			wantWriteConcern: mongo.DefaultWriteConcern,
		},
		"only read overridden": {
			spec:             &api.DefaultRWConcern{ReadConcern: "local"},
			wantReadConcern:  "local",
			wantWriteConcern: mongo.DefaultWriteConcern,
		},
		"only write overridden": {
			spec:             &api.DefaultRWConcern{WriteConcern: "1"},
			wantReadConcern:  mongo.DefaultReadConcern,
			wantWriteConcern: "1",
		},
		"both overridden": {
			spec:             &api.DefaultRWConcern{ReadConcern: "local", WriteConcern: "1"},
			wantReadConcern:  "local",
			wantWriteConcern: "1",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{DefaultRWConcern: tt.spec},
			}
			gotRead, gotWrite := defaultRWConcern(cr)
			assert.Equal(t, tt.wantReadConcern, gotRead)
			assert.Equal(t, tt.wantWriteConcern, gotWrite)
		})
	}
}

func TestGetRoles(t *testing.T) {
	tests := map[string]struct {
		crVersion string
		role      api.SystemUserRole
		expected  []mongo.Role
	}{
		"RoleDatabaseAdmin": {
			role: api.RoleDatabaseAdmin,
			expected: []mongo.Role{
				{DB: "admin", Role: "readWriteAnyDatabase"},
				{DB: "admin", Role: "readAnyDatabase"},
				{DB: "admin", Role: "restore"},
				{DB: "admin", Role: "backup"},
				{DB: "admin", Role: "dbAdminAnyDatabase"},
				{DB: "admin", Role: string(api.RoleClusterMonitor)},
			},
		},
		"RoleClusterMonitor with version >= 1.20.0": {
			crVersion: "1.20.0",
			role:      api.RoleClusterMonitor,
			expected: []mongo.Role{
				{DB: "admin", Role: "explainRole"},
				{DB: "local", Role: "read"},
				{DB: "admin", Role: "directShardOperations"},
				{DB: "admin", Role: string(api.RoleClusterMonitor)},
			},
		},
		"RoleClusterMonitor with version < 1.20.0": {
			crVersion: "1.19.0",
			role:      api.RoleClusterMonitor,
			expected: []mongo.Role{
				{DB: "admin", Role: "explainRole"},
				{DB: "local", Role: "read"},
				{DB: "admin", Role: string(api.RoleClusterMonitor)},
			},
		},
		"RoleBackup": {
			role: api.RoleBackup,
			expected: []mongo.Role{
				{DB: "admin", Role: "readWrite"},
				{DB: "admin", Role: string(api.RoleClusterMonitor)},
				{DB: "admin", Role: "restore"},
				{DB: "admin", Role: "pbmAnyAction"},
				{DB: "admin", Role: string(api.RoleBackup)},
			},
		},
		"RoleClusterAdmin": {
			crVersion: "1.19.0",
			role:      api.RoleClusterAdmin,
			expected: []mongo.Role{
				{DB: "admin", Role: string(api.RoleClusterAdmin)},
			},
		},
		"RoleClusterAdmin with version >= 1.20.0": {
			crVersion: "1.20.0",
			role:      api.RoleClusterAdmin,
			expected: []mongo.Role{
				{DB: "admin", Role: "directShardOperations"},
				{DB: "admin", Role: string(api.RoleClusterAdmin)},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{Spec: api.PerconaServerMongoDBSpec{CRVersion: tt.crVersion}}
			actual := getRoles(cr, tt.role)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestCompareRoles(t *testing.T) {
	tests := map[string]struct {
		x        []mongo.Role
		y        []mongo.Role
		expected bool
	}{
		"length is different": {
			x: []mongo.Role{
				{DB: "admin", Role: string(api.RoleClusterAdmin)},
			},
			y: []mongo.Role{
				{DB: "admin", Role: "directShardOperations"},
				{DB: "admin", Role: string(api.RoleClusterAdmin)},
			},
			expected: false,
		},
		"order is different": {
			x: []mongo.Role{
				{DB: "admin", Role: string(api.RoleClusterAdmin)},
				{DB: "admin", Role: "directShardOperations"},
			},
			y: []mongo.Role{
				{DB: "admin", Role: "directShardOperations"},
				{DB: "admin", Role: string(api.RoleClusterAdmin)},
			},
			expected: true,
		},
		"one role is different": {
			x: []mongo.Role{
				{DB: "admin", Role: "readWriteAnyDatabase"},
				{DB: "admin", Role: "readAnyDatabase"},
				{DB: "admin", Role: "restore"},
				{DB: "admin", Role: "backup"},
				{DB: "admin", Role: "dbAdminAnyDatabase"},
				{DB: "admin", Role: string(api.RoleClusterMonitor)},
			},
			y: []mongo.Role{
				{DB: "admin", Role: "readWriteAnyDatabase"},
				{DB: "admin", Role: "readAnyDatabase"},
				{DB: "admin", Role: "restore"},
				{DB: "admin", Role: "backup"},
				{DB: "admin", Role: "dbAdminAnyDatabase2"},
				{DB: "admin", Role: string(api.RoleClusterMonitor)},
			},
			expected: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			actual := compareRoles(tt.x, tt.y)
			assert.Equal(t, tt.expected, actual)
		})
	}
}
