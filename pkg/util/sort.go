package util

import (
	"sort"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func SortPodsByOrdinalAsc(pods []corev1.Pod) {
	sortPodsByOrdinal(pods, func(i, j int) bool { return i < j })
}

func SortPodsByOrdinalDesc(pods []corev1.Pod) {
	sortPodsByOrdinal(pods, func(i, j int) bool { return i > j })
}

func sortPodsByOrdinal(pods []corev1.Pod, less func(i, j int) bool) {
	sort.Slice(pods, func(i, j int) bool {
		oi, oj := podOrdinal(&pods[i]), podOrdinal(&pods[j])
		return less(oi, oj)
	})
}

func podOrdinal(pod *corev1.Pod) int {
	val, ok := pod.GetLabels()[appsv1.PodIndexLabel]
	if !ok {
		return -1
	}
	ordinal, err := strconv.Atoi(val)
	if err != nil || ordinal < 0 {
		return -1
	}
	return ordinal
}
