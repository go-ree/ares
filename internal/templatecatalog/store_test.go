package templatecatalog

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestValidationAndSafeErrors(t *testing.T) {
	for _, key := range []string{"Java", "java\n", "", "a/b"} {
		if validType(key, "Java") {
			t.Fatalf("accepted %q", key)
		}
	}
	if validType("java", " \n") || validRevision(0) || validRevision(math.MaxUint64) {
		t.Fatal("invalid metadata accepted")
	}
	if !validType("java", "Java 构建") || !validRevision(1) {
		t.Fatal("valid metadata rejected")
	}
	for _, err := range []error{errors.New("password=secret"), &mysql.MySQLError{Number: 1045, Message: "secret"}} {
		if storageError(err) != ErrStorage {
			t.Fatal("database error leaked")
		}
	}
	if storageError(&mysql.MySQLError{Number: 1062, Message: "secret"}) != ErrConflict || storageError(context.Canceled) != context.Canceled {
		t.Fatal("classification")
	}
}
