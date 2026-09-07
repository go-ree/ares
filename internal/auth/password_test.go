package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashRoundTripAndRandomSalt(t *testing.T) {
	password := "correct horse battery staple"
	first, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("password hashes reused a salt")
	}
	if !VerifyPassword(first, password) {
		t.Fatal("correct password was rejected")
	}
	if VerifyPassword(first, "incorrect password") {
		t.Fatal("incorrect password was accepted")
	}
}

func TestPasswordHashQueueHonorsContextCancellation(t *testing.T) {
	for index := 0; index < cap(passwordHashSlots); index++ {
		passwordHashSlots <- struct{}{}
	}
	defer func() {
		for index := 0; index < cap(passwordHashSlots); index++ {
			<-passwordHashSlots
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := HashPasswordContext(ctx, "correct horse battery staple")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued password hash error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued password hash ignored context cancellation")
	}
}

func TestPasswordHashRejectsUnsafeInputsAndParameters(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password was accepted")
	}
	if _, err := HashPassword(strings.Repeat("x", maximumPasswordBytes+1)); err == nil {
		t.Fatal("oversized password was accepted")
	}
	if VerifyPassword("$argon2id$v=19$m=4294967295,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$ZmFrZWZha2VmYWtlZmFrZQ", "password") {
		t.Fatal("unsafe database-controlled Argon2 parameters were accepted")
	}
}
