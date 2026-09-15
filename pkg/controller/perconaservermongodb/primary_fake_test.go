package perconaservermongodb

import (
	"context"
	"strings"
	"sync"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	mongoFake "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo/fake"
)

// primaryProvider answers isPodPrimary per pod.
//
// isPodPrimary dials a standalone client against the pod's host and reads
// IsMaster, so steering the primary means steering that one call. Every code
// path that orders a shutdown, a step-down or a rolling update branches on it,
// and none of them is testable without this.
//
// primaries is keyed by pod name; MongoHost produces
// "<pod>.<svc>.<ns>.svc.cluster.local:27017", so the lookup matches on the
// leading segment.
type primaryProvider struct {
	mu        sync.Mutex
	primaries map[string]bool
	// isMasterCalls records the pod name of every IsMaster call, in order, so a
	// test can assert which members were asked and in what sequence.
	isMasterCalls []string
	// stepDowns records the hosts StepDown was called against.
	stepDowns []string
	// freezes records the hosts replSetFreeze was called against.
	freezes []string
}

func newPrimaryProvider(primaries ...string) *primaryProvider {
	p := &primaryProvider{primaries: make(map[string]bool, len(primaries))}
	for _, name := range primaries {
		p.primaries[name] = true
	}
	return p
}

// podNameFromHost returns the pod name from a mongo host, which is the segment
// before the first dot (or before the port, for a bare host:port).
func podNameFromHost(host string) string {
	if name, _, ok := strings.Cut(host, "."); ok {
		return name
	}
	name, _, _ := strings.Cut(host, ":")
	return name
}

func (p *primaryProvider) Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole) (mongo.Client, error) {
	return &primaryFakeClient{provider: p, Client: mongoFake.NewClient()}, nil
}

func (p *primaryProvider) Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (mongo.Client, error) {
	return &primaryFakeClient{provider: p, Client: mongoFake.NewClient()}, nil
}

func (p *primaryProvider) Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, host string, tlsEnabled bool) (mongo.Client, error) {
	return &primaryFakeClient{provider: p, pod: podNameFromHost(host), Client: mongoFake.NewClient()}, nil
}

// primaryFakeClient overrides only the calls a primary decision depends on and
// delegates everything else to the package fake.
type primaryFakeClient struct {
	mongo.Client

	provider *primaryProvider
	pod      string
}

func (c *primaryFakeClient) IsMaster(ctx context.Context) (*mongo.IsMasterResp, error) {
	c.provider.mu.Lock()
	defer c.provider.mu.Unlock()

	c.provider.isMasterCalls = append(c.provider.isMasterCalls, c.pod)

	return &mongo.IsMasterResp{
		IsMaster:   c.provider.primaries[c.pod],
		OKResponse: mongo.OKResponse{OK: 1},
	}, nil
}

func (c *primaryFakeClient) StepDown(ctx context.Context, seconds int, force bool) error {
	c.provider.mu.Lock()
	defer c.provider.mu.Unlock()

	c.provider.stepDowns = append(c.provider.stepDowns, c.pod)
	// A stepped-down member is no longer the primary; without this a caller that
	// re-reads the state after stepping down sees an impossible cluster.
	delete(c.provider.primaries, c.pod)

	return nil
}

func (c *primaryFakeClient) Freeze(ctx context.Context, seconds int) error {
	c.provider.mu.Lock()
	defer c.provider.mu.Unlock()

	c.provider.freezes = append(c.provider.freezes, c.pod)

	return nil
}

// calls returns a copy of the recorded IsMaster pod names.
func (p *primaryProvider) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.isMasterCalls...)
}
