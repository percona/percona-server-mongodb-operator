package init

import "testing"

func TestReplaceImageTag(t *testing.T) {
	tests := map[string]struct {
		image string
		tag   string
		want  string
	}{
		"repository with tag": {
			image: "percona/percona-server-mongodb-operator:1.23.1",
			tag:   "1.22.0",
			want:  "percona/percona-server-mongodb-operator:1.22.0",
		},
		"repository without tag": {
			image: "percona/percona-server-mongodb-operator",
			tag:   "1.22.0",
			want:  "percona/percona-server-mongodb-operator:1.22.0",
		},
		"registry with port keeps port and repository": {
			image: "registry.example.invalid:5000/percona/percona-server-mongodb-operator:1.23.1",
			tag:   "1.22.0",
			want:  "registry.example.invalid:5000/percona/percona-server-mongodb-operator:1.22.0",
		},
		"registry with port and no tag": {
			image: "localhost:5000/percona/percona-server-mongodb-operator",
			tag:   "1.22.0",
			want:  "localhost:5000/percona/percona-server-mongodb-operator:1.22.0",
		},
		"IPv6 registry with port": {
			image: "[::1]:5000/percona/percona-server-mongodb-operator:1.23.1",
			tag:   "1.22.0",
			want:  "[::1]:5000/percona/percona-server-mongodb-operator:1.22.0",
		},
		"name only with tag": {
			image: "operator:1.23.1",
			tag:   "1.22.0",
			want:  "operator:1.22.0",
		},
		"name only without tag": {
			image: "operator",
			tag:   "1.22.0",
			want:  "operator:1.22.0",
		},
		"digest only": {
			image: "percona/percona-server-mongodb-operator@sha256:0123456789abcdef",
			tag:   "1.22.0",
			want:  "percona/percona-server-mongodb-operator:1.22.0",
		},
		"tag and digest": {
			image: "percona/percona-server-mongodb-operator:1.23.1@sha256:0123456789abcdef",
			tag:   "1.22.0",
			want:  "percona/percona-server-mongodb-operator:1.22.0",
		},
		"registry with port, tag and digest": {
			image: "registry.example.invalid:5000/percona/percona-server-mongodb-operator:1.23.1@sha256:0123456789abcdef",
			tag:   "1.22.0",
			want:  "registry.example.invalid:5000/percona/percona-server-mongodb-operator:1.22.0",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := replaceImageTag(tt.image, tt.tag); got != tt.want {
				t.Errorf("replaceImageTag(%q, %q) = %q, want %q", tt.image, tt.tag, got, tt.want)
			}
		})
	}
}
