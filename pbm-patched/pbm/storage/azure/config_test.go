package azure

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestClone(t *testing.T) {
	f := 1.1
	c1 := &Config{
		Account:        "acc",
		Container:      "cnt",
		EndpointURL:    "ep.com",
		EndpointURLMap: map[string]string{"n1": "ep1", "n2": "ep2"},
		Prefix:         "p1",
		Credentials: Credentials{
			Key: "k1",
		},
		MaxObjSizeGB: &f,
		Retryer: &Retryer{
			NumMaxRetries: 5,
			MinRetryDelay: 10 * time.Second,
			MaxRetryDelay: 20 * time.Second,
		},
	}

	c2 := c1.Clone()

	if &c1.EndpointURLMap == &c2.EndpointURLMap ||
		c1.MaxObjSizeGB == c2.MaxObjSizeGB ||
		c1.Retryer == c2.Retryer {
		t.Fatal("Deep copy of pointer fields is missing")
	}
	if !reflect.DeepEqual(c1, c2) {
		t.Fatalf("Clone is not performed, diff=%s", cmp.Diff(*c1, *c2))
	}
}

func TestEqual(t *testing.T) {
	f := 1.1
	c1 := &Config{
		Account:        "acc",
		Container:      "cnt",
		EndpointURL:    "ep.com",
		EndpointURLMap: map[string]string{"n1": "ep1", "n2": "ep2"},
		Prefix:         "p1",
		Credentials: Credentials{
			Key: "k1",
		},
		MaxObjSizeGB: &f,
		Retryer: &Retryer{
			NumMaxRetries: 5,
			MinRetryDelay: 10 * time.Second,
			MaxRetryDelay: 20 * time.Second,
		},
	}

	c2 := c1.Clone()

	if !c1.Equal(c2) {
		t.Fatalf("cfg should be equal, diff=%s", cmp.Diff(*c1, *c2))
	}
}

func TestCast(t *testing.T) {
	var c *Config
	err := c.Cast()
	if err == nil {
		t.Fatal("sigsegv should have happened instead")
	}

	c = &Config{}
	err = c.Cast()
	if err != nil {
		t.Fatalf("got error during Cast: %v", err)
	}
	want := &Config{
		Retryer: &Retryer{
			NumMaxRetries: defaultMaxRetries,
			MinRetryDelay: defaultMinRetryDelay,
			MaxRetryDelay: defaultMaxRetryDelay,
		},
	}

	if !c.Equal(want) {
		t.Fatalf("wrong config after Cast, diff=%s", cmp.Diff(*c, *want))
	}
}
