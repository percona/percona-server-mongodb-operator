package restore

import (
	"reflect"
	"testing"

	"github.com/percona/percona-backup-mongodb/pbm/snapshot"
)

func TestResolveNamespace(t *testing.T) {
	checkNS := func(t testing.TB, got, want []string) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got=%v, want=%v", got, want)
		}
	}

	t.Run("without users&roles", func(t *testing.T) {
		testCases := []struct {
			desc       string
			nssBackup  []string
			nssRestore []string
			want       []string
		}{
			{
				desc:       "full backup -> full restore",
				nssBackup:  []string{},
				nssRestore: []string{},
				want:       []string{},
			},
			{
				desc:       "full backup -> selective restore",
				nssBackup:  []string{},
				nssRestore: []string{"d.c"},
				want:       []string{"d.c"},
			},
			{
				desc:       "selective backup -> full restore",
				nssBackup:  []string{"d.c"},
				nssRestore: []string{},
				want:       []string{"d.c"},
			},
			{
				desc:       "selective backup -> selective restore",
				nssBackup:  []string{"d.*"},
				nssRestore: []string{"d.c"},
				want:       []string{"d.c"},
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				got := resolveNamespace(tC.nssBackup, tC.nssRestore, snapshot.CloneNS{}, false)
				checkNS(t, got, tC.want)
			})
		}
	})

	t.Run("with users&roles", func(t *testing.T) {
		testCases := []struct {
			desc       string
			nssBackup  []string
			nssRestore []string
			want       []string
		}{
			{
				desc:       "full backup -> full restore",
				nssBackup:  []string{},
				nssRestore: []string{},
				want:       []string{},
			},
			{
				desc:       "full backup -> selective restore",
				nssBackup:  []string{},
				nssRestore: []string{"d.c"},
				want:       []string{"d.c", "admin.pbmRUsers", "admin.pbmRRoles"},
			},
			{
				desc:       "selective backup -> full restore",
				nssBackup:  []string{"d.c"},
				nssRestore: []string{},
				want:       []string{"d.c"},
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				got := resolveNamespace(tC.nssBackup, tC.nssRestore, snapshot.CloneNS{}, true)
				checkNS(t, got, tC.want)
			})
		}
	})

	t.Run("cloning collection", func(t *testing.T) {
		testCases := []struct {
			desc         string
			nssBackup    []string
			nsFrom       string
			nsTo         string
			userAndRoles bool
			want         []string
		}{
			{
				desc:      "full backup -> cloning collection",
				nssBackup: []string{},
				nsFrom:    "d.from",
				nsTo:      "d.to",
				want:      []string{"d.from"},
			},
			{
				desc:      "selective backup -> cloning collection",
				nssBackup: []string{"d.*", "a.b"},
				nsFrom:    "d.from",
				nsTo:      "d.to",
				want:      []string{"d.from"},
			},
			{
				desc:         "full backup -> cloning collection, users&roles are ignored",
				nssBackup:    []string{},
				nsFrom:       "d.from",
				nsTo:         "d.to",
				userAndRoles: true,
				want:         []string{"d.from"},
			},
			{
				desc:         "selective backup -> cloning collection, users&roles are ignored",
				nssBackup:    []string{"d.*", "a.b"},
				nsFrom:       "d.from",
				nsTo:         "d.to",
				userAndRoles: true,
				want:         []string{"d.from"},
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				got := resolveNamespace(
					tC.nssBackup,
					[]string{},
					snapshot.CloneNS{FromNS: tC.nsFrom, ToNS: tC.nsTo},
					tC.userAndRoles)
				checkNS(t, got, tC.want)
			})
		}
	})
}

func TestShouldRestoreUsersAndRoles(t *testing.T) {
	checkOpt := func(t testing.TB, got, want restoreUsersAndRolesOption) {
		t.Helper()
		if got != want {
			t.Errorf("invalid restoreUsersAndRolesOption got=%t, want=%t", got, want)
		}
	}

	t.Run("without users&roles", func(t *testing.T) {
		testCases := []struct {
			desc       string
			nssBackup  []string
			nssRestore []string
			nsFrom     string
			nsTo       string
			wantOpt    restoreUsersAndRolesOption
		}{
			{
				desc:       "full backup -> full restore",
				nssBackup:  []string{},
				nssRestore: []string{},
				wantOpt:    true,
			},
			{
				desc:       "full backup -> selective restore",
				nssBackup:  []string{},
				nssRestore: []string{"d.c"},
				wantOpt:    false,
			},
			{
				desc:       "selective backup -> full restore",
				nssBackup:  []string{"d.c"},
				nssRestore: []string{},
				wantOpt:    false,
			},
			{
				desc:       "full backup -> cloning collection",
				nssBackup:  []string{},
				nssRestore: []string{},
				nsFrom:     "d.from",
				nsTo:       "d.to",
				wantOpt:    false,
			},
			{
				desc:       "selective backup -> cloning collection",
				nssBackup:  []string{"d.*"},
				nssRestore: []string{},
				nsFrom:     "d.from",
				nsTo:       "d.to",
				wantOpt:    false,
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				gotOpt := shouldRestoreUsersAndRoles(
					tC.nssBackup,
					tC.nssRestore,
					snapshot.CloneNS{FromNS: tC.nsFrom, ToNS: tC.nsTo},
					false)
				checkOpt(t, gotOpt, tC.wantOpt)
			})
		}
	})

	t.Run("with users&roles", func(t *testing.T) {
		testCases := []struct {
			desc       string
			nssBackup  []string
			nssRestore []string
			wantOpt    restoreUsersAndRolesOption
		}{
			{
				desc:       "full backup -> full restore",
				nssBackup:  []string{},
				nssRestore: []string{},
				wantOpt:    true,
			},
			{
				desc:       "full backup -> selective restore",
				nssBackup:  []string{},
				nssRestore: []string{"d.c"},
				wantOpt:    true,
			},
			{
				desc:       "selective backup -> full restore",
				nssBackup:  []string{"d.c"},
				nssRestore: []string{},
				wantOpt:    false,
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				gotOpt := shouldRestoreUsersAndRoles(tC.nssBackup, tC.nssRestore, snapshot.CloneNS{}, true)
				checkOpt(t, gotOpt, tC.wantOpt)
			})
		}
	})
}
