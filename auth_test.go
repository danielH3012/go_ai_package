package goaipackage

import (
	"testing"
)

func TestToUserAuth(t *testing.T) {
	// 1. Direct struct
	t.Run("Direct UserAuth struct", func(t *testing.T) {
		orig := UserAuth{
			Role:      "admin",
			UserID:    "usr-1",
			CompanyID: "comp-1",
			Name:      "Alice",
		}
		res := ToUserAuth(orig)
		if res != orig {
			t.Fatalf("expected %+v, got %+v", orig, res)
		}
	})

	// 2. Nested "auth" in map
	t.Run("Nested auth in map", func(t *testing.T) {
		ctxMap := map[string]any{
			"auth": UserAuth{
				Role:      "manager",
				UserID:    "usr-2",
				CompanyID: "comp-2",
				Name:      "Bob",
			},
		}
		res := ToUserAuth(ctxMap)
		if res.Role != "manager" || res.UserID != "usr-2" || res.CompanyID != "comp-2" || res.Name != "Bob" {
			t.Fatalf("unexpected res: %+v", res)
		}
	})

	// 3. User with aliases (User doesn't need redundant keys anymore!)
	t.Run("Single canonical keys or aliases", func(t *testing.T) {
		// Testing company_id alias
		ctxMap1 := map[string]any{
			"role":       "admin",
			"user_id":    "u100",
			"company_id": "c100",
			"name":       "Charlie",
		}
		res1 := ToUserAuth(ctxMap1)
		if res1.CompanyID != "c100" || res1.UserID != "u100" || res1.Name != "Charlie" || res1.Role != "admin" {
			t.Fatalf("unexpected res1: %+v", res1)
		}

		// Testing company (legacy) + username
		ctxMap2 := map[string]any{
			"Role":     "user",
			"UserID":   "u200",
			"company":  "c200",
			"username": "David",
		}
		res2 := ToUserAuth(ctxMap2)
		if res2.CompanyID != "c200" || res2.UserID != "u200" || res2.Name != "David" || res2.Role != "user" {
			t.Fatalf("unexpected res2: %+v", res2)
		}

		// Testing tenant_id alias
		ctxMap3 := map[string]any{
			"role":      "operator",
			"userId":    "u300",
			"tenant_id": "t300",
			"user_name": "Eve",
		}
		res3 := ToUserAuth(ctxMap3)
		if res3.CompanyID != "t300" || res3.UserID != "u300" || res3.Name != "Eve" || res3.Role != "operator" {
			t.Fatalf("unexpected res3: %+v", res3)
		}
	})
}

func TestExtractCompanyFromContext(t *testing.T) {
	// 1. "company" key
	if res := extractCompanyFromContext(map[string]any{"company": "ACME"}); res != "ACME" {
		t.Fatalf("expected ACME, got %q", res)
	}

	// 2. "company_id" key
	if res := extractCompanyFromContext(map[string]any{"company_id": "ACME_ID"}); res != "ACME_ID" {
		t.Fatalf("expected ACME_ID, got %q", res)
	}

	// 3. nested in auth
	if res := extractCompanyFromContext(map[string]any{"auth": UserAuth{CompanyID: "AUTH_CORP"}}); res != "AUTH_CORP" {
		t.Fatalf("expected AUTH_CORP, got %q", res)
	}
}
