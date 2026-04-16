package backup

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/percona/percona-backup-mongodb/pbm/util"
)

func TestMakeConfigsvrDocFilter(t *testing.T) {
	t.Run("selective backup without wildcards", func(t *testing.T) {
		testCases := []struct {
			desc     string
			bcpNS    []string
			docNS    string
			selected bool
		}{
			{
				desc:     "single backup ns, doc selected",
				bcpNS:    []string{"d.c"},
				docNS:    "d.c",
				selected: true,
			},
			{
				desc:     "multiple backup ns, doc selected",
				bcpNS:    []string{"d.c1", "d.c2", "d.c3"},
				docNS:    "d.c2",
				selected: true,
			},
			{
				desc:     "single backup ns, doc not selected",
				bcpNS:    []string{"d.c"},
				docNS:    "x.y",
				selected: false,
			},
			{
				desc:     "single backup ns, doc not selected different coll",
				bcpNS:    []string{"d.c"},
				docNS:    "d.y",
				selected: false,
			},
			{
				desc:     "multiple backup ns, doc not selected",
				bcpNS:    []string{"d.c1", "d.c2", "d.c3"},
				docNS:    "d.c4",
				selected: false,
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				docFilter := makeConfigsvrDocFilter(tC.bcpNS, util.NewUUIDChunkSelector())
				res := docFilter(tC.docNS, bson.Raw{})
				if res != tC.selected {
					t.Errorf("want=%t, got=%t, for backup ns: %s and doc ns: %s", tC.selected, res, tC.bcpNS, tC.docNS)
				}
			})
		}
	})

	t.Run("selective backup with wildcards", func(t *testing.T) {
		testCases := []struct {
			desc     string
			bcpNS    []string
			docNS    string
			selected bool
		}{
			{
				desc:     "single backup ns, doc selected",
				bcpNS:    []string{"d.*"},
				docNS:    "d.c",
				selected: true,
			},
			{
				desc:     "multiple backup ns, doc selected",
				bcpNS:    []string{"d1.*", "d2.*", "d3.*"},
				docNS:    "d2.c2",
				selected: true,
			},
			{
				desc:     "single backup ns, doc not selected",
				bcpNS:    []string{"d.*"},
				docNS:    "x.y",
				selected: false,
			},
			{
				desc:     "multiple backup ns, doc not selected",
				bcpNS:    []string{"d1.*", "d2.*", "d3.*"},
				docNS:    "d4.c4",
				selected: false,
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				docFilter := makeConfigsvrDocFilter(tC.bcpNS, util.NewUUIDChunkSelector())
				res := docFilter(tC.docNS, bson.Raw{})
				if res != tC.selected {
					t.Errorf("want=%t, got=%t, for backup ns: %s and doc ns: %s", tC.selected, res, tC.bcpNS, tC.docNS)
				}
			})
		}
	})
}
