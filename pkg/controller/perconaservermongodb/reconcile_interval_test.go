package perconaservermongodb

import (
	"os"
	"testing"
	"time"
)

func TestGetReconcileInterval(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected time.Duration
	}{
		{
			name:     "default when env not set",
			envValue: "",
			expected: 30 * time.Second,
		},
		{
			name:     "valid duration 15s",
			envValue: "15s",
			expected: 15 * time.Second,
		},
		{
			name:     "valid duration 60s",
			envValue: "60s",
			expected: 60 * time.Second,
		},
		{
			name:     "valid duration 2m",
			envValue: "2m",
			expected: 2 * time.Minute,
		},
		{
			name:     "invalid duration falls back to default",
			envValue: "notaduration",
			expected: 30 * time.Second,
		},
		{
			name:     "zero duration falls back to default",
			envValue: "0s",
			expected: 30 * time.Second,
		},
		{
			name:     "negative duration falls back to default",
			envValue: "-5s",
			expected: 30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue == "" {
				os.Unsetenv("RESYNC_PERIOD")
			} else {
				os.Setenv("RESYNC_PERIOD", tt.envValue)
			}
			defer os.Unsetenv("RESYNC_PERIOD")

			got := getReconcileInterval()
			if got != tt.expected {
				t.Errorf("getReconcileInterval() = %v, want %v", got, tt.expected)
			}
		})
	}
}
