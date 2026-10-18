package thumbnails

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// CaptureTimeout bounds a single screenshot run, including browser startup.
const CaptureTimeout = 45 * time.Second

// defaultSelector is used when Target.Selector is empty. It covers the
// dashboard grid, a chart card and the Ant Design content area.
const defaultSelector = ".dashboard-page, .chart-card, main"

// defaultHideCSS removes the application chrome before taking the picture.
const defaultHideCSS = `.app-sider, .app-header, .ant-layout-sider, .ant-layout-header { display:none !important }
.ant-layout-content { padding: 0 !important; margin: 0 !important; }
.chart-card, .dashboard-tile, .ant-card { border-radius: 0 !important; box-shadow: none !important; }`

// renderSettleWait gives ECharts time to finish its entry animation.
const renderSettleWait = 800 * time.Millisecond

// Target describes one screenshot request.
type Target struct {
	URL           string
	Selector      string
	Width         int
	Height        int
	Token         string
	WaitSelectors []string
	HideSelectors []string
}

// Capture renders the target page in a headless browser and returns PNG bytes.
func Capture(ctx context.Context, t Target) ([]byte, error) {
	if strings.TrimSpace(t.URL) == "" {
		return nil, fmt.Errorf("thumbnails: target URL is required")
	}
	browser, err := findBrowser()
	if err != nil {
		return nil, fmt.Errorf("thumbnails: %w", err)
	}
	width, height := t.Width, t.Height
	if width <= 0 {
		width = 1440
	}
	if height <= 0 {
		height = 900
	}
	selector := strings.TrimSpace(t.Selector)
	if selector == "" {
		selector = defaultSelector
	}

	runCtx, cancel := context.WithTimeout(ctx, CaptureTimeout)
	defer cancel()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browser),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("force-device-scale-factor", "1"),
		chromedp.Flag("window-size", fmt.Sprintf("%d,%d", width, height)),
		// Edge on Windows relaunches itself through a "compat layer" and the
		// first process exits before printing the DevTools address. Skipping
		// the relaunch keeps the process we started (Chrome ignores the flag).
		chromedp.Flag("edge-skip-compat-layer-relaunch", true),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(runCtx, opts...)
	defer cancelAlloc()
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()

	if err := injectSession(tabCtx, t, width, height); err != nil {
		return nil, fmt.Errorf("thumbnails: prepare %s with %s: %w", t.URL, browser, err)
	}

	var buf []byte
	// Navigation runs on its own: a same-document redirect from the SPA swaps
	// the execution context, and polling in the same batch can hit the old id.
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
	if err := chromedp.Run(tabCtx,
		chromedp.Evaluate(hideCSSExpression(t.HideSelectors), nil),
		chromedp.Sleep(renderSettleWait),
	); err != nil {
		return nil, fmt.Errorf("thumbnails: prepare %s with %s: %w", t.URL, browser, err)
	}

	if err := captureElement(tabCtx, selector, width, height, &buf); err != nil {
		return nil, fmt.Errorf("thumbnails: screenshot %s with %s: %w", t.URL, browser, err)
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("thumbnails: screenshot %s with %s returned no data", t.URL, browser)
	}
	return buf, nil
}

// runWithRetry retries once when the execution context was replaced by a
// navigation between two CDP calls.
func runWithRetry(ctx context.Context, actions ...chromedp.Action) error {
	err := chromedp.Run(ctx, actions...)
	if err == nil || !strings.Contains(err.Error(), "Cannot find context") {
		return err
	}
	time.Sleep(200 * time.Millisecond)
	return chromedp.Run(ctx, actions...)
}

// maxCaptureHeight caps the viewport growth for very tall dashboards.
const maxCaptureHeight = 6000

