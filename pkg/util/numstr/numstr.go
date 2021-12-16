package numstr

import (
	"log"
	"strconv"

	"github.com/pkg/errors"
)

// +kubebuilder:validation:Type="number"
type NumberString string

func Parse(s string) (NumberString, error) {
	// ensure valid value to keep invariant
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return "0", errors.Wrapf(err, "unable to parse %#v", s)
	}

	return NumberString(strconv.FormatFloat(f, 'f', -1, 64)), nil
}

func MustParse(s string) NumberString {
	r, err := Parse(s)
	if err != nil {
		log.Fatal(err)
	}

	return r
}

func (r *NumberString) Float64() float64 {
	f, _ := strconv.ParseFloat(string(*r), 64)
	return f
}

func (r *NumberString) String() string {
	return string(*r)
}

func (r *NumberString) MarshalJSON() ([]byte, error) {
	return []byte(r.String()), nil
}

func (r *NumberString) UnmarshalJSON(bytes []byte) (err error) {
	*r, err = Parse(string(bytes))
	return
}
