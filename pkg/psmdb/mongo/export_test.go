package mongo

import "context"

func (m *ConfigMembers) SetVotes(compareWith ConfigMembers, unsafePSA bool) {
	m.setVotes(compareWith, unsafePSA)
}

func (m *ConfigMembers) LimitVotingChanges(ctx context.Context, prev ConfigMembers, primary string) bool {
	return m.limitVotingChanges(ctx, prev, primary)
}
