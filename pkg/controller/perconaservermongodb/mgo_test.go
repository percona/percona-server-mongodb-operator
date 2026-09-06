package perconaservermongodb

import (
	"context"
	"net"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

// fakeResolver is a test double for dnsResolver.
type fakeResolver struct {
	ips     []net.IPAddr
	err     error
	gotHost string
}

func (f *fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	f.gotHost = host
	return f.ips, f.err
}

func TestHostResolvable(t *testing.T) {
	t.Parallel()

	const host = "my-cluster-rs0-0.psmdb.svc.clusterset.local"

	tests := map[string]struct {
		resolver    *fakeResolver
		expectedOK  bool
		expectedErr bool
	}{
		"resolves to an IP": {
			resolver:    &fakeResolver{ips: []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}},
			expectedOK:  true,
			expectedErr: false,
		},
		"resolves to multiple IPs": {
			resolver: &fakeResolver{ips: []net.IPAddr{
				{IP: net.ParseIP("10.0.0.1")},
				{IP: net.ParseIP("10.0.0.2")},
			}},
			expectedOK:  true,
			expectedErr: false,
		},
		"lookup error": {
			resolver:    &fakeResolver{err: errors.New("no such host")},
			expectedOK:  false,
			expectedErr: true,
		},
		"no addresses without error": {
			resolver:    &fakeResolver{ips: nil},
			expectedOK:  false,
			expectedErr: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ok, err := hostResolvable(context.Background(), tt.resolver, host)

			if tt.expectedErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expectedOK, ok)
			// the bare hostname must be passed to the resolver, never host:port
			assert.Equal(t, host, tt.resolver.gotHost)
		})
	}
}

func TestReconcilerResolverDefault(t *testing.T) {
	t.Parallel()

	// no resolver configured -> falls back to the system default resolver
	r := &ReconcilePerconaServerMongoDB{}
	assert.Same(t, net.DefaultResolver, r.resolver())

	// configured resolver is returned as-is
	fake := &fakeResolver{}
	r.dnsResolver = fake
	assert.Same(t, fake, r.resolver())
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
