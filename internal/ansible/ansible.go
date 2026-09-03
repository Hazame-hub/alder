// Package ansible renders a directory.ChangeRecord as an Ansible task.
//
// The output targets the community.general LDAP modules, which is what a
// platform engineer automating a directory already has installed. It is meant
// to be pasted into a playbook and run, not read as documentation, so it uses
// variables for the connection parameters rather than inlining the host Alder
// happens to be connected to, and it never emits a bind password.
//
// The package renders from the same ChangeRecord that produced the LDIF the
// user confirmed and the operation the server received. Two renderings of one
// record cannot describe two different changes.
package ansible

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// Variable names used for the connection parameters. Emitting the live values
// would put a directory host and bind DN into whatever the user pastes the task
// into, and the password would be worse than either.
const (
	varURI    = "{{ ldap_server_uri }}"
	varBindDN = "{{ ldap_bind_dn }}"
	// #nosec G101 -- this is the Ansible variable reference emitted in place of
	// a password. It is a template placeholder precisely so that no credential
	// is ever written into generated output.
	varBindPW  = "{{ ldap_bind_pw }}"
	moduleAttr = "community.general.ldap_attrs"
	moduleEnt  = "community.general.ldap_entry"
	modulePw   = "community.general.ldap_passwd"
	// The new password is a variable for the same reason the bind password is:
	// a generated file gets pasted into a repository.
	// #nosec G101 -- a template placeholder, which is precisely what stops a
	// real password being written into generated output.
	varNewPW = "{{ ldap_new_password }}"
)

// Task renders a change record as one or more Ansible tasks.
//
// A modify that mixes operations becomes several tasks, because ldap_attrs
// takes one state per task. They are emitted in the order of the modifications
// so that applying them in sequence has the same effect as the single LDAP
// modify operation would.
func Task(c directory.ChangeRecord) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	switch c.Type {
	case directory.ChangeAdd:
		writeAdd(&b, c)
	case directory.ChangeDelete:
		writeDelete(&b, c)
	case directory.ChangeModify:
		writeModify(&b, c)
	case directory.ChangeSetPassword:
		writeSetPassword(&b, c)
	case directory.ChangeModRDN:
		writeRename(&b, c)
	default:
		return "", fmt.Errorf("ansible: cannot render change type %q", c.Type)
	}
	return b.String(), nil
}

func writeAdd(b *strings.Builder, c directory.ChangeRecord) {
	var objectClasses [][]byte
	type kv struct {
		name   string
		values [][]byte
	}
	var attrs []kv
	for _, a := range c.Attrs {
		if equalFold(schema.BaseName(a.Name), "objectClass") {
			objectClasses = append(objectClasses, a.Values...)
			continue
		}
		attrs = append(attrs, kv{a.Name, a.Values})
	}

	fmt.Fprintf(b, "- name: Create %s\n", yamlScalar(c.DN.String()))
	fmt.Fprintf(b, "  %s:\n", moduleEnt)
	writeConnection(b)
	fmt.Fprintf(b, "    dn: %s\n", yamlScalar(c.DN.String()))
	if len(objectClasses) > 0 {
		b.WriteString("    objectClass:\n")
		for _, v := range objectClasses {
			fmt.Fprintf(b, "      - %s\n", yamlValue(v))
		}
	}
	if len(attrs) > 0 {
		b.WriteString("    attributes:\n")
		for _, a := range attrs {
			writeAttrValues(b, "      ", a.name, a.values)
		}
	}
	b.WriteString("    state: present\n")
}

func writeDelete(b *strings.Builder, c directory.ChangeRecord) {
	fmt.Fprintf(b, "- name: Remove %s\n", yamlScalar(c.DN.String()))
	fmt.Fprintf(b, "  %s:\n", moduleEnt)
	writeConnection(b)
	fmt.Fprintf(b, "    dn: %s\n", yamlScalar(c.DN.String()))
	b.WriteString("    state: absent\n")
}

