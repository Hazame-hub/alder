package diff

import (
	"fmt"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/snapshot"
)

// A configuration is small -- a hundred settings on OpenLDAP, a few thousand on
// 389 Directory Server with every plugin configured -- so what matters here is
// not that comparing one is fast but that the cost grows with the
// configuration rather than with the square of it. Each benchmark runs at a
// hundred and at a thousand settings, and at a hundred repeated resources,
// where a comparison that paired settings by scanning would show as a ratio
// well above ten.

var configBenchSizes = []int{100, 1000}

func benchConfig(b *testing.B, settings int, resources int, drift bool) *snapshot.ConfigSnapshot {
	b.Helper()
	list := make([]snapshot.ConfigSetting, 0, settings)
	names := make([]snapshot.ConfigResource, 0, resources)
	for r := 0; r < resources; r++ {
		name := fmt.Sprintf("dc=tenant%04d,dc=test", r)
		names = append(names, snapshot.ConfigResource{Section: "backend", Kind: "database", Name: name,
			DN: fmt.Sprintf("olcDatabase={%d}mdb,cn=config", r+1), Label: fmt.Sprintf("{%d}mdb", r+1)})
	}
	for i := 0; i < settings; i++ {
		value := fmt.Sprintf("%d", i)
		if drift && i%10 == 0 { // one in ten changed
			value = fmt.Sprintf("%d", i+1)
		}
		setting := snapshot.ConfigSetting{
			Section: "limits", Key: fmt.Sprintf("olcSetting%05d", i), Values: []string{value},
			Type: "int", Mutability: snapshot.MutabilityWritable, Comparison: snapshot.ComparisonNormalised,
			DN: "cn=config",
		}
		if resources > 0 {
			resource := names[i%len(names)]
			setting.Section = "backend"
			setting.Resource = resource.ID()
			setting.DN = resource.DN
		}
		list = append(list, setting)
	}
	s, err := snapshot.BuildConfig(snapshot.ConfigCapture{
		Provider: snapshot.ProviderOpenLDAP, Vendor: "OpenLDAP", Root: "cn=config",
		Sections: []string{"backend", "limits"}, CreatedAt: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
	}, names, list)
	if err != nil {
		b.Fatalf("BuildConfig: %v", err)
	}
	return s
}

func BenchmarkCompareConfig(b *testing.B) {
	for _, n := range configBenchSizes {
		source := benchConfig(b, n, 0, false)
		target := benchConfig(b, n, 0, true)
		b.Run(fmt.Sprintf("settings=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				r := CompareConfig(ConfigSide{Snapshot: source}, ConfigSide{Snapshot: target}, ConfigOptions{})
				if r.Counts.Modified == 0 {
					b.Fatal("nothing compared")
				}
			}
		})
	}
}

// The same settings spread over a hundred repeated resources, which is where
// identity by name rather than by position has to hold.
func BenchmarkCompareConfigRepeatedResources(b *testing.B) {
	for _, n := range configBenchSizes {
		source := benchConfig(b, n, 100, false)
		target := benchConfig(b, n, 100, true)
		b.Run(fmt.Sprintf("settings=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				r := CompareConfig(ConfigSide{Snapshot: source}, ConfigSide{Snapshot: target}, ConfigOptions{})
				if r.Counts.Modified == 0 {
					b.Fatal("nothing compared")
				}
			}
		})
	}
}

func BenchmarkBuildConfigSnapshot(b *testing.B) {
	for _, n := range configBenchSizes {
		b.Run(fmt.Sprintf("settings=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchConfig(b, n, 10, false)
			}
		})
	}
}
