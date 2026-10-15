package chrome

import (
	"context"
	"errors"
	"testing"

	"github.com/chromedp/chromedp"
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

// stubLaunch drives the retry loop with a scripted sequence of outcomes,
// recording how many attempts actually happened.
func stubLaunch(results ...error) (*Client, *int) {
	calls := 0
	c := &Client{}
	c.launch = func(context.Context, string, ...chromedp.Action) error {
		calls++
		if calls <= len(results) {
			return results[calls-1]
		}
		return nil
	}
	return c, &calls
}

var errLaunch = errors.New("chrome: websocket url timeout reached")

func TestRunRetriesAColdStartAndSucceeds(t *testing.T) {
	c, calls := stubLaunch(errLaunch, nil)

	if err := c.run(context.Background(), "file:///x"); err != nil {
		t.Fatalf("want the second attempt to succeed, got %v", err)
	}
	if *calls != 2 {
		t.Errorf("attempts = %d, want 2", *calls)
	}
}

func TestRunDoesNotRetryAPageFailure(t *testing.T) {
	// Repeating a bad selector just produces the same error more slowly.
	pageErr := errors.New("chrome: could not find node")
	c, calls := stubLaunch(pageErr, nil)

	if err := c.run(context.Background(), "file:///x"); !errors.Is(err, pageErr) {
		t.Fatalf("err = %v, want the page error surfaced unretried", err)
	}
	if *calls != 1 {
		t.Errorf("attempts = %d, want 1", *calls)
	}
}

func TestRunGivesUpAfterTheBudgetAndReturnsTheLastError(t *testing.T) {
	c, calls := stubLaunch(errLaunch, errLaunch, errLaunch)

	err := c.run(context.Background(), "file:///x")
	if err == nil || !isLaunchFailure(err) {
		t.Fatalf("err = %v, want the launch failure surfaced", err)
	}
	if *calls != LaunchAttempts {
		t.Errorf("attempts = %d, want %d", *calls, LaunchAttempts)
	}
}

func TestRunStopsRetryingOnceTheContextIsDone(t *testing.T) {
	// A cancelled build must not spend its remaining budget on retries.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, calls := stubLaunch(errLaunch, nil)

	if err := c.run(ctx, "file:///x"); err == nil {
		t.Fatal("want the launch error, not a retry into a dead context")
	}
	if *calls != 1 {
		t.Errorf("attempts = %d, want 1", *calls)
	}
}

func TestRunSucceedsFirstTimeWithoutRetrying(t *testing.T) {
	c, calls := stubLaunch(nil)

	if err := c.run(context.Background(), "file:///x"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if *calls != 1 {
		t.Errorf("attempts = %d, want 1", *calls)
	}
}
