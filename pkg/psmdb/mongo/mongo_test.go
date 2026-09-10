package mongo_test

import (
	"context"
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

// dbm builds a data-bearing member as the operator writes one: BuildIndexes is
// always true and the podName identity tag is always present.
func dbm(id int, host string, votes, priority int) mongo.ConfigMember {
	return mongo.ConfigMember{
		ID:           id,
		Host:         host,
		Votes:        votes,
		Priority:     priority,
		BuildIndexes: true,
		Tags:         mongo.ReplsetTags{"podName": host},
	}
}

func TestApplyMemberConfig(t *testing.T) {
	// live/desired are the inputs; want is the expected state of live after the
	// call, so every case also asserts that nothing else was touched.
	// wantChanged/wantPending are the return values of the LAST call when
	// calls > 1.
	cases := []struct {
		name        string
		live        mongo.ConfigMembers
		desired     mongo.ConfigMembers
		calls       int // defaults to 1
		want        mongo.ConfigMembers
		wantChanged bool
		wantPending bool
		wantErr     string
	}{
		{
			name:    "no changes",
			live:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
			desired: mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
			want:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
		},
		{
			name:        "priority changed",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 10), dbm(1, "h1", 1, 2)},
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 10), dbm(1, "h1", 1, 2)},
			wantChanged: true,
		},
		{
			name: "hidden changed",
			live: mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			desired: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "h0", 1, 0)
				m.Hidden = true
				return m
			}()},
			want: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "h0", 1, 0)
				m.Hidden = true
				return m
			}()},
			wantChanged: true,
		},
		{
			name: "tag added",
			live: mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			desired: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "h0", 1, 2)
				m.Tags = mongo.ReplsetTags{"podName": "h0", "workload": "analytics"}
				return m
			}()},
			want: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "h0", 1, 2)
				m.Tags = mongo.ReplsetTags{"podName": "h0", "workload": "analytics"}
				return m
			}()},
			wantChanged: true,
		},
		{
			// A removed key must disappear
			name: "tag removed",
			live: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "h0", 1, 2)
				m.Tags = mongo.ReplsetTags{"podName": "h0", "workload": "analytics"}
				return m
			}()},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			wantChanged: true,
		},
		{
			name: "horizons removed",
			live: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "h0", 1, 2)
				m.Horizons = map[string]string{"ext": "example.com:27017"}
				return m
			}()},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			wantChanged: true,
		},
		{
			name:        "single vote change applies, nothing pending",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			wantChanged: true,
		},
		{
			// MongoDB permits only one voting-member change per ordinary
			// reconfiguration, so the second one waits for the next pass.
			name:        "two vote changes: only one applied, rest pending",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 0, 0), dbm(2, "h2", 0, 0)},
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 0, 0), dbm(2, "h2", 1, 0)},
			wantChanged: true,
			wantPending: true,
		},
		{
			name:        "second pass converges the remaining vote",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 0, 0), dbm(2, "h2", 0, 0)},
			calls:       2,
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 0, 0), dbm(2, "h2", 0, 0)},
			wantChanged: true,
		},
		{
			// No parity or cap rule may re-add or strip a vote once converged:
			// this is the whole point of not calling SetVotes here.
			name:        "explicit votes survive repeated reconciliation",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			calls:       5,
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			wantChanged: false, // converged on the first call, no-op thereafter
		},
		{
			name: "arbiterOnly change is rejected",
			live: mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			desired: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "h0", 1, 0)
				m.ArbiterOnly = true
				m.Tags = nil
				return m
			}()},
			want:    mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			wantErr: "arbiterOnly cannot be changed",
		},
		{
			// ExternalNodesChanged owns these; ApplyMemberConfig must not touch
			// them even when the desired list disagrees.
			name: "external member is skipped",
			live: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "ext", 1, 1)
				m.Tags = mongo.ReplsetTags{"external": "true"}
				return m
			}()},
			desired: mongo.ConfigMembers{dbm(0, "ext", 0, 0)},
			want: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "ext", 1, 1)
				m.Tags = mongo.ReplsetTags{"external": "true"}
				return m
			}()},
		},
		{
			// RemoveOld owns members that are gone from the desired list.
			name:    "host absent from desired is left alone",
			live:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "gone", 1, 2)},
			desired: mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			want:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "gone", 1, 2)},
		},
		{
			// AddNew and RemoveOld own ID assignment; matching is by host.
			name:        "member IDs are never modified",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
			desired:     mongo.ConfigMembers{dbm(100, "h0", 1, 5), dbm(101, "h1", 1, 2)},
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 5), dbm(1, "h1", 1, 2)},
			wantChanged: true,
		},
		{
			// Neither field is exposed on the CRD, so a value somebody set by
			// hand must be neither reverted nor treated as an error.
			name: "buildIndexes and delay set out of band are left as found",
			live: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "h0", 1, 2)
				m.BuildIndexes = false
				m.SecondaryDelaySecs = new(int64(3600))
				return m
			}()},
			desired: mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			want: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "h0", 1, 2)
				m.BuildIndexes = false
				m.SecondaryDelaySecs = new(int64(3600))
				return m
			}()},
		},
		{
			// MongoDB arbiters carry no tags, so the tag comparison is skipped
			// for them entirely.
			name: "arbiter tags are not compared",
			live: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "arb", 1, 0)
				m.ArbiterOnly = true
				m.Tags = nil
				return m
			}()},
			desired: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "arb", 1, 0)
				m.ArbiterOnly = true
				return m
			}()},
			want: mongo.ConfigMembers{func() mongo.ConfigMember {
				m := dbm(0, "arb", 1, 0)
				m.ArbiterOnly = true
				m.Tags = nil
				return m
			}()},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			live := c.live

			calls := c.calls
			if calls == 0 {
				calls = 1
			}

			var (
				changed bool
				pending bool
				err     error
			)
			for range calls {
				changed, pending, err = live.ApplyMemberConfig(ctx, c.desired)
				if err != nil {
					break
				}
			}

			if c.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), c.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, c.wantChanged, changed, "changed")
				assert.Equal(t, c.wantPending, pending, "votingChangePending")
			}

			assert.Equal(t, c.want, live, "live config after the call")
		})
	}
}
