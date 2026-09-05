package api

import (
	"reflect"
	"strings"
	"testing"
)

// This function reads bytes that are a secret and returns something that is
// shown on a screen, so what it will and will not return is the whole point.
// Every case below is about the boundary between the label and the hash.

func TestValueSchemesReadsTheLabelAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   []string
	}{
		{
			name:   "the common schemes",
			values: []string{"{SSHA}c29tZXRoaW5n", "{PBKDF2-SHA512}10000$abc$def"},
			want:   []string{"SSHA", "PBKDF2-SHA512"},
		},
		{
			// The reason this feature exists: seeing that a directory is still
			// storing crypt(3) hashes is the point.
			name:   "crypt is reported, because that is worth knowing",
			values: []string{"{CRYPT}abJnggxhB/yWI"},
			want:   []string{"CRYPT"},
		},
		{
			name:   "a value with no scheme reports nothing for that value",
			values: []string{"{SSHA}aaa", "plaintextsomehow"},
			want:   []string{"SSHA", ""},
		},
		{
			// Not nil. An unprefixed userPassword is the cleartext password, so
			// an empty entry is the answer rather than the absence of one — and
			// it is the answer that most needs saying.
			name:   "no value carries a scheme at all",
			values: []string{"plaintext", "alsoplain"},
			want:   []string{"", ""},
		},
		{
			name:   "no values",
			values: nil,
			want:   nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := valueSchemes(toBytes(tc.values))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// The failure that would matter is a parse that runs past the brace and puts
// part of a hash on the screen. These are the shapes that could cause it.
func TestValueSchemesNeverReturnsPartOfTheHash(t *testing.T) {
	secret := "SUPERSECRETHASHMATERIAL"

	for _, tc := range []struct {
		name  string
		value string
	}{
		{"no closing brace at all", "{" + secret},
		{"closing brace far past anything real", "{" + strings.Repeat("A", 40) + "}" + secret},
		{"empty braces", "{}" + secret},
		{"brace not at the start", "x{SSHA}" + secret},
		{"just a brace", "{"},
		{"empty value", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := valueSchemes(toBytes([]string{tc.value}))
			for _, scheme := range got {
				if scheme != "" && strings.Contains(secret, scheme) {
					t.Fatalf("the scheme %q is part of the hash", scheme)
				}
				if len(scheme) > 32 {
					t.Fatalf("the scheme %q is longer than any real one", scheme)
				}
			}
			// And whatever came back, the hash itself must not be in it.
			for _, scheme := range got {
				if strings.Contains(scheme, secret) {
					t.Fatalf("the hash reached the output as %q", scheme)
				}
			}
		})
	}
}

// A brace-delimited run of arbitrary bytes is not a scheme name. Rendering it
// as one would put raw bytes of a secret on the screen, badly encoded.
func TestValueSchemesRefusesInvalidUTF8(t *testing.T) {
	value := []byte{'{', 0xff, 0xfe, '}', 'h', 'a', 's', 'h'}
	got := valueSchemes([][]byte{value})
	if len(got) != 1 || got[0] != "" {
		t.Errorf("got %#v; a scheme that is not valid UTF-8 is not a scheme", got)
	}
}

func toBytes(values []string) [][]byte {
	if values == nil {
		return nil
	}
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		out = append(out, []byte(v))
	}
	return out
}
