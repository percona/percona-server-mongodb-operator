package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

func TestConditionsEqual(t *testing.T) {
	tests := map[string]struct {
		a        []metav1.Condition
		b        []metav1.Condition
		expected bool
	}{
		"both empty": {
			a:        nil,
			b:        nil,
			expected: true,
		},
		"both empty slices": {
			a:        []metav1.Condition{},
			b:        []metav1.Condition{},
			expected: true,
		},
		"nil vs empty slice": {
			a:        nil,
			b:        []metav1.Condition{},
			expected: true,
		},
		"equal single condition": {
			a: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
			b: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
			expected: true,
		},
		"different lengths": {
			a: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
			b:        nil,
			expected: false,
		},
		"same type different status": {
			a: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
			b: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionFalse},
			},
			expected: false,
		},
		"different type same status": {
			a: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
			b: []metav1.Condition{
				{Type: "Progressing", Status: metav1.ConditionTrue},
			},
			expected: false,
		},
		"same conditions different order": {
			a: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Progressing", Status: metav1.ConditionFalse},
			},
			b: []metav1.Condition{
				{Type: "Progressing", Status: metav1.ConditionFalse},
				{Type: "Ready", Status: metav1.ConditionTrue},
			},
			expected: true,
		},
		"same type and status but different reason": {
			a: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AllGood"},
			},
			b: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Recovered"},
			},
			expected: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a := &PerconaServerMongoDBRestoreStatus{Conditions: tc.a}
			b := &PerconaServerMongoDBRestoreStatus{Conditions: tc.b}

			assert.Equal(t, tc.expected, a.ConditionsEqual(b))
		})
	}
}

func TestIsCloningNamespace(t *testing.T) {
	tests := map[string]struct {
		selective *SelectiveRestoreOpts
		expected  bool
	}{
		"nil selective": {
			selective: nil,
			expected:  false,
		},
		"empty selective": {
			selective: &SelectiveRestoreOpts{},
			expected:  false,
		},
		"namespaceFrom set": {
			selective: &SelectiveRestoreOpts{NamespaceFrom: "db1.coll1"},
			expected:  true,
		},
		"namespaceFrom and namespaceTo set": {
			selective: &SelectiveRestoreOpts{NamespaceFrom: "db1.coll1", NamespaceTo: "db2.coll2"},
			expected:  true,
		},
		"only namespaceTo set": {
			selective: &SelectiveRestoreOpts{NamespaceTo: "db2.coll2"},
			expected:  false,
		},
		"selective with namespaces but no namespaceFrom": {
			selective: &SelectiveRestoreOpts{Namespaces: []string{"db1.coll1"}},
			expected:  false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := &PerconaServerMongoDBRestore{
				Spec: PerconaServerMongoDBRestoreSpec{
					Selective: tc.selective,
				},
			}
			assert.Equal(t, tc.expected, r.IsCloningNamespace())
		})
	}
}

func TestCheckFieldsCloningNamespace(t *testing.T) {
	tests := map[string]struct {
		backupType defs.BackupType
		selective  *SelectiveRestoreOpts
		expectErr  string
	}{
		"cloning namespace with logical backup": {
			backupType: defs.LogicalBackup,
			selective:  &SelectiveRestoreOpts{NamespaceFrom: "db1.coll1", NamespaceTo: "db2.coll2"},
		},
		"cloning namespace with physical backup": {
			backupType: defs.PhysicalBackup,
			selective:  &SelectiveRestoreOpts{NamespaceFrom: "db1.coll1", NamespaceTo: "db2.coll2"},
			expectErr:  "nsFrom and nsTo are only available for logical backups",
		},
		"cloning namespace with incremental backup": {
			backupType: defs.IncrementalBackup,
			selective:  &SelectiveRestoreOpts{NamespaceFrom: "db1.coll1", NamespaceTo: "db2.coll2"},
			expectErr:  "nsFrom and nsTo are only available for logical backups",
		},
		"cloning namespace with external backup": {
			backupType: defs.ExternalBackup,
			selective:  &SelectiveRestoreOpts{NamespaceFrom: "db1.coll1", NamespaceTo: "db2.coll2"},
			expectErr:  "nsFrom and nsTo are only available for logical backups",
		},
		"no cloning with logical backup": {
			backupType: defs.LogicalBackup,
			selective:  nil,
		},
		"no cloning with physical backup": {
			backupType: defs.PhysicalBackup,
			selective:  nil,
		},
		"selective without nsFrom is not cloning with physical backup": {
			backupType: defs.PhysicalBackup,
			selective:  &SelectiveRestoreOpts{Namespaces: []string{"db1.coll1"}},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := &PerconaServerMongoDBRestore{
				Spec: PerconaServerMongoDBRestoreSpec{
					ClusterName: "some-cluster",
					BackupName:  "some-backup",
					Selective:   tc.selective,
				},
			}
			err := r.CheckFields(tc.backupType)
			if tc.expectErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.EqualError(t, err, tc.expectErr)
		})
	}
}