// writeModify emits one ldap_attrs task per run of adjacent modifications that
// share an operation. Splitting on the operation is forced by the module;
// keeping the runs in order is what preserves the semantics of the original
// modify, where an add followed by a replace of the same attribute is not the
// same as the reverse.
func writeModify(b *strings.Builder, c directory.ChangeRecord) {
	runs := groupByOp(c.Mods)
	for i, run := range runs {
		if i > 0 {
			b.WriteString("\n")
		}
		state, comment := stateFor(run.op)
		names := make([]string, len(run.mods))
		for j, m := range run.mods {
			names[j] = m.Name
		}
		fmt.Fprintf(b, "- name: %s\n",
			yamlScalar(fmt.Sprintf("%s %s on %s", verbFor(run.op), strings.Join(names, ", "), c.DN)))
		fmt.Fprintf(b, "  %s:\n", moduleAttr)
		writeConnection(b)
		fmt.Fprintf(b, "    dn: %s\n", yamlScalar(c.DN.String()))
		b.WriteString("    attributes:\n")
		for _, m := range run.mods {
			if len(m.Values) == 0 {
				// state: exact with no values is how the module expresses
				// "remove every value of this attribute"; a delete with no
				// values in LDAP means exactly that.
				fmt.Fprintf(b, "      %s: []\n", yamlKey(m.Name))
				continue
			}
			writeAttrValues(b, "      ", m.Name, m.Values)
		}
		fmt.Fprintf(b, "    state: %s%s\n", state, comment)
	}
}

// writeSetPassword emits a task for a password change.
//
// ldap_passwd is idempotent in the way Ansible means it: it sets the password
// only if the current one does not already match, so a replayed playbook does
// not report a change every run.
func writeSetPassword(b *strings.Builder, c directory.ChangeRecord) {
	fmt.Fprintf(b, "- name: %s\n", yamlScalar(fmt.Sprintf("Set the password of %s", c.DN)))
	fmt.Fprintf(b, "  %s:\n", modulePw)
	writeConnection(b)
	fmt.Fprintf(b, "    dn: %s\n", yamlScalar(c.DN.String()))
	fmt.Fprintf(b, "    passwd: %s\n", yamlScalar(varNewPW))
}

// writeRename emits a task for a modrdn.
//
// community.general has no module that renames an entry, so this is the one
// change Alder cannot express as a native Ansible task. Emitting an ldap_entry
// pair that deletes and recreates would silently drop every attribute of the
// entry, so the task shells out to ldapmodify with the same LDIF the user
// confirmed, and says why.
func writeRename(b *strings.Builder, c directory.ChangeRecord) {
	b.WriteString("# community.general has no module that renames a directory entry.\n")
	b.WriteString("# Deleting and recreating the entry would lose every attribute on it,\n")
	b.WriteString("# so this task applies the same modrdn LDIF that Alder previewed.\n")
	fmt.Fprintf(b, "- name: Rename %s\n", yamlScalar(c.DN.String()))
	b.WriteString("  ansible.builtin.command:\n")
	fmt.Fprintf(b, "    cmd: ldapmodify -H %s -D %s -w %s -c\n", varURI, varBindDN, varBindPW)
	b.WriteString("    stdin: |\n")
	for _, line := range strings.Split(strings.TrimRight(c.LDIFFolded(), "\n"), "\n") {
		fmt.Fprintf(b, "      %s\n", line)
	}
	b.WriteString("  changed_when: true\n")
}

func writeConnection(b *strings.Builder) {
	fmt.Fprintf(b, "    server_uri: %s\n", yamlScalar(varURI))
	fmt.Fprintf(b, "    bind_dn: %s\n", yamlScalar(varBindDN))
	fmt.Fprintf(b, "    bind_pw: %s\n", yamlScalar(varBindPW))
}

type modRun struct {
	op   directory.ModOp
	mods []directory.Mod
}

