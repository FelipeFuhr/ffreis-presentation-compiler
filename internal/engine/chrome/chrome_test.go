package chrome

import (
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
)

// Everything in this package that does not require a running browser is
// covered here. Navigate, Eval and CaptureAll need real Chrome and are
// exercised by `make smoke-check`, `make check` and `make check-selftest`
// instead — a unit test that launched a browser would pass or fail depending
// on the machine, which is the trap engine_test.go already fell into once.

func TestNewUsesTheDefaultTimeout(t *testing.T) {
	if got := New().Timeout; got != DefaultTimeout {
		t.Errorf("New().Timeout = %v, want %v", got, DefaultTimeout)
	}
}

func TestTimeoutFallsBackWhenUnsetOrNonsensical(t *testing.T) {
	cases := []struct {
		name string
		set  time.Duration
		want time.Duration
	}{
		{"zero value", 0, DefaultTimeout},
		{"negative", -5 * time.Second, DefaultTimeout},
		{"explicit", 3 * time.Second, 3 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{Timeout: tc.set}
			if got := c.timeout(); got != tc.want {
				t.Errorf("timeout() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAwaitPromiseSetsTheFlag(t *testing.T) {
	// Without this the font-readiness step resolves to a pending promise and
	// the page is measured before its web fonts load.
	p := awaitPromise(&runtime.EvaluateParams{})
	if !p.AwaitPromise {
		t.Error("awaitPromise did not set AwaitPromise")
	}
}

func TestClipsBuildsViewports(t *testing.T) {
	boxes := []box{{X: 0, Y: 0, W: 1280, H: 720}, {X: 0, Y: 740, W: 1280, H: 720}}

	got, err := clips(boxes, "[data-slide]", 2)
	if err != nil {
		t.Fatalf("clips: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d viewports, want 2", len(got))
	}
	if got[1].Y != 740 || got[1].Width != 1280 {
		t.Errorf("second viewport = %+v, want the second element's box", got[1])
	}
	for i, v := range got {
		if v.Scale != 2 {
			t.Errorf("viewport %d scale = %v, want 2", i, v.Scale)
		}
	}
}

func TestClipsDefaultsAndRejects(t *testing.T) {
	t.Run("non-positive scale falls back to 1", func(t *testing.T) {
		got, err := clips([]box{{W: 10, H: 10}}, "sel", 0)
		if err != nil {
			t.Fatalf("clips: %v", err)
		}
		if got[0].Scale != 1 {
			t.Errorf("scale = %v, want 1", got[0].Scale)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		// Silently producing zero images would make an empty deck look fine.
		if _, err := clips(nil, "[data-slide]", 1); err == nil ||
			!strings.Contains(err.Error(), "no elements matched") {
			t.Fatalf("want a \"no elements matched\" error, got %v", err)
		}
	})

	t.Run("zero-sized element", func(t *testing.T) {
		_, err := clips([]box{{W: 10, H: 10}, {W: 0, H: 10}}, "sel", 1)
		if err == nil || !strings.Contains(err.Error(), "element 2") {
			t.Fatalf("want an error naming element 2, got %v", err)
		}
	})
}
