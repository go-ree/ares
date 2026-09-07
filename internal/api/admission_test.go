package api

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestRequestAdmissionThrottledKeyDoesNotConsumeGlobalBudget(t *testing.T) {
	admission := newRequestAdmission(0, 2, 0, 1, 10, 10)

	release, ok := admission.acquire("attacker")
	if !ok {
		t.Fatal("first attacker request was unexpectedly rejected")
	}
	release()
	for requestNumber := 0; requestNumber < 10; requestNumber++ {
		if _, admitted := admission.acquire("attacker"); admitted {
			t.Fatalf("throttled attacker request %d was admitted", requestNumber)
		}
	}

	release, ok = admission.acquire("legitimate")
	if !ok {
		t.Fatal("throttled attacker drained the legitimate key's global budget")
	}
	release()
	if _, admitted := admission.acquire("another-legitimate-key"); admitted {
		t.Fatal("request was admitted after the two-token global budget was consumed")
	}
}

func TestRequestAdmissionGlobalRejectionRestoresPerKeyBudget(t *testing.T) {
	admission := newRequestAdmission(0, 1, 0, 2, 10, 10)

	release, ok := admission.acquire("first")
	if !ok {
		t.Fatal("first request was unexpectedly rejected")
	}
	release()
	if _, admitted := admission.acquire("second"); admitted {
		t.Fatal("second request exceeded the global budget")
	}

	admission.global = rate.NewLimiter(0, 1)
	release, ok = admission.acquire("second")
	if !ok {
		t.Fatal("global rejection consumed the second key's per-key budget")
	}
	release()
}

func TestRequestAdmissionAppliesEveryKeyDimension(t *testing.T) {
	admission := newRequestAdmission(rate.Inf, 100, 0, 1, 10, 10)

	release, ok := admission.acquire("session:one", "client:192.0.2.1")
	if !ok {
		t.Fatal("first request was unexpectedly rejected")
	}
	release()
	if _, admitted := admission.acquire("session:two", "client:192.0.2.1"); admitted {
		t.Fatal("rotating the session key bypassed the client-key rate limit")
	}
	release, ok = admission.acquire("session:two", "client:192.0.2.2")
	if !ok {
		t.Fatal("independent session and client keys were unexpectedly rejected")
	}
	release()
}

func TestDenialAdmissionOverLimitPrincipalCannotDrainGlobalBudget(t *testing.T) {
	admission := newDenialAdmission(rate.Every(time.Hour), 2, rate.Every(time.Hour), 1)
	if !admission.allow("attacker") {
		t.Fatal("first attacker denial was unexpectedly rejected")
	}
	for index := 0; index < 10; index++ {
		if admission.allow("attacker") {
			t.Fatal("over-limit attacker denial was unexpectedly admitted")
		}
	}
	if !admission.allow("victim") {
		t.Fatal("over-limit attacker drained the global denial-audit budget")
	}
}

func TestDenialAdmissionRestoresPerKeyBudgetAfterGlobalRejection(t *testing.T) {
	admission := newDenialAdmission(rate.Every(time.Hour), 1, rate.Every(time.Hour), 1)
	if !admission.allow("first") {
		t.Fatal("first denial was unexpectedly rejected")
	}
	if admission.allow("second") {
		t.Fatal("denial was unexpectedly admitted after global exhaustion")
	}
	admission.global = nil
	if !admission.allow("second") {
		t.Fatal("global rejection permanently consumed the principal budget")
	}
}
