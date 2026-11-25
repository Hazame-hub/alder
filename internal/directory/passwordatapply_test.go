package directory

import (
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

func TestAPasswordSuppliedAtApplyIsOnlyPlanned(t *testing.T) {
	target, _ := dn.Parse("uid=a,dc=x")
	rec := ChangeRecord{DN: target, Type: ChangeSetPassword, PasswordAtApply: true}
	if err := rec.ValidatePlanned(); err != nil {
		t.Fatalf("planned: %v", err)
	}
	if rec.Validate() == nil {
		t.Fatal("Validate accepted a password change with no password")
	}
	rec.NewPassword = "x"
	if rec.ValidatePlanned() == nil || rec.Validate() == nil {
		t.Fatal("a password change supplied at apply that carries a password was accepted")
	}
	other := ChangeRecord{DN: target, Type: ChangeDelete, PasswordAtApply: true}
	if other.ValidatePlanned() == nil {
		t.Fatal("a delete marked as supplied at apply was accepted")
	}
}
