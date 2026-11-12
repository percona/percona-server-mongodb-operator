package init

import "strings"

// replaceImageTag returns image with its tag replaced by tag.
//
// Only a colon after the last slash separates the tag from the repository, so a
// registry port (registry:5000/repo) is kept. A digest is dropped because it would
// keep pointing at the image of the version being replaced.
func replaceImageTag(image, tag string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image = image[:i]
	}
	return image + ":" + tag
}
