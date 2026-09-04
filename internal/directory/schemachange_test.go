package directory

import "testing"

// sameNames chooses which form a replace takes, so getting it wrong either
// makes an edit fail on one server or silently leaves two definitions.
func TestSameNames(t *testing.T) {
	const base = "( 1.2.3 NAME 'alderTeam' DESC 'a' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"

	for _, tc := range []struct {
		name     string
		old, new string
		want     bool
	}{
		{
			name: "the same name with other fields changed updates in place",
			old:  base,
			new:  "( 1.2.3 NAME 'alderTeam' DESC 'b' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			want: true,
		},
		{
			// A rename is a collision, not an update, so the old definition has
			// to be removed explicitly.
			name: "a rename does not",
			old:  base,
			new:  "( 1.2.3 NAME 'alderSquad' DESC 'a' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			want: false,
		},
		{
			name: "names compare case-insensitively, as the protocol does",
			old:  base,
			new:  "( 1.2.3 NAME 'ALDERTEAM' DESC 'a' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			want: true,
		},
		{
			name: "adding an alias is a change of names",
			old:  base,
			new:  "( 1.2.3 NAME ( 'alderTeam' 'alderSquad' ) SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			want: false,
		},
		{
			// The stored form carries a position prefix where the schema lives
			// in configuration; it is not part of the name.
			name: "an ordering prefix on the stored value is ignored",
			old:  "{4}" + base,
			new:  "( 1.2.3 NAME 'alderTeam' DESC 'b' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			want: true,
		},
		{
			name: "object classes compare the same way",
			old:  "( 1.2.4 NAME 'alderEmployee' SUP top AUXILIARY MUST alderTeam )",
			new:  "( 1.2.4 NAME 'alderEmployee' SUP top AUXILIARY MUST ( alderTeam $ cn ) )",
			want: true,
		},
		{
			// Unparseable input takes the conservative branch, which removes
			// the old value explicitly rather than hoping for an update.
			name: "nonsense is not treated as a match",
			old:  "not a definition",
			new:  base,
			want: false,
		},
		{
			name: "a definition with no name is not a match either",
			old:  "( 1.2.3 SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			new:  base,
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameNames(tc.old, tc.new); got != tc.want {
				t.Errorf("sameNames = %v, want %v", got, tc.want)
			}
		})
	}
}
