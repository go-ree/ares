package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	minimumPasswordBytes = 12
	maximumPasswordBytes = 1024
)

type argon2Parameters struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLength  uint32
	keyLength   uint32
}

var defaultArgon2Parameters = argon2Parameters{
	memory: 64 * 1024, iterations: 3, parallelism: 2, saltLength: 16, keyLength: 32,
}

// Password hashing is intentionally bounded to avoid exhausting the host when
// several login requests arrive at once. Multi-replica/global rate limiting is
// part of W06; this protects a single process in W02.
var passwordHashSlots = make(chan struct{}, 2)

func HashPassword(password string) (string, error) {
	return HashPasswordContext(context.Background(), password)
}

func HashPasswordContext(ctx context.Context, password string) (string, error) {
	if len(password) < minimumPasswordBytes || len(password) > maximumPasswordBytes {
		return "", newInputError(fmt.Sprintf("密码长度必须在 %d 到 %d 字节之间", minimumPasswordBytes, maximumPasswordBytes))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	salt := make([]byte, defaultArgon2Parameters.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成密码盐值: %w", err)
	}
	key, err := derivePasswordKey(ctx, password, salt, defaultArgon2Parameters)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		defaultArgon2Parameters.memory,
		defaultArgon2Parameters.iterations,
		defaultArgon2Parameters.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(encoded, password string) bool {
	valid, _ := VerifyPasswordContext(context.Background(), encoded, password)
	return valid
}

func VerifyPasswordContext(ctx context.Context, encoded, password string) (bool, error) {
	parameters, salt, expected, err := parsePasswordHash(encoded)
	if err != nil || len(password) > maximumPasswordBytes {
		return false, nil
	}
	actual, err := derivePasswordKey(ctx, password, salt, parameters)
	if err != nil {
		return false, err
	}
	return len(actual) == len(expected) && subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func derivePasswordKey(ctx context.Context, password string, salt []byte, parameters argon2Parameters) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case passwordHashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-passwordHashSlots }()
	return argon2.IDKey(
		[]byte(password), salt, parameters.iterations, parameters.memory,
		parameters.parallelism, parameters.keyLength,
	), nil
}

func parsePasswordHash(encoded string) (argon2Parameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return argon2Parameters{}, nil, nil, errors.New("unsupported password hash")
	}
	var parameters argon2Parameters
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &parameters.memory, &parameters.iterations, &parameters.parallelism); err != nil {
		return argon2Parameters{}, nil, nil, errors.New("invalid password hash parameters")
	}
	// Treat database-controlled parameters as untrusted input. These limits
	// prevent a corrupted hash from causing an excessive allocation or runtime.
	if parameters.memory < 8*1024 || parameters.memory > 256*1024 ||
		parameters.iterations < 1 || parameters.iterations > 10 ||
		parameters.parallelism < 1 || parameters.parallelism > 8 {
		return argon2Parameters{}, nil, nil, errors.New("unsafe password hash parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return argon2Parameters{}, nil, nil, errors.New("invalid password hash salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return argon2Parameters{}, nil, nil, errors.New("invalid password hash output")
	}
	parameters.saltLength = uint32(len(salt))
	parameters.keyLength = uint32(len(expected))
	return parameters, salt, expected, nil
}
