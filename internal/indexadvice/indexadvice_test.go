package indexadvice

import (
	"strings"
	"testing"
	"time"
)

var harness = []Backend{{
	Suffix: "dc=alder,dc=test",
	Indexes: map[string][]string{
		"objectclass": {"eq"},
		"uid":         {"eq"},
		"cn":          {"eq", "sub"},
		"mail":        {"eq"},
	},
}}

const ds389Log = `[30/Oct/2026:14:02:11.000000000 +0000] conn=12 fd=64 slot=64 connection from 10.0.0.7 to 10.0.0.1
[30/Oct/2026:14:02:11.100000000 +0000] conn=12 op=0 BIND dn="cn=Directory Manager" method=128 version=3
[30/Oct/2026:14:02:11.100000000 +0000] conn=12 op=0 RESULT err=0 tag=97 nentries=0 wtime=0.0001 optime=0.0001 etime=0.0002 dn="cn=directory manager"
[30/Oct/2026:14:02:11.200000000 +0000] conn=12 op=1 SRCH base="ou=people,dc=alder,dc=test" scope=2 filter="(title=Engineer)" attrs=ALL
[30/Oct/2026:14:02:11.700000000 +0000] conn=12 op=1 RESULT err=0 tag=101 nentries=3 wtime=0.0001 optime=0.5 etime=0.5001 notes=A details="Fully Unindexed Filter"
[30/Oct/2026:14:03:00.000000000 +0000] conn=12 op=2 SRCH base="dc=alder,dc=test" scope=2 filter="(&(objectClass=person)(title=Manager))" attrs="cn"
[30/Oct/2026:14:03:00.010000000 +0000] conn=12 op=2 RESULT err=0 tag=101 nentries=1 wtime=0.0001 optime=0.01 etime=0.0101 notes=U details="Partially Unindexed Filter"
[30/Oct/2026:14:03:01.000000000 +0000] conn=12 op=3 SRCH base="dc=alder,dc=test" scope=2 filter="(uid=alice)" attrs=ALL
[30/Oct/2026:14:03:01.001000000 +0000] conn=12 op=3 RESULT err=0 tag=101 nentries=1 wtime=0.0001 optime=0.001 etime=0.001
[30/Oct/2026:14:03:02.000000000 +0000] conn=12 op=4 SRCH base="dc=alder,dc=test" scope=2 filter="(mail=*@example.com)" attrs=ALL
[30/Oct/2026:14:03:02.300000000 +0000] conn=12 op=4 RESULT err=0 tag=101 nentries=40 wtime=0.0001 optime=0.3 etime=0.3001 notes=U
[30/Oct/2026:14:03:03.000000000 +0000] conn=12 op=5 SRCH base="cn=config" scope=2 filter="(nsslapd-x=1)" attrs=ALL
[30/Oct/2026:14:03:03.900000000 +0000] conn=12 op=5 RESULT err=0 tag=101 nentries=0 wtime=0.0001 optime=0.9 etime=0.9 notes=A
[30/Oct/2026:14:03:04.000000000 +0000] conn=12 op=6 SRCH base="dc=alder,dc=test" scope=2 filter="(title=Director)" attrs=ALL
`

func TestA389AccessLogSuggestsWhatTheServerMarkedUnindexed(t *testing.T) {
	r, err := Read(strings.NewReader(ds389Log), Options{Backends: harness})
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != Format389 || r.Searches != 6 || r.Counted != 4 || r.Unattributed != 1 || r.WithoutResult != 1 {
		t.Fatalf("report %+v", r)
	}
	if len(r.Suggestions) != 2 {
		t.Fatalf("suggestions %+v", r.Suggestions)
	}
	title := r.Suggestions[0]
	if title.Attribute != "title" || title.Type != TypeEquality || title.Status != StatusCandidate ||
		title.Searches != 2 || title.MarkedUnindexed != 2 {
		t.Errorf("title: %+v", title)
	}
	if title.Total != 510200*time.Microsecond || title.Worst != 500100*time.Microsecond {
		t.Errorf("title durations: %s %s", title.Total, title.Worst)
	}
	if !title.First.Equal(time.Date(2026, 10, 30, 14, 2, 11, 200000000, time.UTC)) || title.Last.Minute() != 3 {
		t.Errorf("title seen %s .. %s", title.First, title.Last)
	}
	mail := r.Suggestions[1]
	if mail.Attribute != "mail" || mail.Type != TypeSubstring || mail.Status != StatusTypeMissing || mail.Reason == "" {
		t.Errorf("mail: %+v", mail)
	}
}

func TestNoValueFromTheLogReachesTheReport(t *testing.T) {
	r, err := Read(strings.NewReader(ds389Log), Options{Backends: harness})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Suggestions {
		for _, e := range s.Examples {
			for _, secret := range []string{"Engineer", "Manager", "example.com", "person", "alice"} {
				if strings.Contains(e, secret) {
					t.Errorf("example %q carries %q", e, secret)
				}
			}
		}
	}
	if got := r.Suggestions[0].Examples; len(got) != 2 || got[0] != "(title=…)" || got[1] != "(&(objectClass=…)(title=…))" {
		t.Errorf("examples %q", got)
	}
	if got := r.Suggestions[1].Examples; len(got) != 1 || got[0] != "(mail=*…)" {
		t.Errorf("examples %q", got)
	}
}

