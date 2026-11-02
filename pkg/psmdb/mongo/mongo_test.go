package mongo_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func TestVoting(t *testing.T) {
	t.Parallel()

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
			compareWith := slices.Clone(*c.mset)
			c.mset.SetVotes(compareWith, c.unsafePSA, "")
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

			again := slices.Clone(*c.mset)
			again.SetVotes(compareWith, c.unsafePSA, "")
			assert.Equal(t, *c.mset, again, "SetVotes is not idempotent")
		})
	}
}

func TestSetVotesTransitions(t *testing.T) {
	member := func(host string, votes, priority int) mongo.ConfigMember {
		return mongo.ConfigMember{Host: host, Votes: votes, Priority: priority}
	}
	hidden := func(host string, votes int) mongo.ConfigMember {
		return mongo.ConfigMember{Host: host, Votes: votes, Hidden: true, Tags: mongo.ReplsetTags{naming.ComponentHidden: "true"}}
	}
	nonVoting := func(host string, votes, priority int) mongo.ConfigMember {
		return mongo.ConfigMember{Host: host, Votes: votes, Priority: priority, Tags: mongo.ReplsetTags{naming.ComponentNonVoting: "true"}}
	}
	external := func(host string, votes, priority int) mongo.ConfigMember {
		return mongo.ConfigMember{Host: host, Votes: votes, Priority: priority, Tags: mongo.ReplsetTags{"external": "true"}}
	}
	arbiter := func(host string) mongo.ConfigMember {
		return mongo.ConfigMember{Host: host, Votes: mongo.DefaultVotes, ArbiterOnly: true}
	}

	rs := func(n int) mongo.ConfigMembers {
		m := mongo.ConfigMembers{}
		for i := range n {
			m = append(m, member(fmt.Sprintf("rs0-%d", i), mongo.DefaultVotes, mongo.DefaultPriority))
		}
		return m
	}

	// expected votes and priority of each member
	type vp struct{ votes, priority int }

	tests := map[string]struct {
		current   mongo.ConfigMembers
		desired   mongo.ConfigMembers
		unsafePSA bool
		primary   string
		expected  []vp
		// expected after a second SetVotes, if it differs from expected
		next []vp
	}{
		"priority is taken from desired members": {
			current:  rs(3),
			desired:  mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 1, 5), member("rs0-2", 1, 0)},
			expected: []vp{{1, 2}, {1, 5}, {1, 0}},
		},
		"member missing from desired members gets the default priority": {
			current:  mongo.ConfigMembers{member("rs0-0", 1, 5), member("rs0-1", 1, 2), member("rs0-2", 1, 2)},
			desired:  rs(3)[1:],
			expected: []vp{{1, 2}, {1, 2}, {1, 2}},
		},
		"unsafe PSA keeps the default priority of a member without a vote": {
			current:   mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 0, 0), arbiter("rs0-arbiter-0")},
			desired:   mongo.ConfigMembers{member("rs0-0", 1, 5), member("rs0-1", 1, 5), arbiter("rs0-arbiter-0")},
			unsafePSA: true,
			expected:  []vp{{1, 5}, {1, 2}, {1, 0}},
			next:      []vp{{1, 5}, {1, 5}, {1, 0}},
		},
		"voting member becomes non-voting": {
			current:  append(rs(3), nonVoting("rs0-nv-0", 1, 2)),
			desired:  append(rs(3), nonVoting("rs0-nv-0", 0, 0)),
			expected: []vp{{1, 2}, {1, 2}, {1, 2}, {0, 0}},
		},
		"member without a vote gets it back": {
			current:  mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 0, 0), member("rs0-2", 1, 2)},
			desired:  rs(3),
			expected: []vp{{1, 2}, {1, 2}, {1, 2}},
		},
		"hidden member without a vote gets it back": {
			current:  append(rs(2), hidden("rs0-hidden-0", 0)),
			desired:  append(rs(2), hidden("rs0-hidden-0", 1)),
			expected: []vp{{1, 2}, {1, 2}, {1, 0}},
		},
		"external members keep their votes and priority": {
			current:  append(rs(3), external("ext-0", 1, 1), external("ext-1", 0, 0)),
			desired:  append(rs(3), external("ext-0", 1, 3), external("ext-1", 1, 3)),
			expected: []vp{{1, 2}, {1, 2}, {0, 0}, {1, 1}, {0, 0}},
		},
		"hidden member before rs0 members loses its vote": {
			current:  append(mongo.ConfigMembers{hidden("rs0-hidden-0", 1)}, rs(3)...),
			desired:  append(rs(3), hidden("rs0-hidden-0", 1)),
			expected: []vp{{0, 0}, {1, 2}, {1, 2}, {1, 2}},
		},
		"hidden member loses its vote before a member without a vote": {
			current:  mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 0, 0), member("rs0-2", 1, 2), hidden("rs0-hidden-0", 1)},
			desired:  append(rs(3), hidden("rs0-hidden-0", 1)),
			expected: []vp{{1, 2}, {1, 2}, {1, 2}, {0, 0}},
		},
		"hidden member without a vote keeps not having it": {
			current:  append(rs(3), hidden("rs0-hidden-0", 1), hidden("rs0-hidden-1", 0), hidden("rs0-hidden-2", 1)),
			desired:  append(rs(3), hidden("rs0-hidden-0", 1), hidden("rs0-hidden-1", 1), hidden("rs0-hidden-2", 1)),
			expected: []vp{{1, 2}, {1, 2}, {1, 2}, {1, 0}, {0, 0}, {1, 0}},
		},
		"arbiter before rs0 members over the voting limit": {
			current:  append(mongo.ConfigMembers{arbiter("rs0-arbiter-0")}, rs(8)...),
			desired:  append(rs(8), arbiter("rs0-arbiter-0")),
			expected: []vp{{1, 0}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {0, 0}, {0, 0}},
		},
		"even votes are kept without a member to remove the vote from": {
			current:  mongo.ConfigMembers{nonVoting("rs0-nv-0", 0, 0), external("ext-0", 1, 1), arbiter("rs0-arbiter-0")},
			desired:  mongo.ConfigMembers{nonVoting("rs0-nv-0", 0, 0), external("ext-0", 1, 1), arbiter("rs0-arbiter-0")},
			expected: []vp{{0, 0}, {1, 1}, {1, 0}},
		},
		"even votes don't remove the vote of the primary": {
			current:  rs(4),
			desired:  rs(4),
			primary:  "rs0-3",
			expected: []vp{{1, 2}, {1, 2}, {0, 0}, {1, 2}},
		},
		"votes over the limit don't remove the vote of the primary": {
			current:  rs(8),
			desired:  rs(8),
			primary:  "rs0-7",
			expected: []vp{{1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {0, 0}, {1, 2}},
		},
		"even votes keep the existing non-voter": {
			current:  mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 0, 0), member("rs0-2", 1, 2), member("rs0-3", 1, 2)},
			desired:  rs(4),
			expected: []vp{{1, 2}, {0, 0}, {1, 2}, {1, 2}},
		},
		"votes over the limit keep the existing non-voters": {
			current: mongo.ConfigMembers{
				member("rs0-0", 0, 0), member("rs0-1", 1, 2), member("rs0-2", 1, 2), member("rs0-3", 0, 0), member("rs0-4", 1, 2),
				member("rs0-5", 1, 2), member("rs0-6", 1, 2), member("rs0-7", 1, 2), member("rs0-8", 1, 2),
			},
			desired:  rs(9),
			expected: []vp{{0, 0}, {1, 2}, {1, 2}, {0, 0}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}},
		},
		"only non-voting members": {
			current:  mongo.ConfigMembers{nonVoting("rs0-nv-0", 1, 2), nonVoting("rs0-nv-1", 0, 0)},
			desired:  mongo.ConfigMembers{nonVoting("rs0-nv-0", 0, 0), nonVoting("rs0-nv-1", 0, 0)},
			expected: []vp{{0, 0}, {0, 0}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			votes := func(m mongo.ConfigMembers) []vp {
				v := make([]vp, 0, len(m))
				for _, member := range m {
					v = append(v, vp{member.Votes, member.Priority})
				}
				return v
			}

			m := slices.Clone(tt.current)
			m.SetVotes(tt.desired, tt.unsafePSA, tt.primary)
			assert.Equal(t, tt.expected, votes(m))

			if tt.next != nil {
				m.SetVotes(tt.desired, tt.unsafePSA, tt.primary)
				assert.Equal(t, tt.next, votes(m))
			}

			again := slices.Clone(m)
			again.SetVotes(tt.desired, tt.unsafePSA, tt.primary)
			assert.Equal(t, m, again, "SetVotes is not idempotent")
		})
	}
}

func TestConfigAuthSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		conf       mongo.Config
		authSource string
	}{
		{
			name: "auth source set",
			conf: mongo.Config{
				Hosts:      []string{"localhost:27017"},
				Username:   "clusterMonitor",
				Password:   "pass",
				AuthSource: "admin",
			},
			authSource: "admin",
		},
		{
			name: "custom auth source",
			conf: mongo.Config{
				Hosts:      []string{"localhost:27017"},
				Username:   "app",
				Password:   "pass",
				AuthSource: "application",
			},
			authSource: "application",
		},
		{
			name: "auth source unset",
			conf: mongo.Config{
				Hosts:    []string{"localhost:27017"},
				Username: "clusterMonitor",
				Password: "pass",
			},
			authSource: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := c.conf.Options()
			require.NotNil(t, opts.Auth)
			assert.Equal(t, c.authSource, opts.Auth.AuthSource)

			uri, err := url.Parse(c.conf.URI())
			require.NoError(t, err)
			assert.Equal(t, c.authSource, uri.Query().Get("authSource"))
		})
	}
}

func voters(m mongo.ConfigMembers) map[string]struct{} {
	v := make(map[string]struct{})
	for _, member := range m {
		if member.Votes > 0 {
			v[member.Host] = struct{}{}
		}
	}
	return v
}

