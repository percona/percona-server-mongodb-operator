package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
