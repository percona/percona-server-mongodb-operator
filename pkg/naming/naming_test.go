package naming

import "testing"

func TestMongodContainerName(t *testing.T) {
	tests := map[string]struct {
		component string
		expected  string
	}{
		"mongod":        {component: ComponentMongod, expected: "mongod"},
		"config server": {component: ComponentConfigSrv, expected: "mongod"},
		"hidden":        {component: ComponentHidden, expected: "mongod-hidden"},
		"non-voting":    {component: ComponentNonVoting, expected: "mongod-nv"},
		"arbiter":       {component: ComponentArbiter, expected: "mongod-arbiter"},
		"unknown":       {component: "", expected: "mongod"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := MongodContainerName(tt.component); got != tt.expected {
				t.Errorf("MongodContainerName(%q) = %q, want %q", tt.component, got, tt.expected)
			}
		})
	}
}
