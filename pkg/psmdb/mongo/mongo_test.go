package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func TestVoting(t *testing.T) {
	cases := []struct {
		name      string
		mset      *mongo.ConfigMembers
		desired   *mongo.ConfigMembers
		unsafePSA bool
	}{
		{
			"nothing",
			&mongo.ConfigMembers{},
			&mongo.ConfigMembers{},
			false,
		},
		{
			"1 member: 1 rs0",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:     "host0",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
			false,
		},
		{
			"2 members: 2 rs0",
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
			false,
		},
		{
			"3 members: 3 rs0",
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
			false,
		},
		{
			"3 members: 2 rs0 + 1 arbiter",
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
			false,
		},
		{
			"3 members: 1 arbiter + 2 rs0",
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
			false,
		},
		{
			"3 members (unsafe PSA): 2 rs0 + 1 arbiter",
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
			true,
		},
		{
			"2 members (unsafe PSA start): 2 rs0 without arbiter",
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
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
			},
			true,
		},
		{
			"4 members: 4 rs0",
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
					Votes:    0,
					Priority: 0,
				},
			},
			false,
		},
		{
			"4 members: 3 rs0 + 1 arbiter",
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
			false,
		},
		{
			"4 members: 1 rs0 + 3 hidden",
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Host:     "host0",
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Host:     "host1",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host2",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host3",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
			},
			&mongo.ConfigMembers{
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: mongo.DefaultPriority,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
			},
			false,
		},
		{
			"5 members",
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
			},
			false,
		},
		{
			"5 members: 3 rs0 + 2 non-voting",
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
					Votes:    0,
					Priority: 0,
					Tags: mongo.ReplsetTags{
						naming.ComponentNonVoting: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host4",
					Votes:    0,
					Priority: 0,
					Tags: mongo.ReplsetTags{
						naming.ComponentNonVoting: "true",
					},
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
					Votes:    0,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
			},
			false,
		},
		{
			"6 members: 3 rs0 + 3 hidden",
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
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host4",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host5",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
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
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
			},
			false,
		},
		{
			"6 members: 3 rs0 + 1 non-voting + 1 hidden + 1 arbiter",
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
					Votes:    0,
					Priority: 0,
					Tags: mongo.ReplsetTags{
						naming.ComponentNonVoting: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host4",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:        "host5",
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
					Votes:    0,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
			},
			false,
		},
		{
			"6 members: 3 rs0 + 3 external (all voters)",
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
					Priority: 0,
					Tags:     mongo.ReplsetTags{"external": "true"},
				},
				mongo.ConfigMember{
					Host:     "host4",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Tags:     mongo.ReplsetTags{"external": "true"},
				},
				mongo.ConfigMember{
					Host:     "host5",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Tags:     mongo.ReplsetTags{"external": "true"},
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
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
			},
			false,
		},
		{
			"6 members: 3 rs0 + 3 external (no voters)",
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
					Votes:    0,
					Priority: 0,
					Tags:     mongo.ReplsetTags{"external": "true"},
				},
				mongo.ConfigMember{
					Host:     "host4",
					Votes:    0,
					Priority: 0,
					Tags:     mongo.ReplsetTags{"external": "true"},
				},
				mongo.ConfigMember{
					Host:     "host5",
					Votes:    0,
					Priority: 0,
					Tags:     mongo.ReplsetTags{"external": "true"},
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
					Votes:    0,
					Priority: 0,
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
			false,
		},
		{
			"7 members",
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
			},
			false,
		},
		{
			"8 members",
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
			false,
		},
		{
			"8 members: 5 rs0 + 3 hidden",
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
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host6",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host7",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
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
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
			},
			false,
		},
		{
			"9 members",
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
			false,
		},
		{
			"9 members: 8 rs0 + 1 arbiter",
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
					Votes:    0,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:       mongo.DefaultVotes,
					Priority:    0,
					ArbiterOnly: true,
				},
			},
			false,
		},
		{
			"10 members: 5 rs0 + 3 hidden + 2 non-voting",
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
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host6",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host7",
					Votes:    mongo.DefaultVotes,
					Priority: 0,
					Hidden:   true,
					Tags: mongo.ReplsetTags{
						naming.ComponentHidden: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host8",
					Votes:    0,
					Priority: 0,
					Tags: mongo.ReplsetTags{
						naming.ComponentNonVoting: "true",
					},
				},
				mongo.ConfigMember{
					Host:     "host9",
					Votes:    0,
					Priority: 0,
					Tags: mongo.ReplsetTags{
						naming.ComponentNonVoting: "true",
					},
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
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    mongo.DefaultVotes,
					Priority: 0,
				},
				mongo.ConfigMember{
					Votes:    0,
					Priority: 0,
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
			false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.mset.SetVotes(*c.mset, c.unsafePSA)
			require.Len(t, *c.mset, len(*c.desired))

			votes := 0
			for i, member := range *c.mset {
				d := []mongo.ConfigMember(*c.desired)

				votes += member.Votes

				assert.Equalf(t, d[i].Votes, member.Votes, "member (%s) votes are wrong", member.Host)
				assert.Equalf(t, d[i].Priority, member.Priority, "member (%s) priority is wrong", member.Host)
			}

			assert.Falsef(t, votes > mongo.MaxVotingMembers, "there should be max (%d) votes in replset", mongo.MaxVotingMembers)
			if votes != 0 && !c.unsafePSA {
				assert.Falsef(t, votes%2 == 0, "total votes (%d) should be an odd number", votes)
			}
		})
	}
}
