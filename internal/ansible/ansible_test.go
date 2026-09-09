package ansible

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// tasksFor renders one change record, failing the test if it cannot.
func tasksFor(t *testing.T, c directory.ChangeRecord) string {
	t.Helper()
	out, err := Task(c)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	return out
}

// modify builds a modify record against a fixed DN.
func modify(t *testing.T, mods ...directory.Mod) string {
	t.Helper()
	return tasksFor(t, directory.ChangeRecord{
		DN:   mustDN(t, "uid=alice,ou=people,dc=alder,dc=test"),
		Type: directory.ChangeModify,
		Mods: mods,
	})
}

// stateOf returns the state line of the task containing name.
func stateOf(t *testing.T, out, name string) string {
	t.Helper()
	for _, task := range strings.Split(out, "\n\n") {
		if !strings.Contains(task, `"`+name+`"`) {
			continue
		}
		for _, line := range strings.Split(task, "\n") {
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "state:") {
				return trimmed
			}
		}
	}
	t.Fatalf("no task mentions %s:\n%s", name, out)
	return ""
}

// The distinction RFC 2849 draws with the presence of values under a delete,
// carried through to a module that expresses it with a different state.
//
// This was wrong: both kinds of delete rendered as state: absent, and absent
// removes the values it is given. A delete naming none therefore removed
// nothing while the playbook reported success -- the LDIF said "drop this
// attribute" and the Ansible did nothing at all.
func TestDeletingAWholeAttributeIsExactNotAbsent(t *testing.T) {
	out := modify(t, directory.Mod{Op: directory.ModDelete, Name: "telephoneNumber"})

	if got := stateOf(t, out, "telephoneNumber"); !strings.HasPrefix(got, "state: exact") {
		t.Errorf("a delete naming no values rendered %q; absent would remove nothing:\n%s", got, out)
	}
	if !strings.Contains(out, `"telephoneNumber": []`) {
		t.Errorf("the attribute is not emptied:\n%s", out)
	}
}

// The other half of the same distinction, which must keep working.
func TestDeletingNamedValuesStaysAbsent(t *testing.T) {
	out := modify(t, directory.Mod{
		Op: directory.ModDelete, Name: "mail", Values: vals("old@alder.test"),
	})

	if got := stateOf(t, out, "mail"); !strings.HasPrefix(got, "state: absent") {
		t.Errorf("a delete naming values rendered %q; exact would drop the other addresses:\n%s", got, out)
	}
}

// The two cannot share a task, however adjacent they are in the record: one
// needs exact and the other absent, and a task carries one state.
func TestTheTwoKindsOfDeleteDoNotShareATask(t *testing.T) {
	out := modify(t,
		directory.Mod{Op: directory.ModDelete, Name: "telephoneNumber"},
		directory.Mod{Op: directory.ModDelete, Name: "mail", Values: vals("old@alder.test")},
	)

	if strings.Count(out, "community.general.ldap_attrs") != 2 {
		t.Errorf("the two deletes were not split into separate tasks:\n%s", out)
	}
	if got := stateOf(t, out, "telephoneNumber"); !strings.HasPrefix(got, "state: exact") {
		t.Errorf("the whole-attribute delete rendered %q:\n%s", got, out)
	}
	if got := stateOf(t, out, "mail"); !strings.HasPrefix(got, "state: absent") {
		t.Errorf("the value delete rendered %q:\n%s", got, out)
	}
}

// Adjacent deletes of the same kind still share one task; the split is on the
// kind, not on every modification.
func TestAdjacentWholeAttributeDeletesShareATask(t *testing.T) {
	out := modify(t,
		directory.Mod{Op: directory.ModDelete, Name: "telephoneNumber"},
		directory.Mod{Op: directory.ModDelete, Name: "description"},
	)

	if n := strings.Count(out, "community.general.ldap_attrs"); n != 1 {
		t.Errorf("two deletes of the same kind produced %d tasks:\n%s", n, out)
	}
}

