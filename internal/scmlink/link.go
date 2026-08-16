package scmlink

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// SourceLink is a clickable link to the source SCM for a given ref.
type SourceLink struct {
	// URL is the absolute link to the tag or commit in the source SCM.
	URL string
	// Label is the short, human-readable ref shown in the UI.
	Label string
}

// Provider turns a normalised repo URL + ref into a SourceLink, or reports it
// does not handle that source by returning ok=false.
type Provider interface {
	// Match reports whether this provider handles the given (already normalised)
	// repo URL.
	Match(repo string) bool

	// Link builds the SourceLink for repo + ref. Called only when Match
	// returned true.
	Link(repo, ref string) SourceLink
}

// providers is the ordered list of supported SCMs. First match wins.
var providers = []Provider{
	githubProvider{},
}

// Build normalises repo, picks the ref (tag preferred, else commitHash), finds
// a provider that handles it, and returns the SourceLink. Returns nil when
// provenance is absent or no provider handles the source.
func Build(repo, tag, commitHash string) *SourceLink {
	ref := tag
	if ref == "" {
		ref = commitHash
	}
	if repo == "" || ref == "" {
		return nil
	}

	for _, p := range providers {
		if p.Match(repo) {
			l := p.Link(repo, ref)
			return &l
		}
	}
	return nil
}

// isCommitSHA reports whether ref is a full 40-char lowercase hex Git commit SHA
func isCommitSHA(ref string) bool {
	return commitSHAPattern.MatchString(ref)
}

var commitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// githubProvider links GitHub repos to /commit/<sha> or /tree/<tag>.
type githubProvider struct{}

func (githubProvider) Match(repo string) bool {
	return strings.Contains(repo, "github.com")
}

func (githubProvider) Link(repo, ref string) SourceLink {
	escaped := url.QueryEscape(ref)
	if isCommitSHA(ref) {
		return SourceLink{
			URL:   fmt.Sprintf("%s/commit/%s", repo, escaped),
			Label: ref[:7],
		}
	}
	return SourceLink{URL: fmt.Sprintf("%s/tree/%s", repo, escaped), Label: ref}
}
