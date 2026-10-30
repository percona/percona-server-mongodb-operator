package mongo

import "context"

func (m *ConfigMembers) SetVotes(compareWith ConfigMembers, unsafePSA bool, primary string) {
	m.setVotes(compareWith, unsafePSA, primary)
}

func (m *ConfigMembers) LimitVotingChanges(ctx context.Context, prev ConfigMembers, primary string) bool {
	return m.limitVotingChanges(ctx, prev, primary)
}
