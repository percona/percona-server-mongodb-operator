package storage

import "testing"

func TestCreateNextPart(t *testing.T) {
	t.Run("next part for base file", func(t *testing.T) {
		fname := "file_name"
		want := "file_name.pbmpart.1"

		got, err := createNextPart(fname)
		if err != nil {
			t.Errorf("got error: %v", err)
		}
		if got != want {
			t.Errorf("want=%s, got=%s", want, got)
		}
	})

	t.Run("next part for the first part", func(t *testing.T) {
		fname := "file_name.pbmpart.1"
		want := "file_name.pbmpart.2"

		got, err := createNextPart(fname)
		if err != nil {
			t.Errorf("got error: %v", err)
		}
		if got != want {
			t.Errorf("want=%s, got=%s", want, got)
		}
	})

	t.Run("next part for index 9", func(t *testing.T) {
		fname := "file_name.pbmpart.9"
		want := "file_name.pbmpart.10"

		got, err := createNextPart(fname)
		if err != nil {
			t.Errorf("got error: %v", err)
		}
		if got != want {
			t.Errorf("want=%s, got=%s", want, got)
		}
	})

	t.Run("error while parsing index", func(t *testing.T) {
		fname := "file_name.pbmpart.X"

		got, err := createNextPart(fname)
		if err == nil {
			t.Error("want error, get nil")
		}
		if got != "" {
			t.Error("file name should be empty string")
		}
	})

	t.Run("token exists, base part doesn't", func(t *testing.T) {
		fname := ".pbmpart.5"
		want := ".pbmpart.6"

		got, err := createNextPart(fname)
		if err != nil {
			t.Errorf("got error: %v", err)
		}
		if got != want {
			t.Errorf("want=%s, got=%s", want, got)
		}
	})

	t.Run("token exists, index doesn't", func(t *testing.T) {
		fname := "file_name.pbmpart."

		got, err := createNextPart(fname)
		if err == nil {
			t.Error("want error, get nil")
		}
		if got != "" {
			t.Error("file name should be empty string")
		}
	})
}

func TestGetPartIndex(t *testing.T) {
	t.Run("index for base file", func(t *testing.T) {
		fname := "file_name"
		want := 0

		got, err := GetPartIndex(fname)
		if err != nil {
			t.Errorf("got error: %v", err)
		}
		if got != want {
			t.Errorf("want=%d, got=%d", want, got)
		}
	})

	t.Run("index for file which has it", func(t *testing.T) {
		fname := "file_name.pbmpart.15"
		want := 15

		got, err := GetPartIndex(fname)
		if err != nil {
			t.Errorf("got error: %v", err)
		}
		if got != want {
			t.Errorf("want=%d, got=%d", want, got)
		}
	})

	t.Run("error while parsing index", func(t *testing.T) {
		fname := "file_name.pbmpart.X"

		got, err := GetPartIndex(fname)
		if err == nil {
			t.Error("want error, get nil")
		}
		if got != 0 {
			t.Error("index should be 0")
		}
	})

	t.Run("token exists, base part doesn't", func(t *testing.T) {
		fname := ".pbmpart.5"
		want := 5

		got, err := GetPartIndex(fname)
		if err != nil {
			t.Errorf("got error: %v", err)
		}
		if got != want {
			t.Errorf("want=%d, got=%d", want, got)
		}
	})

	t.Run("token exists, index doesn't", func(t *testing.T) {
		fname := "file_name.pbmpart."

		got, err := GetPartIndex(fname)
		if err == nil {
			t.Error("want error, get nil")
		}
		if got != 0 {
			t.Error("index should be 0")
		}
	})
}

// TestGetBasePart tests GetBasePart function.
func TestGetBasePart(t *testing.T) {
	tests := []struct {
		name  string
		fname string
		want  string
	}{
		{
			name:  "only base part",
			fname: "file_name",
			want:  "file_name",
		},
		{
			name:  "base part with pbmpart token and index",
			fname: "file_name.pbmpart.15",
			want:  "file_name",
		},
		{
			name:  "base part with pbmpart token, without index",
			fname: "file_name.pbmpart.",
			want:  "file_name.pbmpart.",
		},
		{
			name:  "base part with pbmpart token, invalid index",
			fname: "file_name.pbmpart.23xx",
			want:  "file_name.pbmpart.23xx",
		},
		{
			name:  "pbmpart token exists, base part doesn't",
			fname: ".pbmpart.5",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetBasePart(tt.fname)
			if got != tt.want {
				t.Errorf("want=%s, got=%s", tt.want, got)
			}
		})
	}
}
