package check

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// ChromeBrowser evaluates the probe in headless Chrome over CDP.
//
// The PDF path shells out to the browser binary instead, because printing needs
// no round trip. Checking does: it has to read measurements back, which is what
// CDP is for.
type ChromeBrowser struct {
	// Timeout bounds the whole navigate-and-evaluate cycle.
	Timeout time.Duration
}

// NewChromeBrowser returns a browser with a sensible timeout.
func NewChromeBrowser() *ChromeBrowser { return &ChromeBrowser{Timeout: 60 * time.Second} }

// awaitPromise lets an Evaluate call resolve a promise before returning.
func awaitPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

// Eval navigates to pageURL, waits for fonts to settle, then evaluates script
// and decodes the result into out.
func (c *ChromeBrowser) Eval(ctx context.Context, pageURL, script string, out any) error {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	// --no-sandbox and --disable-dev-shm-usage are what make this work inside
	// CI containers and act, matching the PDF adapter's flags.
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

	runCtx, cancelRun := context.WithTimeout(browserCtx, timeout)
	defer cancelRun()

	// document.fonts.ready matters: measuring before the web font loads reports
	// the fallback's metrics, which is the very bug the font-fallback rule
	// exists to catch.
	err := chromedp.Run(runCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body"),
		chromedp.Evaluate(`document.fonts.ready.then(() => true)`, nil, awaitPromise),
		chromedp.Evaluate(script, out),
	)
	if err != nil {
		return fmt.Errorf("chrome: %w", err)
	}
	return nil
}