func votersChanged(prev, next mongo.ConfigMembers) int {
	p, n := voters(prev), voters(next)
	changed := 0
	for h := range p {
		if _, ok := n[h]; !ok {
			changed++
		}
	}
	for h := range n {
		if _, ok := p[h]; !ok {
			changed++
		}
	}
	return changed
}

// TestSetVotesDeadlock replays the AddNew/SetVotes reconfigs done by updateConfigMembers
// against a fake mongod that rejects non-force reconfigs changing more than 1 voting member.
func TestSetVotesDeadlock(t *testing.T) {
	mongod := func(host string) mongo.ConfigMember {
		return mongo.ConfigMember{Host: host, Votes: mongo.DefaultVotes, Priority: mongo.DefaultPriority}
	}
	hidden := mongo.ConfigMember{
		Host:   "rs0-hidden-0",
		Hidden: true,
		Votes:  mongo.DefaultVotes,
		Tags:   mongo.ReplsetTags{naming.ComponentHidden: "true"},
	}
	nonVoting := mongo.ConfigMember{
		Host: "rs0-nv-0",
		Tags: mongo.ReplsetTags{naming.ComponentNonVoting: "true"},
	}

	// desired members as built by getConfigMemberForPod, sorted by priority
	tests := map[string]struct {
		desired        mongo.ConfigMembers
		expectedVoters map[string]struct{}
	}{
		"hidden member": {
			desired:        mongo.ConfigMembers{mongod("rs0-0"), mongod("rs0-1"), mongod("rs0-2"), hidden, nonVoting},
			expectedVoters: map[string]struct{}{"rs0-0": {}, "rs0-1": {}, "rs0-2": {}},
		},
		"even number of members": {
			desired:        mongo.ConfigMembers{mongod("rs0-0"), mongod("rs0-1"), mongod("rs0-2"), mongod("rs0-3")},
			expectedVoters: map[string]struct{}{"rs0-0": {}, "rs0-2": {}, "rs0-3": {}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()

			// rs0-0 and rs0-1 are already in the replset, rs0-1 has no vote to keep the number of votes odd
			current := mongo.ConfigMembers{mongod("rs0-0"), mongod("rs0-1")}
			current[1].Votes, current[1].Priority = 0, 0

			// a reconfig can fail transiently, e.g. if the previous config is not committed yet
			failNextVotes := true

			writeConfig := func(next mongo.ConfigMembers) error {
				if n := votersChanged(current, next); n > 1 {
					return fmt.Errorf("(NewReplicaSetConfigurationIncompatible) %d voting members changed", n)
				}
				current = append(mongo.ConfigMembers(nil), next...)
				return nil
			}

			var pending bool
			for range 10 {
				cnf := append(mongo.ConfigMembers(nil), current...)
				if cnf.AddNew(ctx, tt.desired) {
					require.NoError(t, writeConfig(cnf), "add new")
				}

				cnf = append(mongo.ConfigMembers(nil), current...)
				_, pending = cnf.UpdateVotes(ctx, tt.desired, false, "rs0-0")
				if failNextVotes {
					failNextVotes = false
					continue
				}
				require.NoError(t, writeConfig(cnf), "set votes")
			}

			assert.False(t, pending, "voting changes pending")
			assert.Equal(t, tt.expectedVoters, voters(current))
		})
	}
}

