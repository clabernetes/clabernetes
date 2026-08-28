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

const (
	// nameDigestLen is the number of digest characters appended to a name that had to be
	// truncated to fit NameMaxLen.
	nameDigestLen = 7
	// leadingLetterPrefix prefixes a name that would otherwise start with a digit or a dash,
	// which a DNS-1035 label cannot do.
	leadingLetterPrefix = "clab-"
)

// nonNameChars matches every run of characters a DNS label cannot carry, once the name has been
// lower-cased.
var nonNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

// SanitizeName maps a containerlab name onto the DNS-1035 label Kubernetes can name an object
// with: lower case, made up of a-z, 0-9 and '-', starting with a letter and at most 63 characters
// long. A large share of public labs name their nodes R1/PE_1, and Kubernetes cannot carry those
// names as they are.
//
// Unlike EnforceDNSLabelConvention this never rewrites a character a DNS label can carry, so a
// name Kubernetes already accepts maps onto itself and the mapping is idempotent. The result is
// empty only for a name holding nothing a Kubernetes name can be built from.
func SanitizeName(name string) string {
	sanitized := strings.Trim(nonNameChars.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if sanitized == "" {
		return ""
	}

	if sanitized[0] < 'a' || sanitized[0] > 'z' {
		sanitized = leadingLetterPrefix + sanitized
	}

	if len(sanitized) > NameMaxLen {
		digest := sha256.Sum256([]byte(name))
		sanitized = strings.TrimRight(
			sanitized[:NameMaxLen-nameDigestLen-1],
			"-",
		) + "-" + hex.EncodeToString(digest[:])[:nameDigestLen]
	}

	return sanitized
}
