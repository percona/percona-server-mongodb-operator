package v1

import (
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestMongoConfiguration_GetPort(t *testing.T) {
	tests := map[string]struct {
		conf        MongoConfiguration
		expectPort  int32
		expectError error
	}{
		"valid port": {
			conf: `net:
  port: 27017`,
			expectPort: 27017,
		},
		"invalid port type": {
			conf: `net:
  port: invalid`,
			expectError: errors.New("error unmarshalling configuration yaml: unmarshal errors:\n  line 2: cannot unmarshal !!str `invalid` into int32"),
		},
		"error getting net": {
			conf:        "error",
			expectError: errors.New("error unmarshalling configuration yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `error` into struct { Net struct { Port int32 \"yaml:\\\"port,omitempty\\\"\" } \"yaml:\\\"net,omitempty\\\"\" }"),
		},
		"not existing net.port in config": {
			conf: `net:
  key: value`,
			expectPort: int32(0),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			port, err := tt.conf.GetPort()
			if tt.expectError != nil {
				assert.EqualError(t, err, tt.expectError.Error())
				assert.Equal(t, int32(0), port)
			} else {
				assert.Equal(t, tt.expectPort, port)
				assert.NoError(t, err)
			}

		})
	}
}

func TestMongoConfiguration_SetPort(t *testing.T) {

	tests := map[string]struct {
		expectedConf MongoConfiguration
		actualConf   MongoConfiguration
		port         int32
		expectError  error
	}{
		"set config with port": {
			actualConf:   MongoConfiguration(""),
			expectedConf: "net:\n  port: 12345\n",
			port:         12345,
		},
		"set config on non-empty config": {
			actualConf:  MongoConfiguration("non-empty"),
			port:        12345,
			expectError: errors.New("configuration is not empty; refusing to overwrite"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.actualConf.SetPort(tt.port)
			if tt.expectError != nil {
				assert.EqualError(t, err, tt.expectError.Error())
			} else {
				assert.Equal(t, tt.expectedConf, tt.actualConf)
				assert.NoError(t, err)
			}
		})
	}
}

func TestMongosSpec_GetPort(t *testing.T) {
	tests := map[string]struct {
		ms       MongosSpec
		expected int32
	}{
		"return port from configuration": {
			ms: MongosSpec{
				Port: 27019,
				Configuration: `net:
  port: 27018`,
			},
			expected: 27018,
		},
		"return port from spec": {
			ms: MongosSpec{
				Port: 27019,
				Configuration: `net:
  port: 0`,
			},
			expected: 27019,
		},
		"port from spec is zero, return default": {
			ms: MongosSpec{
				Port: 0,
			},
			expected: 27017,
		},
		"port not configured anywhere, return default": {
			expected: 27017,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := tt.ms.GetPort()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestReplsetSpec_GetPort(t *testing.T) {
	tests := map[string]struct {
		config   MongoConfiguration
		expected int32
	}{
		"valid port": {
			config: `net:
  port: 28017`,
			expected: 28017,
		},
		"no configuration, return default": {
			expected: 27017,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ms := ReplsetSpec{Configuration: tt.config}
			got := ms.GetPort()
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestBackupSpec_MainStorage(t *testing.T) {
	tests := map[string]struct {
		spec        BackupSpec
		expected    string
		expectedErr error
	}{
		"no storages": {
			spec:        BackupSpec{},
			expected:    "",
			expectedErr: ErrNoMainStorage,
		},
		"single storage": {
			spec: BackupSpec{
				Storages: map[string]BackupStorageSpec{
					"storage-1": {
						Type: BackupStorageS3,
						S3:   BackupStorageS3Spec{},
					},
				},
			},
			expected:    "storage-1",
			expectedErr: nil,
		},
		"multiple storages": {
			spec: BackupSpec{
				Storages: map[string]BackupStorageSpec{
					"storage-1": {
						Type: BackupStorageS3,
						S3:   BackupStorageS3Spec{},
					},
					"storage-2": {
						Main: true,
						Type: BackupStorageS3,
						S3:   BackupStorageS3Spec{},
					},
					"storage-3": {
						Type: BackupStorageS3,
						S3:   BackupStorageS3Spec{},
					},
				},
			},
			expected:    "storage-2",
			expectedErr: nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			stgName, _, err := tt.spec.MainStorage()
			assert.Equal(t, tt.expected, stgName)
			assert.Equal(t, tt.expectedErr, err)
		})
	}
}
