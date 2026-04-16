package main

import (
	"testing"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

func TestParseCLINSOption(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		nss, err := parseCLINSOption("a.b")
		if err != nil {
			t.Errorf("expected no error, got: %s", err.Error())
		}
		if len(nss) != 1 || nss[0] != "a.b" {
			t.Errorf(`expected [a.b] result, got: %v`, nss)
		}
	})

	t.Run("wildcard (all namespaces)", func(t *testing.T) {
		cases := []string{
			"",
			" ",
			"*.*",
		}
		for _, ns := range cases {
			nss, err := parseCLINSOption(ns)
			if err != nil {
				t.Errorf("expected no error, got: %s", err.Error())
			}
			if nss != nil {
				t.Errorf("expected nil result, got: %v", nss)
			}
		}
	})

	t.Run("invalid namespaces", func(t *testing.T) {
		cases := []string{
			".",
			"ANY.",
			".ANY",
			"*.ANY",
		}
		for _, ns := range cases {
			_, err := parseCLINSOption(ns)
			if err == nil {
				t.Errorf("expected %s, got: nil", ErrInvalidNamespace.Error())
			}
			if !errors.Is(err, ErrInvalidNamespace) {
				t.Errorf("expected %s, got: %s", ErrInvalidNamespace.Error(), err.Error())
			}
		}
	})

	t.Run("ambiguous namespaces", func(t *testing.T) {
		cases := []string{
			"*.*,a.a",
			"*.*,a.*",
			"a.b,a.*",
		}
		for _, ns := range cases {
			_, err := parseCLINSOption(ns)
			if err == nil {
				t.Errorf("expected %s, got: nil", ErrAmbiguousNamespace.Error())
			}
			if !errors.Is(err, ErrAmbiguousNamespace) {
				t.Errorf("expected %s, got: %s", ErrAmbiguousNamespace.Error(), err.Error())
			}
		}
	})

	t.Run("system databases", func(t *testing.T) {
		cases := []string{
			"admin.ANY",
			"config.ANY",
			"local.ANY",
		}
		for _, ns := range cases {
			_, err := parseCLINSOption(ns)
			if !errors.Is(err, ErrForbiddenDatabase) {
				t.Errorf("%q expected to be forbidden", ns)
			}
		}
	})

	t.Run("system collections", func(t *testing.T) {
		_, err := parseCLINSOption("ANY.system.ANY")
		if !errors.Is(err, ErrForbiddenCollection) {
			t.Error(`"ANY.system.ANY" expected to be forbidden`)
		}
	})
}
