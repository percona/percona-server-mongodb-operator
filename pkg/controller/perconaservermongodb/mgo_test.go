package perconaservermongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
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

// rwInstance builds an instance group for the write-concern table.
func rwInstance(name string, replicas int32, cfg *api.MemberConfigSpec) api.InstanceSpec {
	return api.InstanceSpec{Name: name, Replicas: replicas, RSConfig: cfg}
}

func TestShouldSetDefaultRWConcern(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		shardingEnabled bool
		arbiterEnabled  bool
		// instances replaces the legacy topology when set
		instances     []api.InstanceSpec
		externalNodes []*api.ExternalNode
		rwConcern     *api.DefaultRWConcern
		want          bool
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
		"instances with an arbiter group": {
			instances: []api.InstanceSpec{
				rwInstance("mongod", 2, nil),
				rwInstance("arb", 1, &api.MemberConfigSpec{
					ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))}),
			},
			want: true,
		},
		"instances without an arbiter group": {
			instances: []api.InstanceSpec{rwInstance("hot", 3, nil)},
			want:      false,
		},
		"instances with a custom concern": {
			instances: []api.InstanceSpec{rwInstance("hot", 3, nil)},
			rwConcern: &api.DefaultRWConcern{WriteConcern: &api.DefaultWriteConcernSpec{W: "1"}},
			want:      true,
		},
		"sharded instances with an arbiter group": {
			shardingEnabled: true,
			instances: []api.InstanceSpec{
				rwInstance("mongod", 2, nil),
				rwInstance("arb", 1, &api.MemberConfigSpec{
					ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))}),
			},
			want: false,
		},
		"a nil external node is skipped": {
			externalNodes: []*api.ExternalNode{nil, {ArbiterOnly: true}},
			want:          true,
		},
		"only a nil external node": {
			externalNodes: []*api.ExternalNode{nil},
			want:          false,
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
			arbiter := api.Arbiter{Enabled: tc.arbiterEnabled}
			if tc.arbiterEnabled {
				arbiter.Size = 1
			}
			rs := &api.ReplsetSpec{
				Arbiter:       arbiter,
				ExternalNodes: tc.externalNodes,
			}
			if tc.instances != nil {
				rs = &api.ReplsetSpec{
					Instances:     tc.instances,
					ExternalNodes: tc.externalNodes,
				}
			}

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			assert.Equal(t, tc.want, shouldSetDefaultRWConcern(cr, set, rs))
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

func TestIsUnsafePSA(t *testing.T) {
	arbiter := func(name string, replicas int32) api.InstanceSpec {
		return api.InstanceSpec{Name: name, Replicas: replicas, RSConfig: &api.MemberConfigSpec{
			ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))}}
	}
	data := func(name string, replicas int32) api.InstanceSpec {
		return api.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: memberVol(),
			RSConfig: &api.MemberConfigSpec{Votes: new(int32(1)), Priority: new(int32(2))}}
	}
	nonVoting := func(name string, replicas int32) api.InstanceSpec {
		return api.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: memberVol(),
			RSConfig: &api.MemberConfigSpec{Votes: new(int32(0)), Priority: new(int32(0))}}
	}

	for _, tt := range []struct {
		name      string
		legacy    func(*api.PerconaServerMongoDB)
		instances []api.InstanceSpec
		unsafe    bool
		want      bool
	}{
		{
			name: "legacy PSA",
			legacy: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Size = 2
				cr.Spec.Replsets[0].Arbiter = api.Arbiter{Enabled: true, Size: 1}
			},
			unsafe: true, want: true,
		},
		{
			name: "the same topology without the unsafe flag",
			legacy: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Size = 2
				cr.Spec.Replsets[0].Arbiter = api.Arbiter{Enabled: true, Size: 1}
			},
			unsafe: false, want: false,
		},
		{
			name: "three data members and an arbiter is not a PSA",
			legacy: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Size = 3
				cr.Spec.Replsets[0].Arbiter = api.Arbiter{Enabled: true, Size: 1}
			},
			unsafe: true, want: false,
		},
		{
			name: "a non-voting member takes it out of PSA",
			legacy: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Size = 2
				cr.Spec.Replsets[0].Arbiter = api.Arbiter{Enabled: true, Size: 1}
				cr.Spec.Replsets[0].NonVoting = api.NonVotingSpec{Enabled: true, Size: 1}
			},
			unsafe: true, want: false,
		},
		{
			name: "hidden members take it out of PSA",
			legacy: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Size = 2
				cr.Spec.Replsets[0].Arbiter = api.Arbiter{Enabled: true, Size: 1}
				cr.Spec.Replsets[0].Hidden = api.HiddenSpec{Enabled: true, Size: 2}
			},
			unsafe: true, want: false,
		},
		{
			name:      "instances under the reserved names",
			instances: []api.InstanceSpec{data("mongod", 2), arbiter("arbiter", 1)},
			unsafe:    true, want: true,
		},
		{
			name:      "instances under custom names",
			instances: []api.InstanceSpec{data("hot", 2), arbiter("arb", 1)},
			unsafe:    true, want: true,
		},
		{
			name:      "two data voters split across groups",
			instances: []api.InstanceSpec{data("hot", 1), data("cold", 1), arbiter("arb", 1)},
			unsafe:    true, want: true,
		},
		{
			name:      "three data voters across groups is not a PSA",
			instances: []api.InstanceSpec{data("hot", 2), data("cold", 1), arbiter("arb", 1)},
			unsafe:    true, want: false,
		},
		{
			name:      "two arbiters is not a PSA",
			instances: []api.InstanceSpec{data("hot", 2), arbiter("arb", 2)},
			unsafe:    true, want: false,
		},
		{
			name:      "no arbiter is not a PSA",
			instances: []api.InstanceSpec{data("hot", 2)},
			unsafe:    true, want: false,
		},
		{
			name:      "a non-voting group takes instances out of PSA",
			instances: []api.InstanceSpec{data("hot", 2), arbiter("arb", 1), nonVoting("ro", 1)},
			unsafe:    true, want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var cr *api.PerconaServerMongoDB
			if tt.instances != nil {
				cr = instanceCR(t, "psa-cr", "psa", tt.instances, unsafeSize)
			} else {
				cr = legacyCR(t, "psa-cr", "psa", func(c *api.PerconaServerMongoDB) {
					c.Spec.Unsafe.ReplsetSize = true
					tt.legacy(c)
				})
			}
			cr.Spec.Unsafe.ReplsetSize = tt.unsafe

			set, err := membergroup.Resolve(cr, cr.Spec.Replsets[0])
			require.NoError(t, err)

			assert.Equal(t, tt.want, isUnsafePSA(cr, set))
		})
	}
}

