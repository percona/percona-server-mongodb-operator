package cluster

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/percona/percona-backup-mongodb/mdbstructs"
)

const (
	baseScore                      = 100
	tagMatchMultiplier             = 1.8
	hiddenMemberMultiplier         = 1.8
	secondaryMemberMultiplier      = 1.1
	priorityZeroMultiplier         = 1.3
	replsetOkLagMultiplier         = 1.1
	minPrioritySecondaryMultiplier = 1.2
	minVotesSecondaryMultiplier    = 1.2
)

var (
	maxOkReplsetLagDuration = 30 * time.Second
	maxReplsetLagDuration   = 5 * time.Minute
)

type ReplsetScoringMsg string

const (
	msgMemberDown                 ReplsetScoringMsg = "is down"
	msgMemberSecondary            ReplsetScoringMsg = "is secondary"
	msgMemberBadState             ReplsetScoringMsg = "has bad state"
	msgMemberTagMatch             ReplsetScoringMsg = "matches replset tag"
	msgMemberHidden               ReplsetScoringMsg = "is hidden"
	msgMemberPriorityZero         ReplsetScoringMsg = "has priority 0"
	msgMemberReplsetLagOk         ReplsetScoringMsg = "has ok replset lag"
	msgMemberReplsetLagFail       ReplsetScoringMsg = "has high replset lag"
	msgMemberMinPrioritySecondary ReplsetScoringMsg = "min priority secondary"
	msgMemberMinVotesSecondary    ReplsetScoringMsg = "min votes secondary"
)

type ReplsetScoringMember struct {
	config *mdbstructs.ReplsetConfigMember
	status *mdbstructs.ReplsetStatusMember
	score  int
	log    []ReplsetScoringMsg
}

func (m *ReplsetScoringMember) Name() string {
	return m.config.Host
}

func (m *ReplsetScoringMember) Score() int {
	return m.score
}

func (m *ReplsetScoringMember) Skip(msg ReplsetScoringMsg) {
	m.score = 0
	m.log = append(m.log, msg)
}

func (m *ReplsetScoringMember) MultiplyScore(multiplier float64, msg ReplsetScoringMsg) {
	newScore := float64(m.score) * multiplier
	m.score = int(math.Floor(newScore))
	m.log = append(m.log, msg)
}

func minPrioritySecondary(members []*ReplsetScoringMember) *ReplsetScoringMember {
	var minPriority *ReplsetScoringMember
	for _, member := range members {
		if member.status.State != mdbstructs.ReplsetMemberStateSecondary || member.config.Priority < 1 {
			continue
		}
		if minPriority == nil || member.config.Priority < minPriority.config.Priority {
			minPriority = member
		}
	}
	return minPriority
}

func minVotesSecondary(members []*ReplsetScoringMember) *ReplsetScoringMember {
	var minVotes *ReplsetScoringMember
	for _, member := range members {
		if member.status.State != mdbstructs.ReplsetMemberStateSecondary || member.config.Votes < 1 {
			continue
		}
		if minVotes == nil || member.config.Votes < minVotes.config.Votes {
			minVotes = member
		}
	}
	return minVotes
}

func (r *Replset) getReplsetScoringMembers() ([]*ReplsetScoringMember, error) {
	members := []*ReplsetScoringMember{}
	for _, cnfMember := range r.config.Members {
		var statusMember *mdbstructs.ReplsetStatusMember
		for _, m := range r.status.Members {
			if m.Name == cnfMember.Host {
				statusMember = m
				break
			}
		}
		if statusMember == nil {
			return nil, errors.New("no status info")
		}
		members = append(members, &ReplsetScoringMember{
			config: cnfMember,
			status: statusMember,
			score:  baseScore,
		})
	}
	return members, nil
}

type ReplsetScorer struct {
	replsetTags map[string]string
	members     []*ReplsetScoringMember
}

func (r *Replset) BackupSource(replsetTags map[string]string) (string, error) {
	r.Lock()
	defer r.Unlock()

	var err error
	var secondariesWithPriority int
	var secondariesWithVotes int

	scorer := &ReplsetScorer{replsetTags: replsetTags}
	scorer.members, err = r.getReplsetScoringMembers()
	if err != nil {
		return "", err
	}

	for _, member := range scorer.members {
		// replset health
		if member.status.Health != mdbstructs.ReplsetMemberHealthUp {
			member.Skip(msgMemberDown)
			continue
		}

		// replset state
		if member.status.State == mdbstructs.ReplsetMemberStateSecondary {
			member.MultiplyScore(secondaryMemberMultiplier, msgMemberSecondary)
		} else if member.status.State != mdbstructs.ReplsetMemberStatePrimary {
			member.Skip(msgMemberBadState)
			continue
		}

		// replset tags
		if len(replsetTags) > 0 && HasReplsetMemberTags(member.config, replsetTags) {
			member.MultiplyScore(tagMatchMultiplier, msgMemberTagMatch)
		}

		// secondaries only
		if member.status.State == mdbstructs.ReplsetMemberStateSecondary {
			if member.config.Hidden {
				member.MultiplyScore(hiddenMemberMultiplier, msgMemberHidden)
			} else if member.config.Priority == 0 {
				member.MultiplyScore(priorityZeroMultiplier, msgMemberPriorityZero)
			} else {
				secondariesWithPriority++
				if member.config.Votes > 0 {
					secondariesWithVotes++
				}
			}

			replsetLag, err := r.getLagDuration(r.getStatusMember(member.config.Host))
			if err != nil {
				return "", err
			}
			if replsetLag < maxOkReplsetLagDuration {
				member.MultiplyScore(replsetOkLagMultiplier, msgMemberReplsetLagOk)
			} else if replsetLag >= maxReplsetLagDuration {
				member.Skip(msgMemberReplsetLagFail)
			}
		}
	}

	// if there is more than one secondary with priority > 1, increase
	// score of secondary with the lowest priority
	if secondariesWithPriority > 1 {
		minPrioritySecondary := minPrioritySecondary(scorer.members)
		if minPrioritySecondary != nil {
			minPrioritySecondary.MultiplyScore(minPrioritySecondaryMultiplier, msgMemberMinPrioritySecondary)
		}
	}

	// if there is more than one secondary with votes > 1, increase
	// score of secondary with the lowest number of votes
	if secondariesWithVotes > 1 {
		fmt.Println(secondariesWithVotes)
		minVotesSecondary := minVotesSecondary(scorer.members)
		if minVotesSecondary != nil {
			minVotesSecondary.MultiplyScore(minVotesSecondaryMultiplier, msgMemberMinVotesSecondary)
		}
	}

	var winner *ReplsetScoringMember
	for _, member := range scorer.members {
		if member.score > 0 && winner == nil || member.score > winner.score {
			winner = member
		}
	}

	if winner == nil {
		return "", fmt.Errorf("Cannot find a winner")
	}
	return winner.config.Host, nil
}