// A replace naming no values means the same thing and already rendered
// correctly; it must keep doing so now that the delete path shares the branch.
func TestReplacingWithNoValuesEmptiesTheAttribute(t *testing.T) {
	out := modify(t, directory.Mod{Op: directory.ModReplace, Name: "description"})

	if got := stateOf(t, out, "description"); !strings.HasPrefix(got, "state: exact") {
		t.Errorf("a replace naming no values rendered %q:\n%s", got, out)
	}
	if !strings.Contains(out, `"description": []`) {
		t.Errorf("the attribute is not emptied:\n%s", out)
	}
}

// The order of the original modify is preserved: an add followed by a replace
// of one attribute is not the same directory as the reverse.
func TestRunsKeepTheOrderOfTheRecord(t *testing.T) {
	out := modify(t,
		directory.Mod{Op: directory.ModAdd, Name: "mail", Values: vals("first@alder.test")},
		directory.Mod{Op: directory.ModReplace, Name: "mail", Values: vals("second@alder.test")},
	)

	add := strings.Index(out, "state: present")
	replace := strings.Index(out, "state: exact")
	if add < 0 || replace < 0 || add > replace {
		t.Errorf("the add and the replace came out in the wrong order:\n%s", out)
	}
}

// rename renders the one change that has no native module.
func rename(t *testing.T) string {
	t.Helper()
	return tasksFor(t, directory.ChangeRecord{
		DN:           mustDN(t, "uid=alice,ou=people,dc=alder,dc=test"),
		Type:         directory.ChangeModRDN,
		NewRDN:       "uid=alice.liddell",
		DeleteOldRDN: true,
	})
}

// A rename is the one change that shells out, and so the one that could put the
// bind password where every user on the host can read it.
//
// It used to: the command was "ldapmodify ... -w {{ ldap_bind_pw }}", and
// /proc/<pid>/cmdline is world-readable for as long as a process runs. This
// repository already refuses -w in the ldapsearch command it shows operators.
func TestRenameKeepsThePasswordOutOfTheArguments(t *testing.T) {
	out := rename(t)

	if strings.Contains(out, "-w ") {
		t.Errorf("the rename passes the password as an argument:\n%s", out)
	}

	// The password may appear once, under environment. Anything earlier is in
	// the command the shell will run.
	env := strings.Index(out, "  environment:")
	if env < 0 {
		t.Fatalf("no environment block:\n%s", out)
	}
	if command := out[:env]; strings.Contains(command, "ldap_bind_pw") {
		t.Errorf("the password is interpolated into the command itself:\n%s", command)
	}
	if !strings.Contains(out[env:], "LDAP_BIND_PW:") {
		t.Errorf("the password does not reach the task at all:\n%s", out)
	}
}

// ldapmodify -y takes the complete contents of the file as the password, so a
// trailing newline becomes part of it and the bind fails for a reason that
// looks nothing like the cause.
func TestRenameWritesThePasswordFileWithoutATrailingNewline(t *testing.T) {
	out := rename(t)

	if !strings.Contains(out, `printf '%s' "$LDAP_BIND_PW"`) {
		t.Errorf("the password file is not written with printf:\n%s", out)
	}
	if strings.Contains(out, `echo "$LDAP_BIND_PW"`) {
		t.Errorf("echo appends a newline that becomes part of the password:\n%s", out)
	}
}

// However the task ends, the file holding the password goes with it.
func TestRenameRemovesThePasswordFile(t *testing.T) {
	out := rename(t)

	if !strings.Contains(out, "trap 'rm -f") {
		t.Errorf("nothing removes the password file when the task fails:\n%s", out)
	}
	if !strings.Contains(out, "set -eu") {
		t.Errorf("the script continues past a failure:\n%s", out)
	}
}

// The rename still carries the LDIF the user confirmed, which is the reason
// this task shells out rather than deleting and recreating the entry.
func TestRenameCarriesTheConfirmedLDIF(t *testing.T) {
	out := rename(t)

	for _, want := range []string{
		"changetype: modrdn",
		"newrdn: uid=alice.liddell",
		"deleteoldrdn: 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the task does not carry %q:\n%s", want, out)
		}
	}
}