func memberVol() *api.VolumeSpec {
	return &api.VolumeSpec{PersistentVolumeClaim: api.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		}}}
}

func dataGroup(name string, replicas int32, priority int32) api.InstanceSpec {
	return api.InstanceSpec{
		Name: name, Replicas: replicas, VolumeSpec: memberVol(),
		RSConfig: &api.MemberConfigSpec{Votes: new(int32(1)), Priority: new(priority)},
	}
}

func TestBootstrapPod(t *testing.T) {
	type podSpec struct {
		group   string
		ordinal int
		ready   bool
	}

	for _, tt := range []struct {
		name      string
		instances []api.InstanceSpec
		pods      []podSpec
		wantPod   string
		wantGroup string
		wantErr   string
	}{
		{
			name:      "the lowest ready ordinal of the only group",
			instances: []api.InstanceSpec{dataGroup("mongod", 3, 2)},
			pods: []podSpec{
				{"mongod", 0, true}, {"mongod", 1, true}, {"mongod", 2, true},
			},
			wantPod: "boot-cr-rs0-0", wantGroup: "mongod",
		},
		{
			name:      "a pod that is not ready is skipped",
			instances: []api.InstanceSpec{dataGroup("mongod", 3, 2)},
			pods: []podSpec{
				{"mongod", 0, false}, {"mongod", 1, true}, {"mongod", 2, true},
			},
			wantPod: "boot-cr-rs0-1", wantGroup: "mongod",
		},
		{
			name:      "a pod beyond the declared replica count is skipped",
			instances: []api.InstanceSpec{dataGroup("mongod", 2, 2)},
			pods: []podSpec{
				{"mongod", 0, false}, {"mongod", 1, false}, {"mongod", 2, true},
			},
			wantErr: "no ready primary-eligible member pod",
		},
		{
			name: "the highest-priority group goes first",
			instances: []api.InstanceSpec{
				dataGroup("warm", 1, 5), dataGroup("hot", 1, 10), dataGroup("cold", 1, 2),
			},
			pods: []podSpec{
				{"cold", 0, true}, {"hot", 0, true}, {"warm", 0, true},
			},
			wantPod: "boot-cr-rs0-hot-0", wantGroup: "hot",
		},
		{
			name: "groups of equal priority break the tie by name",
			instances: []api.InstanceSpec{
				dataGroup("b", 1, 5), dataGroup("a", 1, 5), dataGroup("c", 1, 5),
			},
			pods: []podSpec{
				{"a", 0, true}, {"b", 0, true}, {"c", 0, true},
			},
			wantPod: "boot-cr-rs0-a-0", wantGroup: "a",
		},
		{
			name: "never an arbiter",
			instances: []api.InstanceSpec{
				dataGroup("data", 3, 2),
				{Name: "arb", Replicas: 1, RSConfig: &api.MemberConfigSpec{
					ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))}},
			},
			pods:    []podSpec{{"arb", 0, true}},
			wantErr: "no ready primary-eligible member pod",
		},
		{
			name: "never a hidden member",
			instances: []api.InstanceSpec{
				dataGroup("data", 3, 2),
				{Name: "hid", Replicas: 1, VolumeSpec: memberVol(), RSConfig: &api.MemberConfigSpec{
					Hidden: new(true), Votes: new(int32(1)), Priority: new(int32(0))}},
			},
			pods:    []podSpec{{"hid", 0, true}},
			wantErr: "no ready primary-eligible member pod",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			cr := instanceCR(t, "boot-cr", "boot", tt.instances, unsafeSize)
			rs := cr.Spec.Replsets[0]

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			objs := []client.Object{cr}
			for _, p := range tt.pods {
				pod := groupPod(cr, rs, resolveGroup(t, cr, rs, p.group), p.ordinal)
				if !p.ready {
					pod = notReady(pod)
				}
				objs = append(objs, pod)
			}
			r := buildFakeClient(objs...)

			pod, group, err := r.bootstrapPod(ctx, cr, rs, set)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPod, pod.Name)
			assert.Equal(t, tt.wantGroup, group.Name)
		})
	}
}

func TestBootstrapPodChecksTheGroupsContainer(t *testing.T) {
	ctx := t.Context()

	cr := instanceCR(t, "boot-cr", "boot", []api.InstanceSpec{dataGroup("mongod", 1, 2)}, unsafeSize)
	rs := cr.Spec.Replsets[0]

	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	group := resolveGroup(t, cr, rs, "mongod")
	pod := groupPod(cr, rs, group, 0)

	// The pod is ready, but the running container is not the group's.
	pod.Spec.Containers[0].Name = "something-else"
	pod.Status.ContainerStatuses[0].Name = "something-else"

	r := buildFakeClient(cr, pod)

	_, _, err = r.bootstrapPod(ctx, cr, rs, set)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no ready primary-eligible member pod")
}
