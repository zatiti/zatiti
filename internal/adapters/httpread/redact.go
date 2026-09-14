package httpread

// truncateText bounds s to at most n bytes, used to keep diagnostic text
// (transport error messages) within the schema's field limits.
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
