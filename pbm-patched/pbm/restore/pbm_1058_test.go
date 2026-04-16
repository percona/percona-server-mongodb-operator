package restore

import "testing"

func TestDBpathSearch(t *testing.T) {
	cases := []struct {
		name   string
		is     bool
		dbpath string
	}{
		{"a", false, ""},
		{"/a", true, ""},
		{"/path/a", true, ""},
		{"/data/db/a", true, ""},
		{"/data/journal/a", true, "/data/"},
		{"/data/db/journal/a", true, "/data/db/"},
		{"/data/journal/journal/a", true, "/data/journal/"},
		{"/data/db/journal/journal/a", true, "/data/db/journal/"},
		{"/data/journal/db/journal/a", true, "/data/journal/db/"},
		{"/data/journal/db/journala", true, ""},
		{"/data/journal/db/journal", true, ""},
		{"/journal/a", true, "/"},
		{"journal/a", false, ""},
		{"collection/a", false, ""},
		{"collection/a", false, ""},
	}

	for _, c := range cases {
		is, path := findDBpath(c.name)
		if is != c.is || path != c.dbpath {
			t.Errorf("filename %s, expected: `%v` `%s`, got: `%v` `%s`", c.name, c.is, c.dbpath, is, path)
		}
	}
}
