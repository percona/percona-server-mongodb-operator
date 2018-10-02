package config

// Replica Set tags: https://docs.mongodb.com/manual/tutorial/configure-replica-set-tag-sets/#add-tag-sets-to-a-replica-set
type ReplsetTags map[string]string

// HasTag returns a boolean reflecting the existenct of a key in replica set tags
func (rt ReplsetTags) HasKey(key string) bool {
	if _, ok := rt[key]; ok {
		return true
	}
	return false
}

// HasMatch returns a boolean reflecting the existence of an exact key/value pair in replica set tags
func (rt ReplsetTags) HasMatch(key, val string) bool {
	if rt.HasKey(key) {
		return rt[key] == val
	}
	return false
}

// GetTagValue returns a string reflecting the value of a replica set tag key
func (rt ReplsetTags) Get(key string) string {
	if rt.HasKey(key) {
		return rt[key]
	}
	return ""
}

// AddTag adds a key/value to the replica set tags
func (rt ReplsetTags) Add(key, val string) {
	rt[key] = val
}
