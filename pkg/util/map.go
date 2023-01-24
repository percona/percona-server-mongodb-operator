package util

// MapEqual compares maps for equality
func MapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if b[k] != v {
			return false
		}
	}

	return true
}

// MapCopy makes (shallow) copy of src map
func MapCopy(src map[string]string) map[string]string {
	dst := make(map[string]string)
	for k, v := range src {
		dst[k] = v
	}

	return dst
}

// MapMerge merges maps ms from left to right with overwriting existing keys
func MapMerge(ms ...map[string]string) map[string]string {
	if len(ms) == 0 {
		return make(map[string]string)
	}

	rv := MapCopy(ms[0])
	for _, m := range ms[1:] {
		for k, v := range m {
			rv[k] = v
		}
	}

	return rv
}

// MapFilterByKeys returns a new map that contains keys from the keys array existing in the m map.
func MapFilterByKeys(m map[string]string, keys []string) map[string]string {
	if len(m) == 0 || len(keys) == 0 {
		return nil
	}
	filteredMap := make(map[string]string)
	for _, k := range keys {
		if v, ok := m[k]; ok {
			filteredMap[k] = v
		}
	}
	return filteredMap
}