const openldapLog = `Oct 30 14:02:11 ldap slapd[42]: conn=1000 fd=12 ACCEPT from IP=10.0.0.7:51000 (IP=0.0.0.0:389)
Oct 30 14:02:11 ldap slapd[42]: conn=1000 op=1 SRCH base="dc=alder,dc=test" scope=2 deref=0 filter="(|(title=a)(uid=b))"
Oct 30 14:02:11 ldap slapd[42]: conn=1000 op=1 SRCH attr=cn
Oct 30 14:02:11 ldap slapd[42]: <= mdb_equality_candidates: (title) not indexed
Oct 30 14:02:11 ldap slapd[42]: conn=1000 op=1 SEARCH RESULT tag=101 err=0 qtime=0.000012 etime=0.250000 nentries=2 text=
Oct 30 14:02:12 ldap slapd[42]: conn=1000 op=2 SRCH base="dc=alder,dc=test" scope=2 deref=0 filter="(title=c)"
Oct 30 14:02:12 ldap slapd[42]: conn=1000 op=2 SEARCH RESULT tag=101 err=0 qtime=0.000012 etime=0.000400 nentries=1 text=
Oct 30 14:02:13 ldap slapd[42]: conn=1000 op=3 SRCH base="dc=alder,dc=test" scope=2 deref=0 filter="(!(description=x))"
Oct 30 14:02:13 ldap slapd[42]: conn=1000 op=3 SEARCH RESULT tag=101 err=0 qtime=0.000012 etime=2.0 nentries=1 text=
Oct 30 14:02:14 ldap slapd[42]: conn=1000 op=4 SRCH base="dc=alder,dc=test" scope=2 deref=0 filter="(&(uid=z)(manager=*))"
Oct 30 14:02:14 ldap slapd[42]: conn=1000 op=4 SEARCH RESULT tag=101 err=0 qtime=0.000012 etime=1.0 nentries=1 text=
Oct 30 14:02:15 ldap slapd[42]: conn=1000 op=5 SRCH base="dc=alder,dc=test" scope=2 deref=0 filter="not a filter"
Oct 30 14:02:15 ldap slapd[42]: conn=1000 op=5 SEARCH RESULT tag=101 err=0 qtime=0.000012 etime=1.0 nentries=1 text=
`

func TestAnOpenLDAPStatsLogCountsSlowSearches(t *testing.T) {
	r, err := Read(strings.NewReader(openldapLog), Options{Backends: harness})
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != FormatOpenLDAP || r.Searches != 5 || r.Counted != 4 || r.Unparsed != 1 {
		t.Fatalf("report %+v", r)
	}
	// The or needs title; the fast search does not count; the not suggests
	// nothing; the and is served by uid's index, so manager is not blamed.
	if len(r.Suggestions) != 1 {
		t.Fatalf("suggestions %+v", r.Suggestions)
	}
	s := r.Suggestions[0]
	if s.Attribute != "title" || s.Type != TypeEquality || s.Searches != 1 || s.ServerSaidNotIndexed != 1 ||
		s.MarkedUnindexed != 0 || !s.First.IsZero() {
		t.Errorf("title: %+v", s)
	}
}

func TestTheThresholdIsTheOperators(t *testing.T) {
	r, err := Read(strings.NewReader(openldapLog), Options{Backends: harness, SlowerThan: 300 * time.Microsecond})
	if err != nil {
		t.Fatal(err)
	}
	if r.Suggestions[0].Attribute != "title" || r.Suggestions[0].Searches != 2 {
		t.Errorf("with a lower threshold: %+v", r.Suggestions)
	}
}

func TestABlockedIndexIsShownWithItsReason(t *testing.T) {
	backends := []Backend{{Suffix: "dc=alder,dc=test", Indexes: map[string][]string{"uid": {"eq"}},
		Blocked: func(attribute string, _ []string) string {
			if attribute == "title" {
				return "no"
			}
			return ""
		}}}
	r, err := Read(strings.NewReader(ds389Log), Options{Backends: backends})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Suggestions {
		if s.Attribute == "title" && (s.Status != StatusBlocked || s.Reason != "no") {
			t.Errorf("title: %+v", s)
		}
	}
}

func TestANestedBackendTakesItsOwnSearches(t *testing.T) {
	backends := append([]Backend{{Suffix: "ou=people,dc=alder,dc=test", Indexes: map[string][]string{"title": {"eq"}}}}, harness...)
	r, err := Read(strings.NewReader(ds389Log), Options{Backends: backends})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Suggestions {
		if s.Attribute == "title" && (s.Backend != "dc=alder,dc=test" || s.Searches != 1) {
			t.Errorf("title: %+v", s)
		}
	}
}

func TestALineTooLongIsCountedAndTheRestRead(t *testing.T) {
	log := strings.Repeat("x", maxLine+10) + "\n" + ds389Log
	r, err := Read(strings.NewReader(log), Options{Backends: harness})
	if err != nil {
		t.Fatal(err)
	}
	if r.Unrecognised == 0 || len(r.Suggestions) == 0 {
		t.Errorf("report %+v", r)
	}
}

func FuzzRead(f *testing.F) {
	f.Add(ds389Log)
	f.Add(openldapLog)
	f.Add(`conn=1 op=1 SRCH base="" filter="(`)
	f.Fuzz(func(t *testing.T, log string) {
		r, err := Read(strings.NewReader(log), Options{Backends: harness})
		if err != nil {
			return
		}
		if r.Counted > r.Searches {
			t.Fatalf("counted %d of %d searches", r.Counted, r.Searches)
		}
	})
}
