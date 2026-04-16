package version

import (
	"strings"
	"testing"
)

func TestCompatibility(t *testing.T) {
	breaking := []string{
		"1.5.0",
		"1.9.0",
	}
	cases := []struct {
		v1         string
		v2         string
		compatible bool
	}{
		{
			"v1.5.5",
			"1.4.1",
			false,
		},
		{
			"1.4.0",
			"v1.5.4",
			false,
		},
		{
			"1.3.0",
			"1.4.0",
			true,
		},
		{
			"1.4.3",
			"1.3.0",
			true,
		},
		{
			"1.5.0",
			"1.4.2",
			false,
		},
		{
			"1.5.0",
			"1.6.0",
			true,
		},
		{
			"1.8.5",
			"1.5.0",
			true,
		},
		{
			"1.5.5",
			"1.9.0",
			false,
		},
		{
			"1.9.0",
			"1.5.5",
			false,
		},
		{
			"1.4.5",
			"1.9.0",
			false,
		},
		{
			"1.14.5",
			"1.9.0",
			true,
		},
		{
			"1.4.5",
			"1.14.0",
			false,
		},
		{
			"1.9.0",
			"1.10.0",
			true,
		},
		{
			"1.9.0",
			"2.0.0",
			true,
		},
		{
			"2.0.0",
			"3.0.0",
			true,
		},
	}

	for _, test := range cases {
		c := compatible(test.v1, test.v2, breaking)
		if c != test.compatible {
			t.Errorf("compatibility of %s & %s should be %v, got %v", test.v1, test.v2, test.compatible, c)
		}
	}
}

func TestHasPhysicalFilesMetadata(t *testing.T) {
	cases := map[string]bool{
		"":           false,
		"v2.3.2":     false,
		"v2.4.0":     false,
		"v2.4.1-dev": true,
		"v2.4.1":     true,
		"v2.4.2":     true,
		"v2.5.0":     true,
		"v3.0.0":     true,
	}

	for ver, expect := range cases {
		got := HasFilelistFile(ver)
		if expect != got {
			t.Errorf("%q - expected %v, got %v", ver, expect, got)
		}
	}
}

func TestPBMSupport(t *testing.T) {
	cases := []struct {
		name     string
		ver      []int
		wantErr  bool
		contains string
	}{
		{name: "supported 7.0.x", ver: []int{7, 0, 1}, wantErr: false},
		{name: "supported 8.0.x", ver: []int{8, 0, 1}, wantErr: false},

		{
			name: "too old 4.4.18", ver: []int{4, 4, 18},
			wantErr: true, contains: "upgrade your MongoDB",
		},
		{
			name: "too old 5.0.0", ver: []int{5, 0, 0},
			wantErr: true, contains: "upgrade your MongoDB",
		},
		{
			name: "too old 5.0.x", ver: []int{5, 0, 14},
			wantErr: true, contains: "upgrade your MongoDB",
		},
		{
			name: "too old 6.0.x", ver: []int{6, 0, 5},
			wantErr: true, contains: "upgrade your MongoDB",
		},
		{
			name: "too old 6.1.x", ver: []int{6, 1, 0},
			wantErr: true, contains: "upgrade your MongoDB",
		},
		{
			name: "unsupported minor 7.2.3", ver: []int{7, 2, 3},
			wantErr: true, contains: "does not support minor versions of MongoDB",
		},
		{
			name: "unsupported minor 8.3.0", ver: []int{8, 3, 0},
			wantErr: true, contains: "does not support minor versions of MongoDB",
		},
		{
			name: "newer major 9.0.0", ver: []int{9, 0, 0},
			wantErr: true, contains: "upgrade your PBM package",
		},
		{
			name: "incomplete version array", ver: []int{7},
			wantErr: true, contains: "incomplete versionArray",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := FeatureSupport(MongoVersion{Version: tc.ver}).PBMSupport()
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error presence: err=%v wantErr=%v", err, tc.wantErr)
			}
			if err != nil {
				if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
					t.Fatalf("unexpected error message: %q does not contain %q", err.Error(), tc.contains)
				}
				if tc.contains == "" || !strings.Contains(tc.contains, "incomplete versionArray") {
					if !strings.Contains(err.Error(), "This PBM works with MongoDB and PSMDB v7.0, v8.0") {
						t.Fatalf("error should list supported versions, got: %q", err.Error())
					}
				}
			}
		})
	}
}
