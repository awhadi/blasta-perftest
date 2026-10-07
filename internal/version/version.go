// Package version holds BLASTA's version: the one place to change when releasing.
// It follows semantic versioning (MAJOR.MINOR.PATCH): a fix bumps PATCH, a new
// feature bumps MINOR, a breaking change bumps MAJOR. Every release also gets an
// entry in CHANGELOG.md and a git tag v<version>.
package version

// Version is shown in the page (bottom right, when signed in), by `blasta version`
// and in /api/health.
const Version = "2.14.3"

// UserAgent is how BLASTA introduces itself to the systems it tests ("BLASTA/2.6.1") when
// the job does not set its own User-Agent, so the people running them can tell its traffic.
func UserAgent() string { return "BLASTA/" + Version }
