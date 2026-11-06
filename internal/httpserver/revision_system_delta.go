package httpserver

// revisionSystemDelta represents the monetary totals of one engineering
// system in two revisions, preserving the values returned by PostgreSQL.
type revisionSystemDelta struct {
	Code       string
	Before     string
	After      string
	Difference string
}
