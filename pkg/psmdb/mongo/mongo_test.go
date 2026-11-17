package mongo_test

import (
	"context"
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

// hiddenm marks a member hidden. Hidden is only ever legal alongside priority
// 0, so it is always applied to a member dbm already built with one.
func hiddenm(m mongo.ConfigMember) mongo.ConfigMember {
	m.Hidden = true
	return m
}

// members8 builds an eight-member set where the first `voting` members vote
// with priority 2 and the rest are non-voting with priority 0.
func members8(voting int) mongo.ConfigMembers {
	out := make(mongo.ConfigMembers, 0, 8)
	for i := range 8 {
		if i < voting {
			out = append(out, dbm(i, fmt.Sprintf("h%d", i), 1, 2))
			continue
		}
		out = append(out, dbm(i, fmt.Sprintf("h%d", i), 0, 0))
	}
	return out
}

// ceilingSwap is the desired state for a 7-voter set that hands h6's vote to
// h7, leaving the total unchanged.
func ceilingSwap() mongo.ConfigMembers {
	d := members8(7)
	d[6] = dbm(6, "h6", 0, 0)
	d[7] = dbm(7, "h7", 1, 2)
	return d
}

func TestApplyMemberConfig(t *testing.T) {

	cases := []struct {
		name         string
		live         mongo.ConfigMembers
		desired      mongo.ConfigMembers
		calls        int    // defaults to 1
		primary      string // current primary in rs.status()
		want         mongo.ConfigMembers
		wantChanged  bool
		wantPending  bool
		wantStepDown bool
		wantErr      string
	}{
		{
			name:    "no changes",
			live:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
			desired: mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
			want:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
		},
		{
			//  h2 (primary group) votes go from 1 -> 0, so we expect a step down
			name:         "the primary is not reconfigured out of electability",
			live:         mongo.ConfigMembers{dbm(0, "h0", 0, 0), dbm(1, "h1", 1, 1), dbm(2, "h2", 1, 2)},
			desired:      mongo.ConfigMembers{dbm(0, "h0", 0, 0), dbm(1, "h1", 1, 1), dbm(2, "h2", 0, 0)},
			primary:      "h2",
			want:         mongo.ConfigMembers{dbm(0, "h0", 0, 0), dbm(1, "h1", 1, 1), dbm(2, "h2", 1, 2)},
			wantPending:  true,
			wantStepDown: true,
		},
		{
			name:         "a priority-only change to the primary also waits",
			live:         mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
			desired:      mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 0)},
			primary:      "h1",
			want:         mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2)},
			wantPending:  true,
			wantStepDown: true,
		},
		{
			name:    "a blocked primary does not stall the other members",
			live:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired: mongo.ConfigMembers{dbm(0, "h0", 0, 0), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			primary: "h0",
			// h2 converges this pass; h0 is untouched and still pending.
			want:         mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			wantChanged:  true,
			wantPending:  true,
			wantStepDown: true,
		},
		{
			name:        "with no other electable member the change is attempted anyway",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 0), dbm(1, "h1", 1, 2)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 0), dbm(1, "h1", 0, 0)},
			primary:     "h1",
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 0), dbm(1, "h1", 0, 0)},
			wantChanged: true,
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
			name:        "two vote changes: only one applied, rest pending",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 0, 0), dbm(2, "h2", 0, 0)},
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 0, 0), dbm(2, "h2", 1, 2)},
			wantChanged: true,
			wantPending: true,
		},
		{
			name: "hidden waits for a deferred vote change",
			live: mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired: mongo.ConfigMembers{
				dbm(0, "h0", 1, 2),
				hiddenm(dbm(1, "h1", 0, 0)),
				hiddenm(dbm(2, "h2", 0, 0)),
			},
			want: mongo.ConfigMembers{
				dbm(0, "h0", 1, 2),
				hiddenm(dbm(1, "h1", 0, 0)),
				dbm(2, "h2", 1, 2),
			},
			wantChanged: true,
			wantPending: true,
		},
		{
			name: "votes, priority and hidden converge together on the second pass",
			live: mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired: mongo.ConfigMembers{
				dbm(0, "h0", 1, 2),
				hiddenm(dbm(1, "h1", 0, 0)),
				hiddenm(dbm(2, "h2", 0, 0)),
			},
			calls: 2,
			want: mongo.ConfigMembers{
				dbm(0, "h0", 1, 2),
				hiddenm(dbm(1, "h1", 0, 0)),
				hiddenm(dbm(2, "h2", 0, 0)),
			},
			wantChanged: true,
		},
		{
			// Un-hiding is safe in either order
			name: "unhiding is not deferred",
			live: mongo.ConfigMembers{
				dbm(0, "h0", 1, 2),
				hiddenm(dbm(1, "h1", 0, 0)),
				hiddenm(dbm(2, "h2", 0, 0)),
			},
			desired: mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			want: mongo.ConfigMembers{
				dbm(0, "h0", 1, 2),
				dbm(1, "h1", 1, 2),
				hiddenm(dbm(2, "h2", 0, 0)),
			},
			wantChanged: true,
			wantPending: true,
		},
		{
			name: "tags apply to a member whose vote change is deferred",
			live: mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired: mongo.ConfigMembers{
				dbm(0, "h0", 1, 2),
				dbm(1, "h1", 0, 0),
				func() mongo.ConfigMember {
					m := dbm(2, "h2", 0, 0)
					m.Tags = mongo.ReplsetTags{"podName": "h2", "workload": "analytics"}
					return m
				}(),
			},
			want: mongo.ConfigMembers{
				dbm(0, "h0", 1, 2),
				dbm(1, "h1", 0, 0),
				func() mongo.ConfigMember {
					m := dbm(2, "h2", 1, 2)
					m.Tags = mongo.ReplsetTags{"podName": "h2", "workload": "analytics"}
					return m
				}(),
			},
			wantChanged: true,
			wantPending: true,
		},
		{
			name:    "swap at the voting ceiling removes before it adds",
			live:    members8(7),
			desired: ceilingSwap(),
			want: func() mongo.ConfigMembers {
				w := members8(7)
				w[6] = dbm(6, "h6", 0, 0)
				return w
			}(),
			wantChanged: true,
			wantPending: true,
		},
		{
			name:        "ceiling swap converges on the second pass",
			live:        members8(7),
			desired:     ceilingSwap(),
			calls:       2,
			want:        ceilingSwap(),
			wantChanged: true,
		},
		{
			name:        "swap at a single voter adds before it removes",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 0, 0), dbm(2, "h2", 0, 0)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 0, 0), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			wantChanged: true,
			wantPending: true,
		},
		{
			name:        "growth is preferred while there is headroom",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2), dbm(3, "h3", 0, 0)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0), dbm(3, "h3", 1, 2)},
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2), dbm(3, "h3", 1, 2)},
			wantChanged: true,
			wantPending: true,
		},
		{
			name: "a votes change that cannot leave a legal config is rejected",
			live: members8(7),
			desired: func() mongo.ConfigMembers {
				d := members8(7)
				d[7] = dbm(7, "h7", 1, 2)
				return d
			}(),
			want:    members8(7),
			wantErr: "voting members",
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
			name:        "explicit votes survive repeated reconciliation",
			live:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired:     mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			calls:       5,
			want:        mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 0, 0)},
			wantChanged: false, // converged on the first call, no-op thereafter
		},
		{
			name: "arbiterOnly change is rejected before anything is mutated",
			live: mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			desired: mongo.ConfigMembers{
				dbm(0, "h0", 1, 10),
				dbm(1, "h1", 1, 2),
				func() mongo.ConfigMember {
					m := dbm(2, "h2", 1, 0)
					m.ArbiterOnly = true
					m.Tags = nil
					return m
				}(),
			},
			want:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "h1", 1, 2), dbm(2, "h2", 1, 2)},
			wantErr: "arbiterOnly cannot be changed",
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
			name:    "host absent from desired is left alone",
			live:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "gone", 1, 2)},
			desired: mongo.ConfigMembers{dbm(0, "h0", 1, 2)},
			want:    mongo.ConfigMembers{dbm(0, "h0", 1, 2), dbm(1, "gone", 1, 2)},
		},
		{
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
				changed  bool
				pending  bool
				stepDown bool
				err      error
			)
			for range calls {
				changed, pending, stepDown, err = live.ApplyMemberConfig(ctx, c.desired, c.primary)
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
				assert.Equal(t, c.wantStepDown, stepDown, "stepDownPrimary")
			}

			assert.Equal(t, c.want, live, "live config after the call")
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

