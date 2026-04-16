package mio

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestClone(t *testing.T) {
	f := 1.1
	c1 := &Config{
		Region:      "eu",
		Endpoint:    "ep.com",
		EndpointMap: map[string]string{"n1": "ep1", "n2": "ep2"},
		Bucket:      "b1",
		Prefix:      "p1",
		Credentials: Credentials{
			AccessKeyID:     "k1",
			SecretAccessKey: "k2",
			SessionToken:    "sess",
		},
		Secure:       true,
		PartSize:     6 << 20,
		MaxObjSizeGB: &f,
		Retryer: &Retryer{
			NumMaxRetries: 1,
		},
	}

	c2 := c1.Clone()

	if &c1.EndpointMap == &c2.EndpointMap ||
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
		Region:      "eu",
		Endpoint:    "ep.com",
		EndpointMap: map[string]string{"n1": "ep1", "n2": "ep2"},
		Bucket:      "b1",
		Prefix:      "p1",
		Credentials: Credentials{
			AccessKeyID:     "k1",
			SecretAccessKey: "k2",
			SessionToken:    "sess",
		},
		Secure:       true,
		PartSize:     6 << 20,
		MaxObjSizeGB: &f,
		Retryer: &Retryer{
			NumMaxRetries: 1,
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
	if err := c.Cast(); err == nil {
		t.Fatal("want error when EndpointURL is not specified")
	}

	c.Endpoint = "url"
	err = c.Cast()
	if err != nil {
		t.Fatalf("got error during Cast: %v", err)
	}
	want := &Config{
		Endpoint: "url",
		PartSize: defaultPartSize,
		Retryer: &Retryer{
			NumMaxRetries: defaultMaxRetries,
		},
	}

	if !c.Equal(want) {
		t.Fatalf("wrong config after Cast, diff=%s", cmp.Diff(*c, *want))
	}
}
