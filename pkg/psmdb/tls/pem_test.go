package tls

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEqualPEM(t *testing.T) {
	ca1, _, err := IssueCA()
	require.NoError(t, err)
	ca2, _, err := IssueCA()
	require.NoError(t, err)

	bundle := append(append([]byte{}, ca1...), ca2...)
	reversedBundle := append(append([]byte{}, ca2...), ca1...)

	tests := map[string]struct {
		a, b     []byte
		expected bool
	}{
		"identical": {
			a:        ca1,
			b:        ca1,
			expected: true,
		},
		"extra trailing newline": {
			a:        append(bytes.TrimRight(ca1, "\n"), '\n', '\n'),
			b:        append(bytes.TrimRight(ca1, "\n"), '\n'),
			expected: true,
		},
		"CRLF line endings": {
			a:        bytes.ReplaceAll(ca1, []byte("\n"), []byte("\r\n")),
			b:        ca1,
			expected: true,
		},
		"whitespace between bundle blocks": {
			a:        bundle,
			b:        append(append(append([]byte{}, ca1...), '\n'), ca2...),
			expected: true,
		},
		"different certificates": {
			a:        ca1,
			b:        ca2,
			expected: false,
		},
		"bundle vs single": {
			a:        bundle,
			b:        ca1,
			expected: false,
		},
		"bundle in different order": {
			a:        bundle,
			b:        reversedBundle,
			expected: false,
		},
		"both empty": {
			a:        nil,
			b:        []byte{},
			expected: false,
		},
		"both invalid": {
			a:        []byte("garbage"),
			b:        []byte("garbage"),
			expected: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.expected, EqualPEM(tt.a, tt.b))
		})
	}
}
