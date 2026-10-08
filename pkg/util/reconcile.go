package util

import (
	"os"
	"time"

	"github.com/go-logr/logr"
)

// MinReconcileInterval is the lowest reconciliation interval a controller may be
// configured with. Shorter intervals put unnecessary load on the API server.
const MinReconcileInterval = 5 * time.Second

// ReconcileInterval returns the reconciliation interval configured through the
// environment variable envVar. It falls back to MinReconcileInterval if the
// variable is unset, unparsable or shorter than MinReconcileInterval.
func ReconcileInterval(log logr.Logger, envVar string) time.Duration {
	value := os.Getenv(envVar)
	if value == "" {
		return MinReconcileInterval
	}

	d, err := time.ParseDuration(value)
	if err != nil {
		log.Info("Invalid reconcile interval, using default", "envVar", envVar, "value", value, "error", err, "default", MinReconcileInterval)
		return MinReconcileInterval
	}

	if d < MinReconcileInterval {
		log.Info("Reconcile interval is below the minimum, using default", "envVar", envVar, "value", value, "default", MinReconcileInterval)
		return MinReconcileInterval
	}

	return d
}
