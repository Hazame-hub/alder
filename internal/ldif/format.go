package ldif

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hazame-hub/alder/internal/dn"
)

// Format rewrites an LDIF document of content records in the form Alder's own
// writer produces, keeping its comments. It is what alder project fmt does to
// a project's files, so that a Git diff of one shows what changed and not how
// it was wrapped.
//
// Each line keeps its place: records, attributes and values stay in the order
// they were written, and attribute names keep their spelling. What changes is
// only how a line is written down:
//
//   - a line is unfolded and folded again at DefaultLineWidth;
//   - a value is written plainly when it can be and as base64 when it must be,
//     by NeedsBase64, so a trailing space hidden in a plain value comes out as
//     the base64 that shows it is there;
//   - the space after the colon is exactly one;
//   - a DN is written as the dn package renders it, with the escaping and
//     spacing every DN Alder writes has;
//   - "dn" and "version" are spelled in lower case;
//   - records, and comment blocks between them, are separated by exactly one
//     blank line, a version header gets a blank line of its own, and the
//     file ends with one newline;
//   - line endings are LF.
//
// Comments are kept exactly as written, continuation lines and all: they are
// a person's text, and refolding prose at column 76 would break it mid-word.
//
// A document that does not parse is refused, and so is one holding a
// changetype record: a project describes state, and its files hold content
// records only. Before returning, Format reads its own output back and
// checks that it says exactly what the input said, record by record and
// value by value; if it ever did not, the error says so and the caller
// writes nothing.
func Format(src []byte) ([]byte, error) {
	r := NewReader(bytes.NewReader(src))
	before, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	for _, rec := range before {
		if rec.Change != ChangeNone {
			return nil, syntaxErrorf(rec.Line, "%s is a %s record; only content records are formatted", rec.DN, rec.Change)
		}
	}

	var out bytes.Buffer
	w := NewWriter(&out)
	first := true
	startBlock := func() {
		if !first {
			w.write("\n")
		}
		first = false
	}
	sawAttr := false
	for _, block := range formatBlocks(src) {
		startBlock()
		atStart := true // the next record line is the record's dn
		for i, it := range block {
			if it.comment != nil {
				for _, line := range it.comment {
					w.write(line + "\n")
				}
				continue
			}
			name, value, _, lineErr := splitLine(it.text)
			if lineErr != nil {
				return nil, syntaxErrorf(it.num, "%v", lineErr)
			}
			// The reader takes a version header only as the first line of
			// the first block that has any, comments aside.
			if !sawAttr && equalFoldASCII(name, "version") {
				sawAttr = true
				w.write("version: 1\n")
				if slices.ContainsFunc(block[i+1:], func(it formatItem) bool { return it.comment == nil }) {
					startBlock()
				}
				continue
			}
			sawAttr = true
			if atStart {
				atStart = false
				d, dnErr := dn.ParseAllowEmpty(string(value))
				if dnErr != nil {
					return nil, syntaxErrorf(it.num, "%v", dnErr)
				}
				w.writeAttrLine("dn", []byte(d.String()))
				continue
			}
			w.writeAttrLine(name, value)
		}
	}
	if w.err != nil {
		return nil, w.err
	}

	again := NewReader(bytes.NewReader(out.Bytes()))
	after, err := again.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("ldif: the formatted document does not parse, so nothing was formatted: %w", err)
	}
	if again.Version() != r.Version() || !sameRecords(before, after) {
		return nil, errors.New("ldif: the formatted document does not say what the original said, so nothing was formatted")
	}
	return out.Bytes(), nil
}

// formatItem is one logical line of a document being formatted: a comment,
// kept as its physical lines, or an unfolded line of a record.
type formatItem struct {
	comment []string
	text    string
	num     int
}

// formatBlocks splits a document into blocks of logical lines separated by
// blank lines, unfolding exactly as the reader does: a continuation belongs
// to the line before it, a comment's continuations to the comment, and a
// continuation with nothing before it stands as a line of its own. Runs of
// blank lines produce no empty blocks.
func formatBlocks(src []byte) [][]formatItem {
	text := strings.TrimSuffix(string(src), "\n")
	var blocks [][]formatItem
	var block []formatItem
	open := false // whether the last item can take a continuation
	for i, raw := range strings.Split(text, "\n") {
		num := i + 1
		// bufio.ScanLines drops one CR and the reader another.
		raw = strings.TrimSuffix(strings.TrimSuffix(raw, "\r"), "\r")
		if raw == "" {
			if len(block) > 0 {
				blocks = append(blocks, block)
				block = nil
			}
			open = false
			continue
		}
		if after, isCont := strings.CutPrefix(raw, " "); isCont {
			if open {
				last := &block[len(block)-1]
				if last.comment != nil {
					last.comment = append(last.comment, commentLine(raw))
				} else {
					last.text += after
				}
				continue
			}
			// The reader returns an orphan continuation as a line by itself
			// and absorbs nothing onto it.
			if strings.HasPrefix(after, "#") {
				block = append(block, formatItem{comment: []string{commentLine(raw)}, num: num})
			} else {
				block = append(block, formatItem{text: after, num: num})
			}
			continue
		}
		if strings.HasPrefix(raw, "#") {
			block = append(block, formatItem{comment: []string{commentLine(raw)}, num: num})
		} else {
			block = append(block, formatItem{text: raw, num: num})
		}
		open = true
	}
	if len(block) > 0 {
		blocks = append(blocks, block)
	}
	return blocks
}

// commentLine is a comment's physical line as written, less any carriage
// returns at its end, which no reader keeps and an editor shows as nothing.
func commentLine(raw string) string { return strings.TrimRight(raw, "\r") }

// sameRecords reports whether two parses hold the same records: the same
// DNs, as rendered, and the same attributes, names and values, in the same
// order.
func sameRecords(a, b []*Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Change != y.Change || x.DN.String() != y.DN.String() || len(x.Attrs) != len(y.Attrs) {
			return false
		}
		for j := range x.Attrs {
			if x.Attrs[j].Name != y.Attrs[j].Name ||
				!slices.EqualFunc(x.Attrs[j].Values, y.Attrs[j].Values, bytes.Equal) {
				return false
			}
		}
	}
	return true
}
