package util

type RSMapFunc func(string) string

func identity(a string) string { return a }

func MakeRSMapFunc(m map[string]string) RSMapFunc {
	if len(m) == 0 {
		return identity
	}

	return func(s string) string {
		if a, ok := m[s]; ok {
			return a
		}

		return s
	}
}

func MakeReverseRSMapFunc(m map[string]string) RSMapFunc {
	if len(m) == 0 {
		return identity
	}

	return MakeRSMapFunc(swapSSMap(m))
}

func swapSSMap(m map[string]string) map[string]string {
	rv := make(map[string]string, len(m))

	for k, v := range m {
		rv[v] = k
	}

	return rv
}