// elementRect is a DOMRect subset.
type elementRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// captureElement shoots one element.
//
// chromedp.Screenshot relies on Page.captureScreenshot with
// captureBeyondViewport=true, which hangs on current Chromium builds when the
// node is taller than the viewport. We instead grow the viewport to fit the
// element and capture an explicit clip.
func captureElement(ctx context.Context, selector string, width, height int, out *[]byte) error {
	rect, err := measureElement(ctx, selector)
	if err != nil {
		return err
	}
	if rect.Width <= 0 || rect.Height <= 0 {
		return fmt.Errorf("selector %q has an empty box", selector)
	}

	needW := int(math.Ceil(rect.X + rect.Width))
	needH := int(math.Ceil(rect.Y + rect.Height))
	if needH > maxCaptureHeight {
		needH = maxCaptureHeight
	}
	if needW < width {
		needW = width
	}
	if needH < height {
		needH = height
	}
	if needW != width || needH != height {
		if err := chromedp.Run(ctx,
			chromedp.EmulateViewport(int64(needW), int64(needH), chromedp.EmulateScale(1)),
			chromedp.Sleep(400*time.Millisecond),
		); err != nil {
			return fmt.Errorf("resize viewport: %w", err)
		}
		// Layout may reflow after the resize.
		if rect, err = measureElement(ctx, selector); err != nil {
			return err
		}
	}

	clip := &page.Viewport{
		X:      math.Round(rect.X),
		Y:      math.Round(rect.Y),
		Width:  math.Round(rect.Width),
		Height: math.Round(rect.Height),
		Scale:  1,
	}
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		buf, err := page.CaptureScreenshot().
			WithFormat(page.CaptureScreenshotFormatPng).
			WithFromSurface(true).
			WithCaptureBeyondViewport(false).
			WithClip(clip).
			Do(ctx)
		if err != nil {
			return err
		}
		*out = buf
		return nil
	}))
}

// measureElement returns the bounding rect of the first match.
//
// A comma separated list is tried left to right: document.querySelector would
// otherwise return the first element in *document order*, which makes a broad
// fallback such as "main" win over the more specific selector.
func measureElement(ctx context.Context, selector string) (elementRect, error) {
	candidates := strings.Split(selector, ",")
	var lastErr error
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		rect, err := measureOne(ctx, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		if rect.Width > 0 && rect.Height > 0 {
			return rect, nil
		}
	}
	if lastErr != nil {
		return elementRect{}, lastErr
	}
	return elementRect{}, fmt.Errorf("measure %q: no element found", selector)
}

func measureOne(ctx context.Context, selector string) (elementRect, error) {
	js := fmt.Sprintf(`(function() {
  var el = document.querySelector(%s);
  if (!el) { return null; }
  el.scrollIntoView({block: 'start', inline: 'start'});
  var r = el.getBoundingClientRect();
  return {x: r.x, y: r.y, width: r.width, height: r.height};
})()`, jsonString(selector))
	var rect elementRect
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &rect)); err != nil {
		return elementRect{}, fmt.Errorf("measure %q: %w", selector, err)
	}
	return rect, nil
}

// injectSession opens the site origin, stores the access token in localStorage
// so the SPA boots authenticated, and applies the viewport.
func injectSession(ctx context.Context, t Target, width, height int) error {
	origin, err := originOf(t.URL)
	if err != nil {
		return err
	}
	actions := []chromedp.Action{
		chromedp.EmulateViewport(int64(width), int64(height), chromedp.EmulateScale(1)),
	}
	// Navigating the origin is only needed to own the localStorage scope; a
	// failure here is not fatal because the target navigation retries.
	if strings.TrimSpace(t.Token) != "" {
		actions = append(actions,
			chromedp.Navigate(origin),
			chromedp.Evaluate(tokenExpression(t.Token), nil),
		)
	}
	return chromedp.Run(ctx, actions...)
}

// tokenExpression stores the bearer token in the SPA's localStorage.
func tokenExpression(token string) string {
	return fmt.Sprintf(`(function() {
  window.localStorage.setItem('access_token', %s);
  window.localStorage.setItem('refresh_token', %s);
  return true;
})()`, jsonString(token), jsonString(token))
}

// originOf returns scheme://host[:port] for a URL.
func originOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid target URL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid target URL %q: absolute URL required", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// presentExpression builds a Poll expression testing that a selector matches.
// Poll wraps the expression in "return (...)", so no explicit return here.
func presentExpression(selector string) string {
	return fmt.Sprintf("!!document.querySelector(%s)", jsonString(selector))
}

// hideCSSExpression injects the stylesheet hiding page chrome.
func hideCSSExpression(extra []string) string {
	css := defaultHideCSS
	for _, sel := range extra {
		sel = strings.TrimSpace(sel)
		if sel == "" {
			continue
		}
		css += "\n" + sel + " { display:none !important; }"
	}
	return fmt.Sprintf(`(function() {
  var id = 'superset-thumbnail-style';
  var el = document.getElementById(id);
  if (!el) { el = document.createElement('style'); el.id = id; (document.head || document.documentElement).appendChild(el); }
  el.textContent = %s;
  return true;
})()`, jsonString(css))
}

// jsonString renders s as a JSON string literal, safe inside JS.
func jsonString(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(raw)
}
