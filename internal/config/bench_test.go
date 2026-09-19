package config

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// Capturing a configuration: one paged read and then one pass over what came
// back. A hundred settings is a small OpenLDAP; a thousand is 389 Directory
// Server with its plugins. The cost should grow with the configuration, and a
// capture that scanned for each resource would show it here.

func benchEntries(b *testing.B, settings, resources int) []*directory.Entry {
	b.Helper()
	parse := func(text string) dn.DN {
		d, err := dn.Parse(text)
		if err != nil {
			b.Fatalf("dn %q: %v", text, err)
		}
		return d
	}
	global := directory.NewEntry(parse("cn=config"))
	global.Set("objectClass", [][]byte{[]byte("olcGlobal")})
	global.Set("cn", [][]byte{[]byte("config")})
	out := []*directory.Entry{global}
	perResource := settings / max(resources, 1)
	for r := 0; r < resources; r++ {
		e := directory.NewEntry(parse(fmt.Sprintf("olcDatabase={%d}mdb,cn=config", r+1)))
		e.Set("objectClass", [][]byte{[]byte("olcMdbConfig")})
		e.Set("olcDatabase", [][]byte{[]byte(fmt.Sprintf("{%d}mdb", r+1))})
		e.Set("olcSuffix", [][]byte{[]byte(fmt.Sprintf("dc=tenant%04d,dc=test", r))})
		e.Set("olcRootPW", [][]byte{[]byte("a secret that is never read")})
		for i := 0; i < perResource; i++ {
			e.Set(fmt.Sprintf("olcDbSetting%05d", i), [][]byte{[]byte(fmt.Sprintf("%d", i))})
		}
		out = append(out, e)
	}
	return out
}

func benchCapture(b *testing.B, settings, resources int) {
	b.Helper()
	r := &fakeReader{
		caps: directory.Capabilities{VendorName: "OpenLDAP", ConfigContext: "cn=config",
			Config: directory.ConfigAccess{DN: "cn=config", Readable: true}},
		entries: benchEntries(b, settings, resources),
	}
	opts := Options{Now: func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) }}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := Capture(context.Background(), r, opts)
		if err != nil {
			b.Fatalf("Capture: %v", err)
		}
		if s.Counts.Settings == 0 {
			b.Fatal("nothing was captured")
		}
	}
}

func BenchmarkCaptureConfig(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("settings=%d", n), func(b *testing.B) { benchCapture(b, n, 4) })
	}
}

func BenchmarkCaptureConfigRepeatedResources(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("settings=%d", n), func(b *testing.B) { benchCapture(b, n, 100) })
	}
}
