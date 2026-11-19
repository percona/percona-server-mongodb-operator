package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapEqual(t *testing.T) {
	tests := map[string]struct {
		a    map[string]string
		b    map[string]string
		want bool
	}{
		"both nil":                   {a: nil, b: nil, want: true},
		"nil and empty":              {a: nil, b: map[string]string{}, want: true},
		"equal":                      {a: map[string]string{"k": "v"}, b: map[string]string{"k": "v"}, want: true},
		"different value":            {a: map[string]string{"k": "v"}, b: map[string]string{"k": "x"}, want: false},
		"different length":           {a: map[string]string{"k": "v"}, b: map[string]string{"k": "v", "k2": "v2"}, want: false},
		"same length different keys": {a: map[string]string{"a": "v"}, b: map[string]string{"b": "v"}, want: false},
		// Regression: equal length, different keys, and the differing entry in a
		// has an empty value. A missing key in b reads back as "", so a plain
		// b[k] != v comparison wrongly treats these maps as equal.
		"same length different keys empty value": {a: map[string]string{"a": ""}, b: map[string]string{"b": "v"}, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, MapEqual(tc.a, tc.b))
		})
	}
}
