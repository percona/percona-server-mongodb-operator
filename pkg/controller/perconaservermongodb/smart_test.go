package perconaservermongodb

import (
	"strconv"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSortPodsByOrdinal(t *testing.T) {
	ordinalPod := func(i int) corev1.Pod {
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "pod-" + strconv.Itoa(i),
				Labels: map[string]string{appsv1.PodIndexLabel: strconv.Itoa(i)},
			},
		}
	}

	testCases := []struct {
		desc        string
		pods        []corev1.Pod
		expectedOrd []string
		less        func(i, j int) bool
	}{
		{
			desc: "ascending",
			pods: []corev1.Pod{
				ordinalPod(0),
				ordinalPod(1),
				ordinalPod(2),
				ordinalPod(3),
				ordinalPod(4),
				ordinalPod(5),
				ordinalPod(6),
				ordinalPod(7),
				ordinalPod(8),
				ordinalPod(9),
				ordinalPod(10),
			},
			expectedOrd: []string{
				"pod-0",
				"pod-1",
				"pod-2",
				"pod-3",
				"pod-4",
				"pod-5",
				"pod-6",
				"pod-7",
				"pod-8",
				"pod-9",
				"pod-10",
			},
			less: func(i, j int) bool {
				return i < j
			},
		},
		{
			desc: "descending",
			pods: []corev1.Pod{
				ordinalPod(0),
				ordinalPod(1),
				ordinalPod(2),
				ordinalPod(3),
				ordinalPod(4),
				ordinalPod(5),
				ordinalPod(6),
				ordinalPod(7),
				ordinalPod(8),
				ordinalPod(9),
				ordinalPod(10),
			},
			expectedOrd: []string{
				"pod-10",
				"pod-9",
				"pod-8",
				"pod-7",
				"pod-6",
				"pod-5",
				"pod-4",
				"pod-3",
				"pod-2",
				"pod-1",
				"pod-0",
			},
			less: func(i, j int) bool {
				return i > j
			},
		},
	}

	for _, tc := range testCases {
		in := tc.pods
		sortPodsByOrdinal(in, tc.less)
		for i := range in {
			if in[i].Name != tc.expectedOrd[i] {
				t.Errorf("%s: expected pod at index %d to be %s, got %s", tc.desc, i, tc.expectedOrd[i], in[i].Name)
			}
		}
	}
}
