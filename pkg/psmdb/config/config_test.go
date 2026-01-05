package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
)

func TestVolumeProjection(t *testing.T) {
	objectName := "test-object"
	localObj := corev1.LocalObjectReference{
		Name: objectName,
	}
	testCases := []struct {
		description  string
		volumeSource VolumeSourceType
		expected     corev1.VolumeProjection
	}{
		{
			description:  "VolumeSourceConfigMap",
			volumeSource: VolumeSourceConfigMap,
			expected: corev1.VolumeProjection{
				ConfigMap: &corev1.ConfigMapProjection{
					LocalObjectReference: localObj,
					Optional:             pointer.Bool(true),
				},
			},
		},
		{
			description:  "VolumeSourceSecret",
			volumeSource: VolumeSourceSecret,
			expected: corev1.VolumeProjection{
				Secret: &corev1.SecretProjection{
					LocalObjectReference: localObj,
					Optional:             pointer.Bool(true),
				},
			},
		},
		{
			description:  "VolumeSourceNone",
			volumeSource: VolumeSourceNone,
			expected:     corev1.VolumeProjection{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.volumeSource.VolumeProjection(objectName))
		})
	}
}
