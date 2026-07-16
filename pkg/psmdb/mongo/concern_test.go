package mongo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseWriteConcernW(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		want interface{}
	}{
		"majority stays a string":      {in: "majority", want: "majority"},
		"custom tag stays a string":    {in: "myTag", want: "myTag"},
		"integer 0 becomes int":        {in: "0", want: 0},
		"integer 1 becomes int":        {in: "1", want: 1},
		"integer 3 becomes int":        {in: "3", want: 3},
		"negative falls back to string": {in: "-1", want: "-1"},
		"empty stays a string":         {in: "", want: ""},
		"numeric-looking tag with letters stays a string": {in: "1a", want: "1a"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseWriteConcernW(tt.in))
		})
	}
}
