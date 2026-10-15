// Package chrome drives headless Chrome over CDP.
//
// It is deliberately slide-agnostic — it knows pages, selectors and viewports,
// never decks, themes or layouts — so it can become the fleet's shared
// html-render capability rather than a fourth private copy of "drive a
// browser". The PDF adapter still shells out to the browser binary, because
// printing needs no round trip; everything that reads something back lives
// here.
package chrome

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// DefaultTimeout bounds a whole navigate-and-act cycle.
const DefaultTimeout = 90 * time.Second

// Client runs actions against a page.
type Client struct {
	Timeout time.Duration
}

// New returns a client with the default timeout.
func New() *Client { return &Client{Timeout: DefaultTimeout} }

func (c *Client) timeout() time.Duration {
	if c.Timeout <= 0 {
		return DefaultTimeout
	}
	return c.Timeout
}

// runOnce opens a browser, navigates to pageURL, waits for fonts, then runs
// acts.
//
// Waiting on document.fonts.ready is load-bearing for both callers: measuring
// or capturing before the web font loads uses the fallback's metrics, which is
// precisely the drift this tooling exists to catch.
// LaunchAttempts is how many times a run will start a browser before giving
// up. A cold hosted runner regularly fails the first launch with "websocket
// url timeout reached" — Chrome is still coming up when chromedp stops
// waiting — and that failure is entirely transient: the identical command
// succeeds moments later. Twice it took down a deck build that was correct,
// which is how a green CI stops being believed.
//
// Two, not more: a browser that cannot start for a real reason (not
// installed, no sandbox permissions) should fail fast with that reason
// rather than make the operator wait through a retry storm for the same
// message.
const LaunchAttempts = 2

// run opens a browser and performs acts, retrying a failed *launch* once.
//
// Only the launch is retried. Once the page is up, a failure is the page's or
// the caller's — a bad selector, a script that threw — and repeating it would
// just produce the same error more slowly.
func (c *Client) run(ctx context.Context, pageURL string, acts ...chromedp.Action) error {
	var err error
	for attempt := 1; attempt <= LaunchAttempts; attempt++ {
		err = c.runOnce(ctx, pageURL, acts...)
		if err == nil || ctx.Err() != nil || !isLaunchFailure(err) {
			return err
		}
	}
	return err
}

// isLaunchFailure reports whether err is the transient browser-startup
// failure rather than something the page did.
//
// Matching on the message is unpleasant, but chromedp returns a plain
// errors.New for this and offers no sentinel to compare against; a typed
// check would silently stop matching the day they reword it, whereas this
// degrades to the old behaviour of failing once.
func isLaunchFailure(err error) bool {
	return strings.Contains(err.Error(), "websocket url timeout reached")
}

func (c *Client) runOnce(ctx context.Context, pageURL string, acts ...chromedp.Action) error {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("hide-scrollbars", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	runCtx, cancelRun := context.WithTimeout(browserCtx, c.timeout())
	defer cancelRun()

	prelude := []chromedp.Action{
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		chromedp.Evaluate(`document.fonts.ready.then(() => true)`, nil, awaitPromise),
	}
	if err := chromedp.Run(runCtx, append(prelude, acts...)...); err != nil {
		return fmt.Errorf("chrome: %w", err)
	}
	return nil
}

func awaitPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

// Eval evaluates script in the page and decodes the result into out.
func (c *Client) Eval(ctx context.Context, pageURL, script string, out any) error {
	return c.run(ctx, pageURL, chromedp.Evaluate(script, out))
}

// Shot is one captured element.
type Shot struct {
	Index int
	PNG   []byte
}

// box is an element's position and size in page coordinates.
type box struct {
	X, Y, W, H float64
}

// clips turns measured boxes into capture viewports. Kept separate from the
// CDP calls so the arithmetic and the validation are testable without a
// browser; everything below this line needs real Chrome.
func clips(boxes []box, selector string, scale float64) ([]*page.Viewport, error) {
	if len(boxes) == 0 {
		return nil, fmt.Errorf("no elements matched %q", selector)
	}
	if scale <= 0 {
		scale = 1
	}
	out := make([]*page.Viewport, 0, len(boxes))
	for i, b := range boxes {
		if b.W <= 0 || b.H <= 0 {
			return nil, fmt.Errorf("element %d matching %q has no size", i+1, selector)
		}
		out = append(out, &page.Viewport{X: b.X, Y: b.Y, Width: b.W, Height: b.H, Scale: scale})
	}
	return out, nil
}

// CaptureAll screenshots every element matching selector, in document order, at
// the given device scale factor (2 renders at twice the CSS resolution).
//
// Each element is captured from its own box rather than by scrolling a
// viewport, so a slide that is taller than the window still comes out whole.
func (c *Client) CaptureAll(ctx context.Context, pageURL, selector string, scale float64) ([]Shot, error) {
	var boxes []box
	var shots []Shot

	measure := chromedp.Evaluate(fmt.Sprintf(`
		[...document.querySelectorAll(%q)].map((el) => {
			const r = el.getBoundingClientRect();
			return { X: r.left + scrollX, Y: r.top + scrollY, W: r.width, H: r.height };
		})`, selector), &boxes)

	capture := chromedp.ActionFunc(func(ctx context.Context) error {
		viewports, err := clips(boxes, selector, scale)
		if err != nil {
			return err
		}
		for i, v := range viewports {
			// Resize the viewport to the element so the capture is never
			// clipped by the window. The device scale factor stays 1 — the clip
			// carries the scale, and setting both multiplies them.
			if err := emulation.SetDeviceMetricsOverride(int64(v.Width), int64(v.Height), 1, false).Do(ctx); err != nil {
				return fmt.Errorf("set viewport for element %d: %w", i+1, err)
			}
			png, err := page.CaptureScreenshot().WithCaptureBeyondViewport(true).WithClip(v).Do(ctx)
			if err != nil {
				return fmt.Errorf("capture element %d: %w", i+1, err)
			}
			shots = append(shots, Shot{Index: i + 1, PNG: png})
		}
		return nil
	})

	if err := c.run(ctx, pageURL, measure, capture); err != nil {
		return nil, err
	}
	return shots, nil
}