// TestUpdateVotesConverges replays random topology changes through the reconfigs done by updateConfigMembers
// against a fake mongod, with transient reconfig failures and failovers, and checks that no reconfig is
// rejected for changing more than 1 voting member or exceeding the max number of voting members.
func TestUpdateVotesConverges(t *testing.T) {
	ctx := t.Context()

	type topology struct {
		rs, hidden, nonVoting int
		arbiter, unsafePSA    bool
	}

	// AddNew adds members with a vote and mongod rejects configs with more than MaxVotingMembers voting
	// members, so the members that can vote are limited to MaxVotingMembers across all topologies of a run.
	randomTopology := func(r *rand.Rand, maxRS, maxHidden int, arbiter bool) topology {
		tp := topology{rs: 1 + r.IntN(maxRS), hidden: r.IntN(maxHidden + 1), nonVoting: r.IntN(3)}
		tp.arbiter = arbiter && tp.rs >= 2 && r.IntN(4) == 0
		tp.unsafePSA = tp.rs == 2 && tp.arbiter && tp.nonVoting == 0 && r.IntN(2) == 0
		return tp
	}

	// desired members as built by getConfigMemberForPod, sorted by priority
	desiredMembers := func(tp topology) mongo.ConfigMembers {
		m := mongo.ConfigMembers{}
		for i := range tp.rs {
			m = append(m, mongo.ConfigMember{Host: fmt.Sprintf("rs0-%d", i), Votes: mongo.DefaultVotes, Priority: mongo.DefaultPriority})
		}
		for i := range tp.hidden {
			m = append(m, mongo.ConfigMember{
				Host:   fmt.Sprintf("rs0-hidden-%d", i),
				Votes:  mongo.DefaultVotes,
				Hidden: true,
				Tags:   mongo.ReplsetTags{naming.ComponentHidden: "true"},
			})
		}
		for i := range tp.nonVoting {
			m = append(m, mongo.ConfigMember{
				Host: fmt.Sprintf("rs0-nv-%d", i),
				Tags: mongo.ReplsetTags{naming.ComponentNonVoting: "true"},
			})
		}
		if tp.arbiter {
			m = append(m, mongo.ConfigMember{Host: "rs0-arbiter-0", Votes: mongo.DefaultVotes, ArbiterOnly: true})
		}
		return m
	}

	electable := func(m mongo.ConfigMember) bool {
		return m.Votes > 0 && m.Priority > 0 && !m.Hidden && !m.ArbiterOnly
	}

	for seed := range uint64(2000) {
		r := rand.New(rand.NewPCG(seed, 0))

		current := mongo.ConfigMembers{{Host: "rs0-0", Votes: mongo.DefaultVotes, Priority: mongo.DefaultPriority}}
		primary := "rs0-0"

		var tp topology
		var desired mongo.ConfigMembers
		var history []topology

		writeConfig := func(step string, next mongo.ConfigMembers, transientFailures bool) error {
			if transientFailures && r.IntN(10) == 0 {
				return errors.New("transient failure")
			}
			if n := len(voters(next)); n > mongo.MaxVotingMembers {
				t.Fatalf("seed %d: %s: %d voting members, topologies: %+v", seed, step, n, history)
			}
			if n := votersChanged(current, next); n > 1 {
				t.Fatalf("seed %d: %s: %d voting members changed, topologies: %+v", seed, step, n, history)
			}
			if i := slices.IndexFunc(next, func(m mongo.ConfigMember) bool { return m.Host == primary }); primary != "" && (i < 0 || !electable(next[i])) {
				if step == "update votes" {
					t.Fatalf("seed %d: %s: primary %s is not electable, topologies: %+v", seed, step, primary, history)
				}
				return errors.New("primary is not electable")
			}
			current = slices.Clone(next)
			return nil
		}

		elect := func() {
			candidates := []string{}
			for _, m := range current {
				if electable(m) && slices.ContainsFunc(desired, func(d mongo.ConfigMember) bool { return d.Host == m.Host }) {
					candidates = append(candidates, m.Host)
				}
			}
			primary = ""
			if len(candidates) > 0 {
				primary = candidates[r.IntN(len(candidates))]
			}
		}

		reconcile := func(transientFailures bool) bool {
			if !slices.ContainsFunc(desired, func(d mongo.ConfigMember) bool { return d.Host == primary }) || r.IntN(5) == 0 {
				elect()
			}

			cnf := slices.Clone(current)
			if cnf.RemoveOld(ctx, desired) && writeConfig("remove old", cnf, transientFailures) != nil {
				return false
			}
			if cnf.AddNew(ctx, desired) && writeConfig("add new", cnf, transientFailures) != nil {
				return false
			}
			changed, pending := cnf.UpdateVotes(ctx, desired, tp.unsafePSA, primary)
			if changed && writeConfig("update votes", cnf, transientFailures) != nil {
				return false
			}
			return !changed && !pending && len(cnf) == len(desired)
		}

		maxRS := 1 + r.IntN(mongo.MaxVotingMembers)
		maxHidden := r.IntN(min(3, mongo.MaxVotingMembers-maxRS) + 1)
		arbiter := maxRS+maxHidden < mongo.MaxVotingMembers

		for range 1 + r.IntN(5) {
			tp = randomTopology(r, maxRS, maxHidden, arbiter)
			desired = desiredMembers(tp)
			history = append(history, tp)

			for range r.IntN(6) {
				reconcile(true)
			}
		}

		converged := false
		for range 100 {
			if reconcile(true) && reconcile(false) {
				converged = true
				break
			}
		}
		require.Truef(t, converged, "seed %d: replset didn't converge, topologies: %+v", seed, history)

		for range 3 {
			elect()
			cnf := slices.Clone(current)
			changed, _ := cnf.UpdateVotes(ctx, desired, tp.unsafePSA, primary)
			require.Falsef(t, changed, "seed %d: votes changed after failover to %s, topologies: %+v", seed, primary, history)
		}

		v := len(voters(current))
		assert.LessOrEqualf(t, v, mongo.MaxVotingMembers, "seed %d: topologies: %+v", seed, history)
		if !tp.unsafePSA {
			assert.Equalf(t, 1, v%2, "seed %d: even number of voting members, topologies: %+v", seed, history)
		}
	}
}

