package mongo_test

import (
	"testing"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func TestVoting(t *testing.T) {
	cases := []struct {
		name     string
		mset     *mongo.ConfigMembers
		desiered *mongo.ConfigMembers
	}{
		{
			"nothing",
			&mongo.ConfigMembers{},
			&mongo.ConfigMembers{},
		},
		{
			"3 mongos",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:     "host0",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host1",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host2",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
		},
		{
			"2 mongos",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:     "host0",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host1",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
			},
		},
		{
			"2 mongos + 1 arbiter",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:     "host0",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host1",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:        "host2",
					Votes:       mongo.DefaultVotes,
					Priority:    0,
					ArbiterOnly: true,
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
			},
		},
		{
			"2 mongos + 1 first arbiter",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:        "host0",
					Votes:       mongo.DefaultVotes,
					Priority:    0,
					ArbiterOnly: true,
				},
				mongo.ConfigMember{
					Host:     "host1",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host2",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
		},
		{
			"9 mongos",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:     "host0",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host1",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host2",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host3",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host4",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host5",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host6",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host7",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host8",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
			},
		},
		{
			"8 mongos",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:     "host0",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host1",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host2",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host3",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host4",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host5",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host6",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host7",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
			},
		},
		{
			"8 mongos + 1 arbiter",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:     "host0",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host1",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host2",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host3",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host4",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host5",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host6",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host7",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:        "host8",
					Votes:       mongo.DefaultVotes,
					Priority:    0,
					ArbiterOnly: true,
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
			},
		},
		{
			"3 mongos + 1 arbiter",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:     "host0",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host1",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host2",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:        "host3",
					Votes:       mongo.DefaultVotes,
					Priority:    0,
					ArbiterOnly: true,
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
			},
		},
	}

	for _, c := range cases {
		c.mset.SetVotes(*c.mset, false)
		if len(*c.mset) != len(*c.desiered) {
			t.Errorf("%s: missmatched members size", c.name)
			continue
		}

		for i, member := range *c.mset {
			d := []mongo.ConfigMember(*c.desiered)
			if member.Votes != d[i].Votes || member.Priority != d[i].Priority {
				t.Errorf("%s: member (%s) want %v, have %v", c.name, member.Host, d[i], member)
			}
		}
	}
}
