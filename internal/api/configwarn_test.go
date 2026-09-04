package api

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

func capsWithConfig(schemaTarget string) directory.Capabilities {
	caps := directory.Capabilities{
		Config: directory.ConfigAccess{DN: "cn=config", Readable: true},
	}
	if schemaTarget != "" {
		caps.SchemaWrite = directory.SchemaWrite{
			Style:   directory.SchemaStyleConfig,
			Targets: []directory.SchemaTarget{{DN: schemaTarget}},
		}
	}
	return caps
}

func modify(t *testing.T, target string, attr string, op directory.ModOp) directory.ChangeRecord {
	t.Helper()
	d, err := dn.Parse(target)
	if err != nil {
		t.Fatalf("dn.Parse(%q): %v", target, err)
	}
	return directory.ChangeRecord{
		DN:   d,
		Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: op, Name: attr, Values: [][]byte{[]byte("x")}}},
	}
}

func TestConfigWarnings(t *testing.T) {
	tests := []struct {
		name    string
		record  directory.ChangeRecord
		caps    directory.Capabilities
		want    []string // substrings that must each appear in some warning
		wantNot []string
	}{
		{
			name:    "an ordinary entry says nothing about configuration",
			record:  modify(t, "uid=bob,ou=people,dc=alder,dc=test", "cn", directory.ModReplace),
			caps:    capsWithConfig(""),
			wantNot: []string{"own configuration"},
		},
		{
			name:   "a configuration entry is called out",
			record: modify(t, "olcDatabase={1}mdb,cn=config", "olcSizeLimit", directory.ModReplace),
			caps:   capsWithConfig(""),
			want:   []string{"own configuration", "no undo"},
		},
		{
			// The single most destructive thing reachable through the editor.
			name:   "replacing the access control list explains what a replace does",
			record: modify(t, "olcDatabase={1}mdb,cn=config", "olcAccess", directory.ModReplace),
			caps:   capsWithConfig(""),
			want:   []string{"olcAccess", "access control list", "still grants your bind DN"},
		},
		{
			// aci lives on ordinary entries, not in the configuration tree, so
			// this is the case the general warning would miss entirely.
			name:   "aci on a data entry is not reached, because it is not configuration",
			record: modify(t, "ou=people,dc=alder,dc=test", "aci", directory.ModReplace),
			caps:   capsWithConfig(""),
			want:   nil,
		},
		{
			name:   "changing the administrative identity is called out",
			record: modify(t, "cn=config", "nsslapd-rootdn", directory.ModReplace),
			caps:   capsWithConfig(""),
			want:   []string{"administrative identity", "the one you are changing"},
		},
		{
			name:   "changing how the server listens is called out",
			record: modify(t, "cn=config", "nsslapd-securePort", directory.ModReplace),
			caps:   capsWithConfig(""),
			want:   []string{"listens for connections", "server's console"},
		},
		{
			name:   "changing the suffix is called out",
			record: modify(t, "olcDatabase={1}mdb,cn=config", "olcSuffix", directory.ModReplace),
			caps:   capsWithConfig(""),
			want:   []string{"what the database holds", "unreachable"},
		},
		{
			// Schema editing reaches the configuration tree too, but through a
			// form that already says so. Repeating it trains people to click past.
			name:    "a schema target does not repeat the general warning",
			record:  modify(t, "cn={4}alder,cn=schema,cn=config", "olcAttributeTypes", directory.ModAdd),
			caps:    capsWithConfig("cn={4}alder,cn=schema,cn=config"),
			wantNot: []string{"own configuration"},
		},
		{
			name:    "a server with no configuration tree warns about nothing",
			record:  modify(t, "cn=config", "olcAccess", directory.ModReplace),
			caps:    directory.Capabilities{},
			wantNot: []string{"own configuration", "access control list"},
		},
		{
			// DNs compare case-insensitively; a warning that missed CN=CONFIG
			// would be worse than none at all.
			name:   "the tree is matched case-insensitively",
			record: modify(t, "olcDatabase={1}mdb,CN=CONFIG", "olcAccess", directory.ModReplace),
			caps:   capsWithConfig(""),
			want:   []string{"own configuration", "access control list"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := configWarnings(tc.record, tc.caps)
			joined := strings.Join(got, "\n")
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Errorf("no warning mentions %q\n  got: %q", want, got)
				}
			}
			for _, unwanted := range tc.wantNot {
				if strings.Contains(joined, unwanted) {
					t.Errorf("a warning mentions %q and should not\n  got: %q", unwanted, got)
				}
			}
			if tc.want == nil && tc.wantNot == nil && len(got) != 0 {
				t.Errorf("want no warnings, got %q", got)
			}
		})
	}
}

// The general warning names the tree the server actually announced, rather than
// a hardcoded DN, so it stays true on a server that keeps its configuration
// somewhere else.
func TestConfigWarningNamesTheAnnouncedTree(t *testing.T) {
	caps := directory.Capabilities{Config: directory.ConfigAccess{DN: "cn=settings", Readable: true}}
	got := configWarnings(modify(t, "cn=db,cn=settings", "olcSizeLimit", directory.ModReplace), caps)
	if len(got) == 0 || !strings.Contains(got[0], "cn=settings") {
		t.Errorf("the warning does not name the announced tree: %q", got)
	}
}
