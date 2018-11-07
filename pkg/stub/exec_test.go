package stub

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrintCommandOutput(t *testing.T) {
	var output bytes.Buffer
	var stderr bytes.Buffer
	var stdout bytes.Buffer

	_, err := stdout.WriteString("test stdout")
	assert.NoError(t, err)
	printCommandOutput("test", t.Name(), &stdout, &stderr, &output)
	assert.Regexp(t, "test stdout$", output.String())
	assert.NotRegexp(t, "stderr$", output.String())
	output.Reset()

	_, err = stderr.WriteString("test stderr")
	assert.NoError(t, err)
	printCommandOutput("test", t.Name(), &stdout, &stderr, &output)
	assert.Regexp(t, "test stderr$", output.String())
}
