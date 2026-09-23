package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/hazame-hub/alder/internal/changepkg"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Recognising what an artifact is.
//
// Only by what the document says it is: its format, version and kind. A file
// name says nothing, and a document that claims to be one thing is decoded
// strictly as that thing -- unknown fields, a wrong checksum or a version this
// build does not read are refused, exactly as the command that made the
// artifact would refuse them.

// ErrNotArtifact is returned for a document that is not an Alder artifact a
// preflight reads.
var ErrNotArtifact = errors.New("preflight: the document is not a change package, a schema snapshot, a data snapshot or a configuration snapshot")

// ErrUnsupportedKind is returned for a snapshot of a kind a preflight does not
// read.
var ErrUnsupportedKind = errors.New("preflight: the snapshot is of a kind a preflight does not read")

// Detect names the artifact type a document claims to be, without decoding it.
func Detect(data []byte) (string, error) {
	var probe struct {
		Format string `json:"format"`
		Kind   string `json:"kind"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil {
		return "", ErrNotArtifact
	}
	switch probe.Format {
	case changepkg.Format:
		return SourceChangePackage, nil
	case snapshot.Format:
		switch probe.Kind {
		case snapshot.KindSchema:
			return SourceSchemaSnapshot, nil
		case snapshot.KindData:
			return SourceDataSnapshot, nil
		case snapshot.KindConfig:
			return SourceConfigSnapshot, nil
		}
		return "", ErrUnsupportedKind
	}
	return "", ErrNotArtifact
}

// Run decodes an artifact and preflights it against a target. The artifact's
// own decoding errors are returned as they are, so a caller reports them with
// the codes the artifact's commands already use.
func Run(ctx context.Context, data []byte, t Target, opts Options) (*Report, error) {
	kind, err := Detect(data)
	if err != nil {
		return nil, err
	}
	switch kind {
	case SourceChangePackage:
		p, integrity, err := changepkg.Decode(data)
		if err != nil {
			return nil, err
		}
		return Package(ctx, p, string(integrity), t, opts)
	case SourceSchemaSnapshot:
		s, integrity, err := snapshot.DecodeSchema(data)
		if err != nil {
			return nil, err
		}
		return SchemaSnapshot(ctx, s, string(integrity), t, opts)
	case SourceConfigSnapshot:
		s, integrity, err := snapshot.DecodeConfig(data)
		if err != nil {
			return nil, err
		}
		return ConfigSnapshot(ctx, s, string(integrity), t, opts)
	default:
		s, integrity, err := snapshot.Decode(data)
		if err != nil {
			return nil, err
		}
		return DataSnapshot(ctx, s, string(integrity), t, opts)
	}
}
