package auth

import (
	"testing"

	"github.com/Prestwickuk/lovelace/internal/domain"
)

func TestPasswordHashingAndPolicy(t *testing.T) {
	if _, err := HashPassword("short123"); err == nil {
		t.Fatal("expected short password to fail policy")
	}

	hash, err := HashPassword("countryside-meadow-2026")
	if err != nil {
		t.Fatalf("hash password failed: %v", err)
	}

	ok, err := VerifyPassword("countryside-meadow-2026", hash)
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

	// Reject dangerous PHC parameters (p=256 uint8 wraparound, oversized m= DoS, tiny salt).
	badHashes := []string{
		"$argon2id$v=19$m=65536,t=3,p=256$c29tZXJhbmRvbXNhbHQxNg$YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE",
		"$argon2id$v=19$m=4194304,t=3,p=1$c29tZXJhbmRvbXNhbHQxNg$YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE",
		"$argon2id$v=19$m=65536,t=99,p=1$c29tZXJhbmRvbXNhbHQxNg$YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE",
		"$argon2id$v=19$m=65536,t=3,p=1$c2hvcnQ$YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE",
	}
	for _, bh := range badHashes {
		if ok, err := VerifyPassword("countryside-meadow-2026", bh); err == nil || ok {
			t.Fatalf("expected dangerous PHC hash %q to fail verification, got ok=%v err=%v", bh, ok, err)
		}
	}

	if !ConstantTimeEqual("secret-token-abc", "secret-token-abc") {
		t.Fatal("expected equal tokens to match")
	}
	if ConstantTimeEqual("short", "secret-token-abc") || ConstantTimeEqual("", "") {
		t.Fatal("expected unequal or empty tokens not to match")
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