func TestLimitVotingChanges(t *testing.T) {
	member := func(host string, votes, priority int) mongo.ConfigMember {
		return mongo.ConfigMember{Host: host, Votes: votes, Priority: priority}
	}

	prev := mongo.ConfigMembers{
		member("rs0-0", 1, 2),
		member("rs0-1", 0, 0),
		member("rs0-2", 1, 2),
		member("rs0-hidden-0", 1, 0),
	}

	maxVoters := mongo.ConfigMembers{}
	for i := range mongo.MaxVotingMembers {
		maxVoters = append(maxVoters, member(fmt.Sprintf("rs0-%d", i), 1, 2))
	}
	maxVoters = append(maxVoters, member("rs0-7", 0, 0))

	tests := map[string]struct {
		prev     mongo.ConfigMembers
		target   mongo.ConfigMembers
		primary  string
		expected mongo.ConfigMembers
		deferred bool
	}{
		"single change is kept": {
			prev:     prev,
			target:   mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 1, 2), member("rs0-2", 1, 2), member("rs0-hidden-0", 1, 0)},
			expected: mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 1, 2), member("rs0-2", 1, 2), member("rs0-hidden-0", 1, 0)},
		},
		"swap adds the vote first": {
			prev:     prev,
			target:   mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 1, 2), member("rs0-2", 1, 2), member("rs0-hidden-0", 0, 0)},
			expected: mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 1, 2), member("rs0-2", 1, 2), member("rs0-hidden-0", 1, 0)},
			deferred: true,
		},
		"swap removes the vote first with max voting members": {
			prev:     maxVoters,
			target:   append(maxVoters[:6:6], member("rs0-6", 0, 0), member("rs0-7", 1, 2)),
			expected: append(maxVoters[:6:6], member("rs0-6", 0, 0), member("rs0-7", 0, 0)),
			deferred: true,
		},
		"removing the vote of the primary isn't deferred with max voting members": {
			prev:     maxVoters,
			target:   append(maxVoters[:6:6], member("rs0-6", 0, 0), member("rs0-7", 1, 2)),
			primary:  "rs0-6",
			expected: append(maxVoters[:6:6], member("rs0-6", 0, 0), member("rs0-7", 0, 0)),
			deferred: true,
		},
		"priority changes without vote changes are kept": {
			prev:     prev,
			target:   mongo.ConfigMembers{member("rs0-0", 1, 3), member("rs0-1", 0, 0), member("rs0-2", 0, 0), member("rs0-hidden-0", 0, 0)},
			expected: mongo.ConfigMembers{member("rs0-0", 1, 3), member("rs0-1", 0, 0), member("rs0-2", 0, 0), member("rs0-hidden-0", 1, 0)},
			deferred: true,
		},
		"removing the vote of the primary is deferred": {
			prev:     prev,
			target:   mongo.ConfigMembers{member("rs0-0", 0, 0), member("rs0-1", 0, 0), member("rs0-2", 0, 0), member("rs0-hidden-0", 1, 0)},
			primary:  "rs0-0",
			expected: mongo.ConfigMembers{member("rs0-0", 1, 2), member("rs0-1", 0, 0), member("rs0-2", 0, 0), member("rs0-hidden-0", 1, 0)},
			deferred: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			deferred := tt.target.LimitVotingChanges(t.Context(), tt.prev, tt.primary)
			assert.Equal(t, tt.expected, tt.target)
			assert.Equal(t, tt.deferred, deferred)
		})
	}
}
