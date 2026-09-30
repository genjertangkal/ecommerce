package errors

import (
	stderrors "errors"
	"fmt"
	"testing"
)

func TestErrorMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *AppError
		want string
	}{
		{
			name: "without cause",
			err:  New(ErrCodeNotFound, "product missing"),
			want: "NOT_FOUND: product missing",
		},
		{
			name: "with cause",
			err:  Wrap(stderrors.New("db down"), ErrCodeInternal, "load failed"),
			want: "INTERNAL: load failed: db down",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWrapNilReturnsNil(t *testing.T) {
	t.Parallel()

	if got := Wrap(nil, ErrCodeInternal, "ignored"); got != nil {
		t.Errorf("Wrap(nil, ...) = %v, want nil", got)
	}
}

// Is reports the code of the *outermost* AppError, because that is the
// classification of the operation that failed. errors.As stops at the first
// match in the chain, which is the outermost wrapper.
func TestIsReportsOutermostCode(t *testing.T) {
	t.Parallel()

	inner := NotFound("product missing")
	outer := Wrap(inner, ErrCodeInternal, "handler failed")

	if !Is(outer, ErrCodeInternal) {
		t.Error("Is(outer, ErrCodeInternal) = false, want true")
	}
	if Is(outer, ErrCodeNotFound) {
		t.Error("Is(outer, ErrCodeNotFound) = true; the outermost AppError is the internal one")
	}
}

// The inner AppError must still be reachable explicitly, otherwise the cause is
// lost to callers that want the specific failure.
func TestInnerCodeReachableThroughUnwrap(t *testing.T) {
	t.Parallel()

	inner := NotFound("product missing")
	outer := Wrap(inner, ErrCodeInternal, "handler failed")

	unwrapped, ok := outer.Unwrap().(*AppError)
	if !ok {
		t.Fatalf("Unwrap() = %T, want *AppError", outer.Unwrap())
	}
	if !Is(unwrapped, ErrCodeNotFound) {
		t.Errorf("Is(unwrapped, ErrCodeNotFound) = false, want true")
	}
}

func TestCodeAndMessageAccessors(t *testing.T) {
	t.Parallel()

	err := Wrap(stderrors.New("boom"), ErrCodeUnavailable, "backend down")

	code, ok := Code(err)
	if !ok || code != ErrCodeUnavailable {
		t.Errorf("Code(err) = %q, %v; want %q, true", code, ok, ErrCodeUnavailable)
	}

	msg, ok := Message(err)
	if !ok || msg != "backend down" {
		t.Errorf("Message(err) = %q, %v; want %q, true", msg, ok, "backend down")
	}
}

func TestUnwrapPreservesCause(t *testing.T) {
	t.Parallel()

	cause := stderrors.New("root cause")
	err := Wrap(cause, ErrCodeInternal, "wrapped")

	if !stderrors.Is(err, cause) {
		t.Error("errors.Is could not find the wrapped cause")
	}
}

func TestAccessorsOnPlainError(t *testing.T) {
	t.Parallel()

	err := stderrors.New("not an AppError")

	if _, ok := Code(err); ok {
		t.Error("Code() reported success for a non-AppError")
	}
	if _, ok := As(err); ok {
		t.Error("As() reported success for a non-AppError")
	}
}

func TestConstructorsSetCodes(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err  *AppError
		want ErrorCode
	}{
		"NotFound":         {NotFound("x"), ErrCodeNotFound},
		"InvalidArgument":  {InvalidArgument("x"), ErrCodeInvalidArgument},
		"PermissionDenied": {PermissionDenied("x"), ErrCodePermissionDenied},
		"Internal":         {Internal("x"), ErrCodeInternal},
		"Unavailable":      {Unavailable("x"), ErrCodeUnavailable},
	}

	for name, tc := range cases {
		if tc.err.Code != tc.want {
			t.Errorf("%s() code = %q, want %q", name, tc.err.Code, tc.want)
		}
		if got, want := tc.err.Error(), fmt.Sprintf("%s: x", tc.want); got != want {
			t.Errorf("%s().Error() = %q, want %q", name, got, want)
		}
	}
}
