package storage_test

import (
	"testing"

	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

func TestComputePartSize(t *testing.T) {
	const (
		_  = iota
		KB = 1 << (10 * iota)
		MB
		GB
	)

	const (
		defaultSize = 10 * MB
		minSize     = 5 * MB
		maxParts    = 10000
	)

	tests := []struct {
		name     string
		fileSize int64
		userSize int64
		want     int64
	}{
		{
			name:     "default",
			fileSize: 0,
			userSize: 0,
			want:     defaultSize,
		},
		{
			name:     "user size provided",
			fileSize: 0,
			userSize: 20 * MB,
			want:     20 * MB,
		},
		{
			name:     "user size less than min",
			fileSize: 0,
			userSize: 4 * MB,
			want:     minSize,
		},
		{
			name:     "file size requires larger part size",
			fileSize: 100 * GB,
			userSize: 0,
			want:     100 * GB / maxParts * 15 / 10,
		},
		{
			name:     "file size requires larger part size than user size",
			fileSize: 100 * GB,
			userSize: 10 * MB,
			want:     100 * GB / maxParts * 15 / 10,
		},
		{
			name:     "file size does not require larger part size",
			fileSize: 50 * GB,
			userSize: 0,
			want:     defaultSize,
		},
		{
			name:     "file size with user size",
			fileSize: 50 * GB,
			userSize: 12 * MB,
			want:     12 * MB,
		},
		{
			name:     "zero file size",
			fileSize: 0,
			userSize: 0,
			want:     defaultSize,
		},
		{
			name:     "zero user size",
			fileSize: 100 * GB,
			userSize: 0,
			want:     100 * GB / maxParts * 15 / 10,
		},
		{
			name:     "negative user size",
			fileSize: 0,
			userSize: -1,
			want:     defaultSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storage.ComputePartSize(tt.fileSize, defaultSize, minSize, maxParts, tt.userSize)
			if got != tt.want {
				t.Errorf("ComputePartSize() = %v, want %v", got, tt.want)
			}
		})
	}
}
