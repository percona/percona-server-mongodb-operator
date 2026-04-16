package util_test

import (
	"reflect"
	"testing"

	"github.com/percona/percona-backup-mongodb/pbm/util"
)

func TestSelectedPred(t *testing.T) {
	nss := []string{
		"db0",
		"db0.c0",
		"db0.c1",
		"db1.",
		"db1.c0",
		"db1.c1",
	}
	cases := []struct {
		s []string
		r []string
	}{
		{[]string{"*.*"}, nss},
		{[]string{"db0.*"}, []string{"db0", "db0.c0", "db0.c1"}},
		{[]string{"db1.*"}, []string{"db1.", "db1.c0", "db1.c1"}},
		{[]string{"db1.c1"}, []string{"db1.c1"}},
		{[]string{"db0.c1", "db1.c0"}, []string{"db0.c1", "db1.c0"}},
		{[]string{"db0.c1", "db1.*"}, []string{"db0.c1", "db1.", "db1.c0", "db1.c1"}},
		{[]string{"db0.c2"}, []string{}},
		{[]string{"db2.c0"}, []string{}},
		{[]string{"db2.*"}, []string{}},
	}

	for _, c := range cases {
		s := util.MakeSelectedPred(c.s)
		r := []string{}
		for _, ns := range nss {
			if s(ns) {
				r = append(r, ns)
			}
		}

		if !reflect.DeepEqual(r, c.r) {
			t.Errorf("expected: %v, got: %v", c.r, r)
		}
	}
}

func TestParseNS(t *testing.T) {
	testCases := []struct {
		desc    string
		ns      string
		resDB   string
		resColl string
	}{
		{desc: "wild-card namespace", ns: "*.*", resDB: "", resColl: ""},
		{desc: "explicit db & explicit collection namespace", ns: "D.C", resDB: "D", resColl: "C"},
		{desc: "only db specified", ns: "D", resDB: "D", resColl: ""},
		{desc: "only db specified, collection with wild-card", ns: "D.*", resDB: "D", resColl: ""},
		{desc: "only db specified, collection is empty", ns: "D.", resDB: "D", resColl: ""},
		{desc: "only db specified, collection is whitespace", ns: "D. ", resDB: "D", resColl: " "},
		{desc: "only db with whitespaces specified, collection is whitespace", ns: " D ", resDB: " D ", resColl: ""},
		{desc: "only collection specified", ns: ".C", resDB: "", resColl: "C"},
		{desc: "only collection specified, database with wild-card", ns: "*.C", resDB: "", resColl: "C"},
		{desc: "only collection specified, collection is empty", ns: ".C", resDB: "", resColl: "C"},
		{desc: "only collection specified, db is whitespace", ns: " .C", resDB: " ", resColl: "C"},
		{desc: "only collection with whitespaces specified, database is whitespace", ns: " . C ", resDB: " ", resColl: " C "},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			resDB, resColl := util.ParseNS(tC.ns)
			if resDB != tC.resDB {
				t.Errorf("expected database: %q, got: %q", tC.resDB, resDB)
			}
			if resColl != tC.resColl {
				t.Errorf("expected collection: %q, got: %q", tC.resColl, resColl)
			}
		})
	}
}

func TestContainsColl(t *testing.T) {
	testCases := []struct {
		desc string
		ns   string
		res  bool
	}{
		{desc: "collection is defined", ns: "d.c", res: true},
		{desc: "collection is wild-carded", ns: "d.*", res: false},
		{desc: "namespace is wild-carded", ns: "*.*", res: false},
		{desc: "namespace is empty", ns: "", res: false},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			if res := util.ContainsColl(tC.ns); res != tC.res {
				t.Errorf("expected: %t, got: %t", tC.res, res)
			}
		})
	}
}

func TestContainsSpecifiedColl(t *testing.T) {
	testCases := []struct {
		desc string
		ns   []string
		res  bool
	}{
		{desc: "collection is defined once", ns: []string{"d.*", "d.c"}, res: true},
		{desc: "all collections are wild-carded", ns: []string{"d1.*", "d2.*", "d3.*"}, res: false},
		{desc: "all collections are not specified", ns: []string{"d1.*", "d2", "d3."}, res: false},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			if res := util.ContainsSpecifiedColl(tC.ns); res != tC.res {
				t.Errorf("expected: %t, got: %t", tC.res, res)
			}
		})
	}
}
