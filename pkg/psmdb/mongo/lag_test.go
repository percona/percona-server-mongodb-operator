package mongo_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func TestElectableSecondaryLag(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	electable := func(host string) mongo.ConfigMember {
		return mongo.ConfigMember{Host: host, Priority: 1, Votes: 1}
	}
	member := func(name string, state mongo.MemberState, health mongo.MemberHealth, behind time.Duration) *mongo.Member {
		return &mongo.Member{Name: name, State: state, Health: health, OptimeDate: now.Add(-behind)}
	}

	cases := []struct {
		name       string
		cfg        mongo.ConfigMembers
		members    []*mongo.Member
		wantMember string
		wantLag    time.Duration
		wantErr    string
	}{
		{
			name: "least lagging secondary",
			cfg:  mongo.ConfigMembers{electable("rs0-0"), electable("rs0-1"), electable("rs0-2")},
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary, mongo.MemberHealthUp, 0),
				member("rs0-1", mongo.MemberStateSecondary, mongo.MemberHealthUp, 30*time.Second),
				member("rs0-2", mongo.MemberStateSecondary, mongo.MemberHealthUp, 5*time.Second),
			},
			wantMember: "rs0-2",
			wantLag:    5 * time.Second,
		},
		{
			name: "secondary ahead of primary optime",
			cfg:  mongo.ConfigMembers{electable("rs0-0"), electable("rs0-1")},
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary, mongo.MemberHealthUp, time.Second),
				member("rs0-1", mongo.MemberStateSecondary, mongo.MemberHealthUp, 0),
			},
			wantMember: "rs0-1",
			wantLag:    0,
		},
		{
			name: "not electable and unhealthy members are skipped",
			cfg: mongo.ConfigMembers{
				electable("rs0-0"),
				electable("rs0-1"),
				electable("rs0-2"),
				{Host: "hidden-0", Hidden: true, Votes: 1},
				{Host: "nv-0", Priority: 0, Votes: 0},
				{Host: "arbiter-0", ArbiterOnly: true, Votes: 1},
			},
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary, mongo.MemberHealthUp, 0),
				member("rs0-1", mongo.MemberStateSecondary, mongo.MemberHealthDown, 0),
				member("rs0-2", mongo.MemberStateSecondary, mongo.MemberHealthUp, time.Minute),
				member("hidden-0", mongo.MemberStateSecondary, mongo.MemberHealthUp, 0),
				member("nv-0", mongo.MemberStateSecondary, mongo.MemberHealthUp, 0),
				member("arbiter-0", mongo.MemberStateArbiter, mongo.MemberHealthUp, 0),
			},
			wantMember: "rs0-2",
			wantLag:    time.Minute,
		},
		{
			name: "recovering member is skipped",
			cfg:  mongo.ConfigMembers{electable("rs0-0"), electable("rs0-1"), electable("rs0-2")},
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary, mongo.MemberHealthUp, 0),
				member("rs0-1", mongo.MemberStateRecovering, mongo.MemberHealthUp, 0),
				member("rs0-2", mongo.MemberStateSecondary, mongo.MemberHealthUp, 10*time.Second),
			},
			wantMember: "rs0-2",
			wantLag:    10 * time.Second,
		},
		{
			name: "no primary",
			cfg:  mongo.ConfigMembers{electable("rs0-0"), electable("rs0-1")},
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStateSecondary, mongo.MemberHealthUp, 0),
				member("rs0-1", mongo.MemberStateSecondary, mongo.MemberHealthUp, 0),
			},
			wantErr: "primary not found in replset status",
		},
		{
			name: "no electable secondary",
			cfg: mongo.ConfigMembers{
				electable("rs0-0"),
				{Host: "hidden-0", Hidden: true, Votes: 1},
			},
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary, mongo.MemberHealthUp, 0),
				member("hidden-0", mongo.MemberStateSecondary, mongo.MemberHealthUp, 0),
			},
			wantErr: "no electable secondary found",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotMember, gotLag, err := mongo.ElectableSecondaryLag(
				mongo.RSConfig{Members: tt.cfg},
				mongo.Status{Members: tt.members},
			)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMember, gotMember)
			assert.Equal(t, tt.wantLag, gotLag)
		})
	}
}

func TestElectableOptimes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cfg := mongo.RSConfig{Members: mongo.ConfigMembers{
		{Host: "rs0-0", Priority: 1, Votes: 1},
		{Host: "rs0-1", Priority: 1, Votes: 1},
		{Host: "rs0-2", Priority: 1, Votes: 1},
		{Host: "hidden-0", Hidden: true, Votes: 1},
	}}
	status := mongo.Status{Members: []*mongo.Member{
		{Name: "rs0-0", State: mongo.MemberStatePrimary, Health: mongo.MemberHealthUp, OptimeDate: now},
		{Name: "rs0-1", State: mongo.MemberStateSecondary, Health: mongo.MemberHealthUp, OptimeDate: now.Add(-time.Second)},
		// unreachable member keeps the last optime the primary saw, it must still be tracked
		{Name: "rs0-2", State: mongo.MemberStateSecondary, Health: mongo.MemberHealthDown, OptimeDate: now.Add(-time.Minute)},
		{Name: "hidden-0", State: mongo.MemberStateSecondary, Health: mongo.MemberHealthUp, OptimeDate: now},
	}}

	assert.Equal(t, map[string]time.Time{
		"rs0-1": now.Add(-time.Second),
		"rs0-2": now.Add(-time.Minute),
	}, mongo.ElectableOptimes(cfg, status))
}

func TestSecondaryProgressed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		prev map[string]time.Time
		cur  map[string]time.Time
		want bool
	}{
		{
			name: "first sample",
			prev: nil,
			cur:  map[string]time.Time{"rs0-1": now},
			want: false,
		},
		{
			name: "no member moved",
			prev: map[string]time.Time{"rs0-1": now, "rs0-2": now},
			cur:  map[string]time.Time{"rs0-1": now, "rs0-2": now},
			want: false,
		},
		{
			name: "one member moved",
			prev: map[string]time.Time{"rs0-1": now, "rs0-2": now},
			cur:  map[string]time.Time{"rs0-1": now, "rs0-2": now.Add(time.Second)},
			want: true,
		},
		{
			name: "member went back",
			prev: map[string]time.Time{"rs0-1": now},
			cur:  map[string]time.Time{"rs0-1": now.Add(-time.Second)},
			want: false,
		},
		{
			name: "new member is not progress",
			prev: map[string]time.Time{"rs0-1": now},
			cur:  map[string]time.Time{"rs0-1": now, "rs0-2": now.Add(time.Second)},
			want: false,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, mongo.SecondaryProgressed(tt.prev, tt.cur))
		})
	}
}
