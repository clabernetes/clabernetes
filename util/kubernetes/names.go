package kubernetes

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
)

var (
	validDNSLabelConventionPatternsObj    *validDNSLabelConventionPatterns //nolint:gochecknoglobals
	validNSLabelConventionPatternsObjOnce sync.Once                        //nolint:gochecknoglobals
)

const (
	// NameMaxLen is the maximum length for a kubernetes name.
	NameMaxLen = 63
)

type validDNSLabelConventionPatterns struct {
	invalidChars       *regexp.Regexp
	startsWithNonAlpha *regexp.Regexp
	endsWithNonAlpha   *regexp.Regexp
}

func getDNSLabelConventionPatterns() *validDNSLabelConventionPatterns {
	validNSLabelConventionPatternsObjOnce.Do(func() {
		validDNSLabelConventionPatternsObj = &validDNSLabelConventionPatterns{
			invalidChars:       regexp.MustCompile(`[^a-z0-9\-]`),
			startsWithNonAlpha: regexp.MustCompile(`^[^a-z]`),
			endsWithNonAlpha:   regexp.MustCompile(`[^a-z]$`),
		}
	})

	return validDNSLabelConventionPatternsObj
}

// SafeConcatNameKubernetes concats all provided strings into a string joined by "-" - if the final
// string is greater than 63 characters, the string will be shortened, and a hash will be used at
// the end of the string to keep it unique, but safely within allowed lengths.
func SafeConcatNameKubernetes(name ...string) string {
	return SafeConcatNameMax(name, NameMaxLen)
}

// SafeConcatNameMax concats all provided strings into a string joined by "-" - if the final string
// is greater than max characters, the string will be shortened, and a hash will be used at the end
// of the string to keep it unique, but safely within allowed lengths.
func SafeConcatNameMax(name []string, maxLen int) string {
	finalName := strings.Join(name, "-")

	if len(finalName) <= maxLen {
		return finalName
	}

	digest := sha256.Sum256([]byte(finalName))

	return finalName[0:maxLen-8] + "-" + hex.EncodeToString(digest[0:])[0:7]
}

// EnforceDNSLabelConvention attempts to enforce the RFC 1123 label name requirements on s.
func EnforceDNSLabelConvention(s string) string {
	p := getDNSLabelConventionPatterns()

	s = strings.ToLower(s)
	s = p.invalidChars.ReplaceAllString(s, "-")
	s = p.startsWithNonAlpha.ReplaceAllString(s, "z")
	s = p.endsWithNonAlpha.ReplaceAllString(s, "z")

	return s
}
