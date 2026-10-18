package thumbnails

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// PrintTimeout bounds a single PDF render (browser startup included).
const PrintTimeout = 60 * time.Second

// A4 landscape, in inches.
const (
	a4LandscapeWidth  = 11.69
	a4LandscapeHeight = 8.27
	a4PortraitWidth   = 8.27
	a4PortraitHeight  = 11.69
)

// CapturePDF renders the target page and prints it to a PDF.
//
// The same headless pipeline as Capture drives the render, so the print output
// matches the screenshot: authenticated SPA boot, the chrome hidden through
// defaultHideCSS, and a settle wait for ECharts animations.
func CapturePDF(ctx context.Context, t Target, landscape bool) ([]byte, error) {
	if strings.TrimSpace(t.URL) == "" {
		return nil, fmt.Errorf("thumbnails: target URL is required")
	}
	browser, err := findBrowser()
	if err != nil {
		return nil, fmt.Errorf("thumbnails: %w", err)
	}
	width, height := t.Width, t.Height
	if width <= 0 {
		width = 1600
	}
	if height <= 0 {
		height = 1000
	}
	// Printing covers the whole document, so the selector only gates "is the
	// page ready yet".
	selector := strings.TrimSpace(t.Selector)
	if selector == "" {
		selector = defaultSelector
	}

	runCtx, cancel := context.WithTimeout(ctx, PrintTimeout)
	defer cancel()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browser),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("force-device-scale-factor", "1"),
		chromedp.Flag("window-size", fmt.Sprintf("%d,%d", width, height)),
		chromedp.Flag("edge-skip-compat-layer-relaunch", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(runCtx, opts...)
	defer cancelAlloc()
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()

	if err := injectSession(tabCtx, t, width, height); err != nil {
		return nil, fmt.Errorf("thumbnails: prepare %s with %s: %w", t.URL, browser, err)
	}
	if err := chromedp.Run(tabCtx, chromedp.Navigate(t.URL)); err != nil {
		return nil, fmt.Errorf("thumbnails: open %s with %s: %w", t.URL, browser, err)
	}
	if err := runWithRetry(tabCtx, chromedp.Poll("document.readyState === 'complete'", nil,
		chromedp.WithPollingTimeout(20*time.Second),
		chromedp.WithPollingInterval(100*time.Millisecond))); err != nil {
		return nil, fmt.Errorf("thumbnails: wait for %s with %s: %w", t.URL, browser, err)
	}
	for _, wait := range t.WaitSelectors {
		wait := strings.TrimSpace(wait)
		if wait == "" {
			continue
		}
		if err := runWithRetry(tabCtx, chromedp.Poll(presentExpression(wait), nil,
			chromedp.WithPollingTimeout(15*time.Second),
			chromedp.WithPollingInterval(200*time.Millisecond))); err != nil {
			return nil, fmt.Errorf("thumbnails: wait for %q on %s with %s: %w", wait, t.URL, browser, err)
		}
	}
	if err := runWithRetry(tabCtx, chromedp.Poll(presentExpression(selector), nil,
		chromedp.WithPollingTimeout(15*time.Second),
		chromedp.WithPollingInterval(200*time.Millisecond))); err != nil {
		return nil, fmt.Errorf("thumbnails: wait for %q on %s with %s: %w", selector, t.URL, browser, err)
	}

	// Force the print media query so the app can flatten its layout, hide the
	// application chrome and top up the height to the full content.
	if err := chromedp.Run(tabCtx, chromedp.Evaluate(printPrepareExpression(selector, t.HideSelectors), nil)); err != nil {
		return nil, fmt.Errorf("thumbnails: prepare print of %s with %s: %w", t.URL, browser, err)
	}
	if err := chromedp.Run(tabCtx, chromedp.Sleep(renderSettleWait)); err != nil {
		return nil, fmt.Errorf("thumbnails: settle print of %s with %s: %w", t.URL, browser, err)
	}

	var buf []byte
	err = chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		paperWidth, paperHeight := a4PortraitWidth, a4PortraitHeight
		if landscape {
			paperWidth, paperHeight = a4LandscapeWidth, a4LandscapeHeight
		}
		data, _, err := page.PrintToPDF().
			WithPrintBackground(true).
			WithLandscape(landscape).
			WithPaperWidth(paperWidth).
			WithPaperHeight(paperHeight).
			WithMarginTop(0.2).
			WithMarginBottom(0.2).
			WithMarginLeft(0.2).
			WithMarginRight(0.2).
			WithPreferCSSPageSize(false).
			Do(ctx)
		if err != nil {
			return err
		}
		buf = data
		return nil
	}))
	if err != nil {
		return nil, fmt.Errorf("thumbnails: print %s with %s: %w", t.URL, browser, err)
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("thumbnails: print %s with %s returned no data", t.URL, browser)
	}
	return buf, nil
}

// printPrepareExpression hides the chrome, flattens scrolling and prints the
// document at its natural size (chromedp's PrintToPDF would otherwise clip a
// dashboard taller than one A4 page).
func printPrepareExpression(selector string, extra []string) string {
	css := defaultHideCSS + `
html, body { height: auto !important; overflow: visible !important; background: #fff !important; }
.dashboard-page { height: auto !important; overflow: visible !important; }
.dashboard-canvas { height: auto !important; overflow: visible !important; }
.ant-tabs-content, .ant-tabs-tabpane { height: auto !important; }
@page { margin: 0; }`
	for _, sel := range extra {
		sel = strings.TrimSpace(sel)
		if sel == "" {
			continue
		}
		css += "\n" + sel + " { display:none !important; }"
	}
	return fmt.Sprintf(`(function() {
  var id = 'superset-print-style';
  var el = document.getElementById(id);
  if (!el) { el = document.createElement('style'); el.id = id; (document.head || document.documentElement).appendChild(el); }
  el.textContent = %s;
  var target = document.querySelector(%s);
  if (target) {
    // The tabbed canvas keeps inactive panes at height 0; expanding the active
    // one keeps every row visible in the printed document.
    target.style.height = 'auto';
    target.style.overflow = 'visible';
  }
  return true;
})()`, jsonString(css), jsonString(selector))
}
