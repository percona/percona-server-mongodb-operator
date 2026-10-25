package perconaservermongodb

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	mongoFake "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo/fake"
)

func TestMongoExpression(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		function string
		argument any
		expected string
	}{
		{
			name:     "replica set configuration",
			function: "rs.initiate",
			argument: map[string]any{
				"_id": "rs0",
				"members": []map[string]any{
					{"_id": 0, "host": "mongo-0:27017", "arbiterOnly": false},
				},
				"version": 1,
			},
			expected: `rs.initiate({"_id":"rs0","members":[{"_id":0,"arbiterOnly":false,"host":"mongo-0:27017"}],"version":1})`,
		},
		{
			name:     "user credentials with special characters",
			function: `db.getSiblingDB("admin").createUser`,
			argument: map[string]any{
				"user":  "user'Admin",
				"pwd":   "quote\"back\\slash\n\t$(false);`false`$HOME",
				"roles": []string{"userAdminAnyDatabase"},
			},
			expected: `db.getSiblingDB("admin").createUser({"pwd":"quote\"back\\slash\n\t$(false);` + "`false`" + `$HOME","roles":["userAdminAnyDatabase"],"user":"user'Admin"})`,
		},
		{
			name:     "string argument with special characters",
			function: "print",
			argument: `<>&"'\`,
			expected: `print("\u003c\u003e\u0026\"'\\")`,
		},
		{
			name:     "nil argument",
			function: "print",
			expected: "print(null)",
		},
		{
			name:     "empty document",
			function: "rs.initiate",
			argument: map[string]any{},
			expected: "rs.initiate({})",
		},
		{
			name:     "empty array",
			function: "print",
			argument: []string{},
			expected: "print([])",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expression, err := mongoExpression(tt.function, tt.argument)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, expression)
		})
	}

	t.Run("unsupported argument", func(t *testing.T) {
		t.Parallel()

		expression, err := mongoExpression("rs.initiate", map[string]any{"unsupported": make(chan int)})
		require.ErrorContains(t, err, "marshal mongo expression argument")
		assert.Empty(t, expression)
		var unsupportedTypeErr *json.UnsupportedTypeError
		assert.ErrorAs(t, err, &unsupportedTypeErr)
	})
}

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
		spec             *api.DefaultRWConcern
		wantReadConcern  string
		wantWriteConcern string
		wantWTimeout     int
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
		"only write w overridden": {
			spec:             &api.DefaultRWConcern{WriteConcern: &api.DefaultWriteConcernSpec{W: "1"}},
			wantReadConcern:  mongo.DefaultReadConcern,
			wantWriteConcern: "1",
		},
		"wtimeout overridden": {
			spec:             &api.DefaultRWConcern{WriteConcern: &api.DefaultWriteConcernSpec{W: "majority", WTimeout: 5000}},
			wantReadConcern:  mongo.DefaultReadConcern,
			wantWriteConcern: "majority",
			wantWTimeout:     5000,
		},
		"empty writeConcern struct keeps defaults": {
			spec:             &api.DefaultRWConcern{WriteConcern: &api.DefaultWriteConcernSpec{}},
			wantReadConcern:  mongo.DefaultReadConcern,
			wantWriteConcern: mongo.DefaultWriteConcern,
		},
		"all overridden": {
			spec: &api.DefaultRWConcern{
				ReadConcern:  "local",
				WriteConcern: &api.DefaultWriteConcernSpec{W: "1", WTimeout: 250},
			},
			wantReadConcern:  "local",
			wantWriteConcern: "1",
			wantWTimeout:     250,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{DefaultRWConcern: tt.spec},
			}
			gotRead, gotWrite, gotWTimeout := defaultRWConcern(cr)
			assert.Equal(t, tt.wantReadConcern, gotRead)
			assert.Equal(t, tt.wantWriteConcern, gotWrite)
			assert.Equal(t, tt.wantWTimeout, gotWTimeout)
		})
	}
}

func TestShouldSetDefaultRWConcern(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		shardingEnabled bool
		arbiterEnabled  bool
		externalNodes   []*api.ExternalNode
		rwConcern       *api.DefaultRWConcern
		want            bool
	}{
		"PSS, no custom concern": {
			want: false,
		},
		"PSA, no custom concern": {
			arbiterEnabled: true,
			want:           true,
		},
		"PSS, custom concern": {
			rwConcern: &api.DefaultRWConcern{WriteConcern: &api.DefaultWriteConcernSpec{W: "1"}},
			want:      true,
		},
		"PSA, custom concern": {
			arbiterEnabled: true,
			rwConcern:      &api.DefaultRWConcern{WriteConcern: &api.DefaultWriteConcernSpec{W: "1"}},
			want:           true,
		},
		"external arbiter, no custom concern": {
			externalNodes: []*api.ExternalNode{{ArbiterOnly: true}},
			want:          true,
		},
		"external node without arbiter, no custom concern": {
			externalNodes: []*api.ExternalNode{{ArbiterOnly: false}},
			want:          false,
		},
		"external arbiter among data-bearing external nodes": {
			externalNodes: []*api.ExternalNode{{ArbiterOnly: false}, {ArbiterOnly: true}},
			want:          true,
		},
		"external arbiter, custom concern": {
			externalNodes: []*api.ExternalNode{{ArbiterOnly: true}},
			rwConcern:     &api.DefaultRWConcern{WriteConcern: &api.DefaultWriteConcernSpec{W: "1"}},
			want:          true,
		},
		"sharded, PSA": {
			shardingEnabled: true,
			arbiterEnabled:  true,
			want:            false,
		},
		"sharded, external arbiter": {
			shardingEnabled: true,
			externalNodes:   []*api.ExternalNode{{ArbiterOnly: true}},
			want:            false,
		},
		"sharded, custom concern": {
			shardingEnabled: true,
			rwConcern:       &api.DefaultRWConcern{WriteConcern: &api.DefaultWriteConcernSpec{W: "1"}},
			want:            false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					Sharding:         api.Sharding{Enabled: tc.shardingEnabled},
					DefaultRWConcern: tc.rwConcern,
				},
			}
			rs := &api.ReplsetSpec{
				Arbiter:       api.Arbiter{Enabled: tc.arbiterEnabled},
				ExternalNodes: tc.externalNodes,
			}
			assert.Equal(t, tc.want, shouldSetDefaultRWConcern(cr, rs))
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

func TestUpdateConfigMembersExternalNodeLimit(t *testing.T) {
	ctx := context.Background()
	cr := newTestCR()
	cr.Spec.ClusterServiceDNSMode = api.DNSModeInternal
	cr.Spec.ClusterServiceDNSSuffix = "svc.cluster.local"
	rs := cr.Spec.Replsets[0]
	rs.Size = 49
	objects := fakePodsForRS(cr, rs)
	objects = append(objects, fakeStatefulset(cr, rs, rs.Size, "", naming.ComponentMongod))
	r := buildFakeClient(objects...)
	pods, err := psmdb.GetRSPods(ctx, r.client, cr, rs.Name)
	require.NoError(t, err)
	require.Len(t, pods.Items, 49)

	initialConfig := mongo.RSConfig{Version: 1}
	for i := range pods.Items {
		member, err := r.getConfigMemberForPod(ctx, cr, rs, i, &pods.Items[i])
		require.NoError(t, err)
		initialConfig.Members = append(initialConfig.Members, member)
	}
	initialConfig.Members.SetVotes(initialConfig.Members, false)
	externalNodes := []*api.ExternalNode{
		{Host: "external-0.example.com", Port: 27017},
		{Host: "external-1.example.com", Port: 27017},
	}

	tests := map[string]struct {
		externalNodes   int
		expectedMembers int
	}{
		"below limit": {externalNodes: 0, expectedMembers: 49},
		"at limit":    {externalNodes: 1, expectedMembers: 50},
		"above limit": {externalNodes: 2, expectedMembers: 50},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rs.ExternalNodes = externalNodes[:tc.externalNodes]
			cli := mongoFake.NewClientWithConfig(initialConfig)

			// AddNew adds one member per call; repeat to check the 51st stays excluded.
			for range 2 {
				_, _, err := r.updateConfigMembers(ctx, cli, cr, rs)
				require.NoError(t, err)
				config, err := cli.ReadConfig(ctx)
				require.NoError(t, err)

				var hosts []string
				for _, member := range config.Members {
					hosts = append(hosts, member.Host)
				}
				assert.Len(t, config.Members, tc.expectedMembers)
				if tc.externalNodes > 0 {
					assert.Contains(t, hosts, "external-0.example.com:27017")
				}
				assert.NotContains(t, hosts, "external-1.example.com:27017")
			}
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

func TestLiveMembers(t *testing.T) {
	t.Parallel()

	managed := func(id int, host, podName string) mongo.ConfigMember {
		return mongo.ConfigMember{
			ID:   id,
			Host: host,
			Tags: mongo.ReplsetTags{"podName": podName},
		}
	}

	tests := []struct {
		name              string
		rsStatus          mongo.Status
		cnf               mongo.RSConfig
		rs                *api.ReplsetSpec
		expectedLive      int
		expectedRSMembers map[string]api.ReplsetMemberStatus
	}{
		{
			name: "primary secondary secondary all live",
			cnf: mongo.RSConfig{Members: mongo.ConfigMembers{
				managed(0, "rs0-0:27017", "rs0-0"),
				managed(1, "rs0-1:27017", "rs0-1"),
				managed(2, "rs0-2:27017", "rs0-2"),
			}},
			rsStatus: mongo.Status{Members: []*mongo.Member{
				{Id: 0, Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				{Id: 1, Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
				{Id: 2, Name: "rs0-2:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
			}},
			rs:           &api.ReplsetSpec{},
			expectedLive: 3,
			expectedRSMembers: map[string]api.ReplsetMemberStatus{
				"rs0-0": {Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				"rs0-1": {Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
				"rs0-2": {Name: "rs0-2:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
			},
		},
		{
			name: "non-live states are not counted",
			cnf: mongo.RSConfig{Members: mongo.ConfigMembers{
				managed(0, "rs0-0:27017", "rs0-0"),
				managed(1, "rs0-1:27017", "rs0-1"),
				managed(2, "rs0-2:27017", "rs0-2"),
			}},
			rsStatus: mongo.Status{Members: []*mongo.Member{
				{Id: 0, Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				{Id: 1, Name: "rs0-1:27017", State: mongo.MemberStateRecovering, StateStr: "RECOVERING"},
				{Id: 2, Name: "rs0-2:27017", State: mongo.MemberStateDown, StateStr: "(not reachable/healthy)"},
			}},
			rs:           &api.ReplsetSpec{},
			expectedLive: 1,
			expectedRSMembers: map[string]api.ReplsetMemberStatus{
				"rs0-0": {Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				"rs0-1": {Name: "rs0-1:27017", State: mongo.MemberStateRecovering, StateStr: "RECOVERING"},
				"rs0-2": {Name: "rs0-2:27017", State: mongo.MemberStateDown, StateStr: "(not reachable/healthy)"},
			},
		},
		{
			name: "in-cluster arbiter is counted",
			cnf: mongo.RSConfig{Members: mongo.ConfigMembers{
				managed(0, "rs0-0:27017", "rs0-0"),
				managed(1, "rs0-1:27017", "rs0-1"),
				{ID: 2, Host: "rs0-arbiter-0:27017", ArbiterOnly: true},
			}},
			rsStatus: mongo.Status{Members: []*mongo.Member{
				{Id: 0, Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				{Id: 1, Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
				{Id: 2, Name: "rs0-arbiter-0:27017", State: mongo.MemberStateArbiter, StateStr: "ARBITER"},
			}},
			rs:           &api.ReplsetSpec{},
			expectedLive: 3,
			expectedRSMembers: map[string]api.ReplsetMemberStatus{
				"rs0-0": {Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				"rs0-1": {Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
			},
		},
		{
			name: "external member is skipped",
			cnf: mongo.RSConfig{Members: mongo.ConfigMembers{
				managed(0, "rs0-0:27017", "rs0-0"),
				managed(1, "rs0-1:27017", "rs0-1"),
				{ID: 2, Host: "external.example.com:27017", Tags: mongo.ReplsetTags{"external": "true"}},
			}},
			rsStatus: mongo.Status{Members: []*mongo.Member{
				{Id: 0, Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				{Id: 1, Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
				{Id: 2, Name: "external.example.com:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
			}},
			rs:           &api.ReplsetSpec{},
			expectedLive: 2,
			expectedRSMembers: map[string]api.ReplsetMemberStatus{
				"rs0-0": {Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				"rs0-1": {Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
			},
		},
		{
			name: "external arbiter is skipped",
			cnf: mongo.RSConfig{Members: mongo.ConfigMembers{
				managed(0, "rs0-0:27017", "rs0-0"),
				managed(1, "rs0-1:27017", "rs0-1"),
				{ID: 2, Host: "arbiter.example.com:27017", ArbiterOnly: true},
			}},
			rsStatus: mongo.Status{Members: []*mongo.Member{
				{Id: 0, Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				{Id: 1, Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
				{Id: 2, Name: "arbiter.example.com:27017", State: mongo.MemberStateArbiter, StateStr: "ARBITER"},
			}},
			rs: &api.ReplsetSpec{
				ExternalNodes: []*api.ExternalNode{
					{Host: "arbiter.example.com", Port: 27017, ArbiterOnly: true},
				},
			},
			expectedLive: 2,
			expectedRSMembers: map[string]api.ReplsetMemberStatus{
				"rs0-0": {Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				"rs0-1": {Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
			},
		},
		{
			name: "non-arbiter external node does not skip in-cluster arbiter",
			cnf: mongo.RSConfig{Members: mongo.ConfigMembers{
				managed(0, "rs0-0:27017", "rs0-0"),
				managed(1, "rs0-1:27017", "rs0-1"),
				{ID: 2, Host: "rs0-arbiter-0:27017", ArbiterOnly: true},
			}},
			rsStatus: mongo.Status{Members: []*mongo.Member{
				{Id: 0, Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				{Id: 1, Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
				{Id: 2, Name: "rs0-arbiter-0:27017", State: mongo.MemberStateArbiter, StateStr: "ARBITER"},
			}},
			rs: &api.ReplsetSpec{
				ExternalNodes: []*api.ExternalNode{
					{Host: "data.example.com", Port: 27017, ArbiterOnly: false},
				},
			},
			expectedLive: 3,
			expectedRSMembers: map[string]api.ReplsetMemberStatus{
				"rs0-0": {Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				"rs0-1": {Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
			},
		},
		{
			name: "arbiter external node with port in host",
			cnf: mongo.RSConfig{Members: mongo.ConfigMembers{
				managed(0, "rs0-0:27017", "rs0-0"),
				managed(1, "rs0-1:27017", "rs0-1"),
				{ID: 2, Host: "arbiter.example.com:27017", ArbiterOnly: true},
			}},
			rsStatus: mongo.Status{Members: []*mongo.Member{
				{Id: 0, Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				{Id: 1, Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
				{Id: 2, Name: "arbiter.example.com:27017", State: mongo.MemberStateArbiter, StateStr: "ARBITER"},
			}},
			rs: &api.ReplsetSpec{
				ExternalNodes: []*api.ExternalNode{
					{Host: "arbiter.example.com:27017", Port: 27017, ArbiterOnly: true},
				},
			},
			expectedLive: 2,
			expectedRSMembers: map[string]api.ReplsetMemberStatus{
				"rs0-0": {Name: "rs0-0:27017", State: mongo.MemberStatePrimary, StateStr: "PRIMARY"},
				"rs0-1": {Name: "rs0-1:27017", State: mongo.MemberStateSecondary, StateStr: "SECONDARY"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rsMembers := make(map[string]api.ReplsetMemberStatus)
			live := countLiveMembers(tt.rsStatus, tt.cnf, tt.rs, rsMembers)
			assert.Equal(t, tt.expectedLive, live)
			assert.Equal(t, tt.expectedRSMembers, rsMembers)
		})
	}
}
