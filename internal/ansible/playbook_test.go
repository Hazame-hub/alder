package ansible

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

func entry(t *testing.T, d string, pairs ...string) *directory.Entry {
	t.Helper()
	parsed, err := dn.Parse(d)
	if err != nil {
		t.Fatalf("parsing %q: %v", d, err)
	}
	e := directory.NewEntry(parsed)
	for i := 0; i+1 < len(pairs); i += 2 {
		name, value := pairs[i], pairs[i+1]
		e.Set(name, append(e.Attributes[name], []byte(value)))
	}
	return e
}

// The reason this renderer exists: a file that lists attributes and does not
// enforce them is worse than no file, because somebody runs it, sees green, and
// believes the directory matches it.
func TestEveryEntryGetsAConvergingTask(t *testing.T) {
	e := entry(t, "uid=alice,ou=people,dc=alder,dc=test",
		"objectClass", "inetOrgPerson", "cn", "Alice", "sn", "Liddell")
	out := EnforceTasks([]*directory.Entry{e}, nil, EnforceOptions{})

	if !strings.Contains(out, "community.general.ldap_entry") {
		t.Error("nothing creates the entry")
	}
	if !strings.Contains(out, "community.general.ldap_attrs") {
		t.Error("nothing enforces the attributes")
	}
	if !strings.Contains(out, "state: exact") {
		t.Errorf("the attributes are not enforced exactly:\n%s", out)
	}
}

// state: present on ldap_attrs would add the listed values and leave whatever
// else is there, which is the same silent divergence ldap_entry has.
func TestTheAttributeTaskIsExactNotPresent(t *testing.T) {
	e := entry(t, "uid=alice,ou=people,dc=alder,dc=test", "objectClass", "person", "cn", "Alice")
	out := EnforceTasks([]*directory.Entry{e}, nil, EnforceOptions{})

	attrsTask := out[strings.Index(out, "ldap_attrs"):]
	if strings.Contains(attrsTask, "state: present") {
		t.Errorf("the ldap_attrs task uses state: present:\n%s", attrsTask)
	}
}

// A search returns the server's order, not the tree's, and ldap_entry cannot
// create a child under a parent that does not exist yet.
func TestEntriesAreOrderedParentFirst(t *testing.T) {
	deep := entry(t, "uid=alice,ou=people,dc=alder,dc=test", "objectClass", "person")
	mid := entry(t, "ou=people,dc=alder,dc=test", "objectClass", "organizationalUnit")
	root := entry(t, "dc=alder,dc=test", "objectClass", "domain")

	out := EnforceTasks([]*directory.Entry{deep, mid, root}, nil, EnforceOptions{})

	iRoot := strings.Index(out, `Ensure "dc=alder,dc=test" exists`)
	iMid := strings.Index(out, `Ensure "ou=people,dc=alder,dc=test" exists`)
	iDeep := strings.Index(out, `Ensure "uid=alice,ou=people,dc=alder,dc=test" exists`)
	if iRoot < 0 || iMid < 0 || iDeep < 0 {
		t.Fatalf("an entry is missing from the playbook:\n%s", out)
	}
	if iRoot >= iMid || iMid >= iDeep {
		t.Errorf("entries are not parent first (root %d, mid %d, deep %d):\n%s",
			iRoot, iMid, iDeep, out)
	}
}

// Equal depth keeps the order the directory returned, so a playbook of one
// level is not reshuffled for no reason.
func TestEqualDepthKeepsTheDirectoryOrder(t *testing.T) {
	a := entry(t, "uid=a,ou=people,dc=alder,dc=test", "objectClass", "person")
	b := entry(t, "uid=b,ou=people,dc=alder,dc=test", "objectClass", "person")
	out := EnforceTasks([]*directory.Entry{b, a}, nil, EnforceOptions{})
	if strings.Index(out, "uid=b,") > strings.Index(out, "uid=a,") {
		t.Error("entries at the same depth were reordered")
	}
}

// A generated file goes into a repository.
func TestSensitiveAttributesAreNeverListed(t *testing.T) {
	e := entry(t, "uid=alice,ou=people,dc=alder,dc=test",
		"objectClass", "inetOrgPerson", "cn", "Alice",
		"userPassword", "{SSHA}reallyasecret")
	out := EnforceTasks([]*directory.Entry{e}, nil, EnforceOptions{})

	// The header names userPassword as something it omits, so the check is on
	// the tasks rather than on the whole file.
	tasks := out[strings.Index(out, "- name:"):]
	if strings.Contains(tasks, "userPassword") || strings.Contains(tasks, "reallyasecret") {
		t.Errorf("a password hash reached the playbook:\n%s", tasks)
	}
	if !strings.Contains(tasks, `"cn": "Alice"`) {
		t.Errorf("dropping the secret also dropped everything else:\n%s", tasks)
	}
}

func TestObjectClassGoesToTheEntryTask(t *testing.T) {
	e := entry(t, "ou=people,dc=alder,dc=test",
		"objectClass", "top", "ou", "people")
	out := EnforceTasks([]*directory.Entry{e}, nil, EnforceOptions{})
	entryTask := out[:strings.Index(out, "ldap_attrs")]
	if !strings.Contains(entryTask, "objectClass:") || !strings.Contains(entryTask, `- "top"`) {
		t.Errorf("the object classes are not on the create task:\n%s", entryTask)
	}
}

// An entry with nothing enforceable gets a create task and no second task,
// rather than an ldap_attrs with an empty mapping, which Ansible rejects.
func TestAnEntryWithNothingToEnforceGetsNoAttributeTask(t *testing.T) {
	e := entry(t, "dc=alder,dc=test", "objectClass", "domain")
	out := EnforceTasks([]*directory.Entry{e}, nil, EnforceOptions{})
	if strings.Contains(out, "ldap_attrs") {
		t.Errorf("an empty attribute task was emitted:\n%s", out)
	}
}

// A partial playbook that does not say so reads later as the whole subtree.
func TestTruncationIsStatedInTheHeader(t *testing.T) {
	e := entry(t, "dc=alder,dc=test", "objectClass", "domain")
	out := EnforceTasks([]*directory.Entry{e}, nil, EnforceOptions{Truncated: true})
	if !strings.Contains(out, "WARNING") || !strings.Contains(out, "whole subtree") {
		t.Errorf("a truncated playbook does not say so:\n%s", out)
	}
}

// The header has to say what the file does, because "playbook generated from
// live content" is exactly the artefact somebody assumes only reports.
func TestTheHeaderSaysItWillChangeThings(t *testing.T) {
	e := entry(t, "dc=alder,dc=test", "objectClass", "domain")
	out := EnforceTasks([]*directory.Entry{e}, nil, EnforceOptions{
		Base: "dc=alder,dc=test", Scope: "sub", Filter: "(objectClass=*)",
	})
	for _, want := range []string{"will change them", "dc=alder,dc=test", "sub", "(objectClass=*)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the header does not mention %q:\n%s", want, out)
		}
	}
}

// The connection is variables, never the live host and never a password.
func TestTheConnectionStaysVariables(t *testing.T) {
	e := entry(t, "dc=alder,dc=test", "objectClass", "domain", "dc", "alder")
	out := EnforceTasks([]*directory.Entry{e}, nil, EnforceOptions{})
	if !strings.Contains(out, "{{ ldap_bind_pw }}") {
		t.Error("the bind password is not a variable reference")
	}
	for _, leak := range []string{"localhost", "10636", "alder-admin"} {
		if strings.Contains(out, leak) {
			t.Errorf("the playbook leaked %q", leak)
		}
	}
}
