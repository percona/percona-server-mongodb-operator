package mongo

import (
	"context"
	"time"
)

func ElectableSecondaryLag(cfg RSConfig, status Status) (string, time.Duration, error) {
	return electableSecondaryLag(cfg, status)
}

func (m *ConfigMembers) SetVotes(compareWith ConfigMembers, unsafePSA bool, primary string) {
	m.setVotes(compareWith, unsafePSA, primary)
}

func (m *ConfigMembers) LimitVotingChanges(ctx context.Context, prev ConfigMembers, primary string) bool {
	return m.limitVotingChanges(ctx, prev, primary)
}
