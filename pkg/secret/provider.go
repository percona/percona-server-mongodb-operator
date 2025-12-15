package secret

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const cleanupInterval = 5 * time.Minute

type Client interface {
	Name() string

	Update(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) error

	FillSecretData(ctx context.Context, data map[string][]byte) (bool, error)

	Close() error
}

type Provider interface {
	NewClient() Client
}

// ProviderHandler manages all secret providers supported by the operator.
// On Update, it initializes or refreshes a secret-provider client for each cluster that has a provider configured.
type ProviderHandler struct {
	providers []Provider
	clients   map[string][]Client

	lastCleanup time.Time
}

func NewProviderHandler(providers ...Provider) *ProviderHandler {
	if len(providers) == 0 {
		return nil
	}

	return &ProviderHandler{
		providers: providers,
		clients:   make(map[string][]Client),
	}
}

// criticalError is an error that should be joined with errors to cause the controller to stop the reconcile loop.
// Secret providers should not block reconciliation for errors unrelated to user actions (for example, connection issues).
// However, reconciliation should be blocked when the user provides incorrect configuration or fails to create a required resource.
var criticalError error = errors.New("critical")

func NewCriticalErr(err error) error {
	return stderrors.Join(criticalError, err)
}

func IsCriticalErr(err error) bool {
	return errors.Is(err, criticalError)
}

func (h *ProviderHandler) Update(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) error {
	if h == nil {
		return nil
	}
	h.ensureClients(cr)
	clients, ok := h.clients[cr.NamespacedName().String()]
	if !ok {
		return errors.New("ensureClients didn't initialize clients")
	}

	var errs []error
	for _, c := range clients {
		if err := c.Update(ctx, cl, cr); err != nil {
			errs = append(errs, err)
		}
	}

	if err := h.cleanupOutdatedClients(ctx, cl); err != nil {
		errs = append(errs, errors.Wrap(err, "cleanup outdated clients"))
	}

	return stderrors.Join(errs...)
}

func (h *ProviderHandler) FillSecretData(ctx context.Context, cr *api.PerconaServerMongoDB, data map[string][]byte) (bool, error) {
	if h == nil {
		return false, nil
	}
	clients, ok := h.clients[cr.NamespacedName().String()]
	if !ok {
		return false, nil
	}

	log := logf.FromContext(ctx)

	changed := false
	var errs []error
	for _, p := range clients {
		c, err := p.FillSecretData(ctx, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if c {
			changed = true
			log.Info("Changes in the secret provider detected. Updating users secret", "cluster", cr.Name, "namespace", cr.Namespace, "secret provider", p.Name())
		}
	}
	return changed, stderrors.Join(errs...)
}

func (h *ProviderHandler) ensureClients(cr *api.PerconaServerMongoDB) {
	if h == nil {
		return
	}
	if h.clients == nil {
		h.clients = make(map[string][]Client)
	}
	if _, ok := h.clients[cr.NamespacedName().String()]; ok {
		return
	}

	clients := []Client{}
	for _, provider := range h.providers {
		clients = append(clients, provider.NewClient())
	}
	h.clients[cr.NamespacedName().String()] = clients
}

func (h *ProviderHandler) cleanupOutdatedClients(ctx context.Context, cl client.Client) error {
	if h == nil || time.Since(h.lastCleanup) < cleanupInterval {
		return nil
	}

	h.lastCleanup = time.Now()

	var errs []error
	for nnStr, clients := range h.clients {
		nnSplit := strings.Split(nnStr, string(types.Separator))
		if len(nnSplit) != 2 {
			return errors.Errorf("wrong namespaced name string: %s", nnStr)
		}
		if err := cl.Get(ctx, types.NamespacedName{
			Name:      nnSplit[1],
			Namespace: nnSplit[0],
		}, new(api.PerconaServerMongoDB)); err != nil {
			if k8serrors.IsNotFound(err) {
				for _, cl := range clients {
					if err := cl.Close(); err != nil {
						errs = append(errs, err)
					}
				}
				delete(h.clients, nnStr)
				continue
			}
			errs = append(errs, err)
		}
	}
	return stderrors.Join(errs...)
}
