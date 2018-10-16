package stub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateMongoDBKey(t *testing.T) {
	str, err := generateMongoDBKey()
	assert.NoError(t, err)
	assert.Len(t, str, 1024)
}
