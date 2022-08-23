package pbm

type RSMapFunc func(string) string

func MakeRSMapFunc(m map[string]string) RSMapFunc {
	if m == nil {
		return func(s string) string { return s }
	}

	return func(s string) string {
		if a, ok := m[s]; ok {
			return a
		}

		return s
	}
}

func MakeReverseRSMapFunc(m map[string]string) RSMapFunc {
	return MakeRSMapFunc(swapSSMap(m))
}

func swapSSMap(m map[string]string) map[string]string {
	rv := make(map[string]string, len(m))

	for k, v := range m {
		rv[v] = k
	}

	return rv
}
