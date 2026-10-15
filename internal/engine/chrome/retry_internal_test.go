package chrome

import (
	"errors"
	"testing"
)

// TestIsLaunchFailureMatchesOnlyTheTransientStartupError guards the one
// decision this retry rests on: a page-level failure must NOT be retried,
// because repeating a bad selector just produces the same error more slowly.
func TestIsLaunchFailureMatchesOnlyTheTransientStartupError(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		retry bool
	}{
		{"cold runner, chrome still starting", errors.New("chrome: websocket url timeout reached"), true},
		{"wrapped the same way run() wraps it", errors.New("chrome: context deadline exceeded: websocket url timeout reached"), true},
		{"bad selector is the page's fault", errors.New("chrome: could not find node"), false},
		{"script threw", errors.New("chrome: exception \"ReferenceError\""), false},
		{"no browser installed", errors.New("chrome: exec: \"google-chrome\": executable file not found in $PATH"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLaunchFailure(tc.err); got != tc.retry {
				t.Errorf("isLaunchFailure(%v) = %v, want %v", tc.err, got, tc.retry)
			}
		})
	}
}

func TestLaunchAttemptsIsBoundedAndGreaterThanOne(t *testing.T) {
	// One would not retry at all; a large number turns a genuine "chrome is
	// not installed" into a long wait for the same message.
	if LaunchAttempts < 2 || LaunchAttempts > 3 {
		t.Errorf("LaunchAttempts = %d, want a small retry budget", LaunchAttempts)
	}
}
