package stub

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrintCommandOutput(t *testing.T) {
	var out bytes.Buffer
	output := execCommandOutput{}
	_, err := output.stdout.WriteString("test stdout")
	assert.NoError(t, err)

	assert.NoError(t, printCommandOutput("test", t.Name(), output, &out))
	assert.Regexp(t, "test stdout\n$", out.String())
	assert.NotRegexp(t, "stderr\n$", out.String())
	out.Reset()

	_, err = output.stderr.WriteString("test stderr")
	assert.NoError(t, err)
	assert.NoError(t, printCommandOutput("test", t.Name(), output, &out))
	assert.Regexp(t, "test stderr\n$", out.String())
}