func TestRemoveOld(t *testing.T) {
	t.Parallel()

	voting := func(id int, host string) mongo.ConfigMember {
		return mongo.ConfigMember{
			ID:       id,
			Host:     host,
			Votes:    mongo.DefaultVotes,
			Priority: mongo.DefaultPriority,
		}
	}
	nonVoting := func(id int, host string) mongo.ConfigMember {
		return mongo.ConfigMember{
			ID:       id,
			Host:     host,
			Votes:    0,
			Priority: 0,
		}
	}

	cases := []struct {
		name string
		// mset is the current replset config, compareWith the members the operator wants.
		mset        *mongo.ConfigMembers
		compareWith mongo.ConfigMembers
		// wantHosts is the config expected after a single RemoveOld call, in order.
		wantHosts   []string
		wantRemoved bool
	}{
		{
			name:        "empty config",
			mset:        &mongo.ConfigMembers{},
			compareWith: mongo.ConfigMembers{},
			wantHosts:   []string{},
			wantRemoved: false,
		},
		{
			name:        "nothing to remove",
			mset:        &mongo.ConfigMembers{voting(0, "rs0-0"), voting(1, "rs0-1"), voting(2, "rs0-2")},
			compareWith: mongo.ConfigMembers{voting(0, "rs0-0"), voting(1, "rs0-1"), voting(2, "rs0-2")},
			wantHosts:   []string{"rs0-0", "rs0-1", "rs0-2"},
			wantRemoved: false,
		},
		{
			name:        "one voting orphan is removed",
			mset:        &mongo.ConfigMembers{voting(0, "rs0-0"), voting(1, "rs0-1"), voting(2, "rs0-2")},
			compareWith: mongo.ConfigMembers{voting(0, "rs0-0"), voting(1, "rs0-1")},
			wantHosts:   []string{"rs0-0", "rs0-1"},
			wantRemoved: true,
		},
		{
			name:        "two voting orphans: only the last one goes",
			mset:        &mongo.ConfigMembers{voting(0, "rs0-0"), voting(1, "rs0-1"), voting(2, "rs0-2")},
			compareWith: mongo.ConfigMembers{voting(0, "rs0-0")},
			wantHosts:   []string{"rs0-0", "rs0-1"},
			wantRemoved: true,
		},
		{
			name: "hidden orphan does not consume the voting removal",
			mset: &mongo.ConfigMembers{
				voting(0, "rs0-0"), voting(1, "rs0-1"), voting(2, "rs0-2"), nonVoting(3, "rs0-hidden-0"),
			},
			compareWith: mongo.ConfigMembers{voting(0, "rs0-0")},
			wantHosts:   []string{"rs0-0", "rs0-1"},
			wantRemoved: true,
		},
		{
			name: "all non-voting orphans go at once",
			mset: &mongo.ConfigMembers{
				voting(0, "rs0-0"), voting(1, "rs0-1"), nonVoting(2, "nv-0"), nonVoting(3, "nv-1"),
			},
			compareWith: mongo.ConfigMembers{voting(0, "rs0-0"), voting(1, "rs0-1")},
			wantHosts:   []string{"rs0-0", "rs0-1"},
			wantRemoved: true,
		},
		{
			name: "non-voting orphans removed alongside one voting orphan",
			mset: &mongo.ConfigMembers{
				voting(0, "rs0-0"), voting(1, "rs0-1"), voting(2, "rs0-2"),
				nonVoting(3, "nv-0"), nonVoting(4, "rs0-hidden-0"),
			},
			compareWith: mongo.ConfigMembers{voting(0, "rs0-0")},
			wantHosts:   []string{"rs0-0", "rs0-1"},
			wantRemoved: true,
		},
		{
			name:        "never empties the config",
			mset:        &mongo.ConfigMembers{nonVoting(0, "nv-0"), nonVoting(1, "nv-1")},
			compareWith: mongo.ConfigMembers{},
			wantHosts:   []string{"nv-0"},
			wantRemoved: true,
		},
		{
			name:        "single member is kept even when orphaned",
			mset:        &mongo.ConfigMembers{voting(0, "rs0-0")},
			compareWith: mongo.ConfigMembers{},
			wantHosts:   []string{"rs0-0"},
			wantRemoved: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			votesBefore := 0
			for _, member := range *c.mset {
				votesBefore += member.Votes
			}

			removed := c.mset.RemoveOld(t.Context(), c.compareWith)

			gotHosts := make([]string, 0, len(*c.mset))
			votesAfter := 0
			for _, member := range *c.mset {
				gotHosts = append(gotHosts, member.Host)
				votesAfter += member.Votes
			}

			assert.Equal(t, c.wantRemoved, removed, "return value")
			assert.Equal(t, c.wantHosts, gotHosts, "members left in config")

			assert.LessOrEqualf(t, votesBefore-votesAfter, 1,
				"removed %d votes in one call, max 1 allowed", votesBefore-votesAfter)

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
