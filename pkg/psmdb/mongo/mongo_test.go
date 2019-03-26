package mongo_test

import (
	"testing"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func TestVoting(t *testing.T) {
	cases := []struct {
		name     string
		mset     *mongo.RSMembers
		desiered *mongo.RSMembers
	}{
		{
			"nothing",
			&mongo.RSMembers{},
			&mongo.RSMembers{},
		},
		{
			"3 mongos",
			&mongo.RSMembers{
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
			},
			&mongo.RSMembers{
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
			},
		},
		{
			"2 mongos",
			&mongo.RSMembers{
				mongo.Member{},
				mongo.Member{},
			},
			&mongo.RSMembers{
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    0,
					Priority: 0,
				},
			},
		},
		{
			"2 mongos + 1 arbiter",
			&mongo.RSMembers{
				mongo.Member{},
				mongo.Member{},
				mongo.Member{ArbiterOnly: true},
			},
			&mongo.RSMembers{
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 0,
				},
			},
		},
		{
			"2 mongos + 1 first arbiter",
			&mongo.RSMembers{
				mongo.Member{ArbiterOnly: true},
				mongo.Member{},
				mongo.Member{},
			},
			&mongo.RSMembers{
				mongo.Member{
					Votes:    1,
					Priority: 0,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
			},
		},
		{
			"9 mongos",
			&mongo.RSMembers{
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
			},
			&mongo.RSMembers{
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    0,
					Priority: 0,
				},
				mongo.Member{
					Votes:    0,
					Priority: 0,
				},
			},
		},
		{
			"8 mongos",
			&mongo.RSMembers{
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
			},
			&mongo.RSMembers{
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    0,
					Priority: 0,
				},
			},
		},
		{
			"8 mongos + 1 arbiter",
			&mongo.RSMembers{
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{ArbiterOnly: true},
			},
			&mongo.RSMembers{
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    0,
					Priority: 0,
				},
				mongo.Member{
					Votes:    0,
					Priority: 0,
				},
				mongo.Member{
					Votes:    1,
					Priority: 0,
				},
			},
		},
		{
			"3 mongos + 1 arbiter",
			&mongo.RSMembers{
				mongo.Member{},
				mongo.Member{},
				mongo.Member{},
				mongo.Member{ArbiterOnly: true},
			},
			&mongo.RSMembers{
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    1,
					Priority: 1,
				},
				mongo.Member{
					Votes:    0,
					Priority: 0,
				},
				mongo.Member{
					Votes:    1,
					Priority: 0,
				},
			},
		},
	}

	for _, c := range cases {
		c.mset.SetVotes()
		if len(*c.mset) != len(*c.desiered) {
			t.Errorf("%s: missmatched members size", c.name)
			continue
		}

		for i, member := range *c.mset {
			d := []mongo.Member(*c.desiered)
			if member.Votes != d[i].Votes || member.Priority != d[i].Priority {
				t.Errorf("%s: member %d want %v, have %v", c.name, i, d[i], member)
			}
		}
	}
}
