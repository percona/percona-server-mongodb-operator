package main

import (
	"strings"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

func parseRSNamesMapping(s string) (map[string]string, error) {
	rv := make(map[string]string)

	if s == "" {
		return rv, nil
	}

	checkSrc, checkDst := makeStrSet(), makeStrSet()

	for _, a := range strings.Split(s, ",") {
		m := strings.Split(a, "=")
		if len(m) != 2 {
			return nil, errors.Errorf("malformatted: %q", a)
		}

		src, dst := m[0], m[1]

		if checkSrc(src) {
			return nil, errors.Errorf("source %v is duplicated", src)
		}
		if checkDst(dst) {
			return nil, errors.Errorf("target %v is duplicated", dst)
		}

		rv[dst] = src
	}

	return rv, nil
}

func makeStrSet() func(string) bool {
	m := make(map[string]struct{})

	return func(s string) bool {
		if _, ok := m[s]; ok {
			return true
		}

		m[s] = struct{}{}
		return false
	}
}

var (
	ErrInvalidNamespace    = errors.New("invalid namespace")
	ErrForbiddenDatabase   = errors.New(`"admin", "config", "local" databases are not allowed`)
	ErrForbiddenCollection = errors.New(`"system.*" collections are not allowed`)
	ErrAmbiguousNamespace  = errors.New("ambiguous namespace")
)

func parseCLINSOption(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*.*" {
		return nil, nil
	}

	m := make(map[string]map[string]struct{})
	for _, ns := range strings.Split(s, ",") {
		db, coll, ok := strings.Cut(strings.TrimSpace(ns), ".")
		if !ok {
			return nil, errors.Wrap(ErrInvalidNamespace, ns)
		}
		if db == "" || coll == "" || (db == "*" && coll != "*") {
			return nil, errors.Wrap(ErrInvalidNamespace, ns)
		}
		if db == "admin" || db == "config" || db == "local" {
			return nil, ErrForbiddenDatabase
		}
		if strings.HasPrefix(coll, "system.") {
			return nil, ErrForbiddenCollection
		}

		if _, ok := m[db]; !ok {
			m[db] = make(map[string]struct{})
		}
		m[db][coll] = struct{}{}
	}

	if _, ok := m["*"]; ok && len(m) != 1 {
		return nil, errors.Wrap(ErrAmbiguousNamespace,
			"cannot use * with other databases")
	}

	rv := []string{}
	for db, colls := range m {
		if _, ok := colls["*"]; ok && len(colls) != 1 {
			return nil, errors.Wrapf(ErrAmbiguousNamespace,
				"cannot use * with other collections in %q database", db)
		}

		for coll := range colls {
			rv = append(rv, db+"."+coll)
		}
	}

	return rv, nil
}
