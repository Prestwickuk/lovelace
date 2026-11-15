package auth

import (
	"testing"

	"github.com/tirbach/tir-bach/internal/domain"
)

func TestPasswordHashingAndPolicy(t *testing.T) {
	if _, err := HashPassword("short123"); err == nil {
		t.Fatal("expected short password to fail policy")
	}

	hash, err := HashPassword("pembrokeshire-meadow-2026")
	if err != nil {
		t.Fatalf("hash password failed: %v", err)
	}

	ok, err := VerifyPassword("pembrokeshire-meadow-2026", hash)
	if err != nil || !ok {
		t.Fatalf("expected valid password verification, got ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword("wrong-password-1234", hash)
	if err != nil {
		t.Fatalf("unexpected error on wrong password: %v", err)
	}
	if ok {
		t.Fatal("expected wrong password to fail verification")
	}
}

func TestRBACMatrix(t *testing.T) {
	if !HasPermission(domain.RoleOwnerAdmin, PermUsersManage) {
		t.Fatal("owner_admin must manage users")
	}
	if HasPermission(domain.RoleManager, PermUsersManage) {
		t.Fatal("manager must not manage users")
	}
	if !HasPermission(domain.RoleManager, PermInventoryWrite) {
		t.Fatal("manager must write inventory")
	}
	if !HasPermission(domain.RoleStaff, PermBlocksWrite) {
		t.Fatal("staff must write availability blocks")
	}
	if HasPermission(domain.RoleStaff, PermRatesWrite) {
		t.Fatal("staff must not write rates")
	}
	if HasPermission(domain.RoleViewer, PermBlocksWrite) {
		t.Fatal("viewer must not write blocks")
	}
	if HasPermission(domain.Role("unknown"), PermInventoryRead) {
		t.Fatal("unknown role must be denied")
	}
}
