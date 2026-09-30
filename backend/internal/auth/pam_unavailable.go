//go:build !pam || !linux || !cgo

package auth

import (
	"context"
	"errors"
)

func PAMAvailable() bool { return false }

func AuthenticatePAM(_ context.Context, _, _, _ string) error {
	return errors.New("PAM authentication is not included in this build; rebuild on Linux with CGO_ENABLED=1 and -tags pam")
}
