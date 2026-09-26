package tool

import (
	"testing"

	"github.com/alecthomas/kingpin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfigAuthSource(t *testing.T) {
	app := kingpin.New("test", "")

	conf, err := NewConfig(app, EnvMongoDBClusterMonitorUser, EnvMongoDBClusterMonitorPassword)
	require.NoError(t, err)

	assert.Equal(t, "admin", conf.AuthSource)
	assert.True(t, conf.Direct)
}
