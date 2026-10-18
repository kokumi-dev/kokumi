package images

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestUnresolvedNote(t *testing.T) {
	imgs := []Image{
		{Ref: nginxRef, Digest: "sha256:aa"},
		{Ref: "private/app:1", Error: "unauthorized"},
		{Ref: "gone:2", Error: "not found"},
	}

	assert.Equal(t, "Could not resolve 2 of 3 images: private/app:1: unauthorized; gone:2: not found", UnresolvedNote(imgs, 1024))
	assert.Empty(t, UnresolvedNote(imgs[:1], 1024), "every image resolved")
}

func TestUnresolvedNote_truncates(t *testing.T) {
	const limit = 1024
	imgs := []Image{{Ref: strings.Repeat("ä", limit), Error: "not found"}}

	note := UnresolvedNote(imgs, limit)
	assert.LessOrEqual(t, len(note), limit)
	assert.True(t, utf8.ValidString(note), "cut at a rune boundary")
	assert.True(t, strings.HasSuffix(note, "…"))
}
