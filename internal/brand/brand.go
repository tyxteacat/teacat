// Package brand holds the project's public identity. Keeping it in one place
// makes the name, author and links consistent across the CLI, API and HTTP
// clients (including the User-Agent sent to model registries).
package brand

const (
	// Name is the project name.
	Name = "TeaCat"
	// Author is the creator/maintainer.
	Author = "Tyxen"
	// Version is the release version.
	Version = "0.1.0"
	// Repo is the canonical source repository.
	Repo = "https://github.com/tyxteacat/teacat"
	// Tagline describes the project in one line.
	Tagline = "Local LLM runtime by Tyxen"
)

// UserAgent returns the HTTP User-Agent used when talking to registries.
func UserAgent() string {
	return Name + "/" + Version + " (+" + Repo + ")"
}