func groupByOp(mods []directory.Mod) []modRun {
	var runs []modRun
	for _, m := range mods {
		if len(runs) > 0 && runs[len(runs)-1].op == m.Op {
			runs[len(runs)-1].mods = append(runs[len(runs)-1].mods, m)
			continue
		}
		runs = append(runs, modRun{op: m.Op, mods: []directory.Mod{m}})
	}
	return runs
}

// stateFor maps an LDAP modification onto an ldap_attrs state, with a trailing
// comment where the mapping is not obvious.
func stateFor(op directory.ModOp) (state, comment string) {
	switch op {
	case directory.ModAdd:
		return "present", "  # add these values, leaving any others"
	case directory.ModDelete:
		return "absent", "  # remove these values, leaving any others"
	default:
		return "exact", "  # replace: the attribute ends up with exactly these values"
	}
}

func verbFor(op directory.ModOp) string {
	switch op {
	case directory.ModAdd:
		return "Add"
	case directory.ModDelete:
		return "Remove"
	default:
		return "Set"
	}
}

func writeAttrValues(b *strings.Builder, indent, name string, values [][]byte) {
	if len(values) == 1 {
		fmt.Fprintf(b, "%s%s: %s\n", indent, yamlKey(name), yamlValue(values[0]))
		return
	}
	fmt.Fprintf(b, "%s%s:\n", indent, yamlKey(name))
	for _, v := range values {
		fmt.Fprintf(b, "%s  - %s\n", indent, yamlValue(v))
	}
}

// yamlValue renders one attribute value. A value that is not valid UTF-8, or
// that holds control characters, is emitted as a !!binary scalar rather than
// mangled into a string.
func yamlValue(v []byte) string {
	if !utf8.Valid(v) || hasControl(v) {
		return "!!binary " + yamlScalar(base64.StdEncoding.EncodeToString(v))
	}
	return yamlScalar(string(v))
}

func hasControl(v []byte) bool {
	for _, c := range v {
		if c < 0x20 && c != '\t' {
			return true
		}
	}
	return false
}

// yamlKey renders a mapping key. Attribute descriptions are restricted to
// letters, digits, hyphen and semicolon, none of which need quoting, but the
// quoting is applied anyway so there is one rule rather than two.
func yamlKey(s string) string { return yamlScalar(s) }

// yamlScalar renders a YAML double-quoted scalar.
//
// The double-quoted style is the only YAML scalar style with a complete escape
// mechanism, and its escapes are JSON's. Emitting every scalar in it means the
// renderer never has to reason about whether a particular value would be
// misread as a number, a boolean, a date, or the string "null", which is the
// entire catalogue of ways hand-written YAML goes wrong.
func yamlScalar(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func equalFold(a, b string) bool { return strings.EqualFold(a, b) }

// Playbook wraps tasks in a runnable playbook, with the variables the tasks
// reference declared at the top so the output is a complete file rather than a
// fragment the user has to assemble.
func Playbook(tasks string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("# Generated by Alder. Review before running.\n")
	b.WriteString("#\n")
	b.WriteString("# The connection variables are left unset on purpose: a bind password does\n")
	b.WriteString("# not belong in a generated file. Supply them from a vault, the command\n")
	b.WriteString("# line, or the environment.\n")
	b.WriteString("- name: Apply directory changes\n")
	b.WriteString("  hosts: localhost\n")
	b.WriteString("  gather_facts: false\n")
	b.WriteString("  vars:\n")
	b.WriteString("    ldap_server_uri: \"ldaps://directory.example.test\"\n")
	b.WriteString("    ldap_bind_dn: \"cn=admin,dc=example,dc=test\"\n")
	b.WriteString("    ldap_bind_pw: \"{{ lookup('env', 'LDAP_BIND_PW') }}\"\n")
	b.WriteString("  tasks:\n")
	for _, line := range strings.Split(strings.TrimRight(tasks, "\n"), "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}
