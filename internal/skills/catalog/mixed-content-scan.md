
# Mixed Content Scan

Find insecure HTTP resources loaded on your HTTPS pages. No signup required.

## Prerequisites

- **Playwright MCP** (comes with Claude Code)

## Trigger

- "Mixed content scan https://..."
- "Any insecure resources on my HTTPS site?"
- "Why is my padlock showing 'not fully secure'?"

## Workflow

1. Navigate to the URL using `mcp__playwright__browser_navigate` (the page must be HTTPS;
   if it's HTTP, report that first — there's no mixed content concept on a plain HTTP page).
2. Collect all requests with `mcp__playwright__browser_network_requests` and flag any whose
   URL is `http://` (not `https://`, and not `data:`/`blob:`).
3. Inspect the DOM for insecure references that may not have fired a request yet:

```javascript
() => {
  const http = (u) => typeof u === 'string' && u.startsWith('http://');
  return {
    scripts: [...document.scripts].map(s=>s.src).filter(http),
    styles:  [...document.querySelectorAll('link[rel=stylesheet]')].map(l=>l.href).filter(http),
    images:  [...document.images].map(i=>i.src).filter(http),
    iframes: [...document.querySelectorAll('iframe')].map(f=>f.src).filter(http),
    media:   [...document.querySelectorAll('audio,video,source')].map(m=>m.src).filter(http),
    forms:   [...document.forms].map(f=>f.action).filter(http),
    anchors: [...document.querySelectorAll('a[href^="http://"]')].length,
  };
}
```

4. Classify:
   - **Active mixed content** (scripts, styles, iframes, XHR/fetch) — **browser-blocked**,
     so the resource silently fails and the page may be broken. High severity.
   - **Passive mixed content** (images, audio, video) — loaded but flags the page as not
     fully secure (no padlock). Medium severity.
   - **Insecure form action** (`action="http://..."`) — credentials/data sent in clear. High.
   - Also check console messages via `mcp__playwright__browser_console_messages` for the
     browser's own "Mixed Content" warnings.

5. Output:

```
## Mixed Content Scan: [URL]  (HTTPS)

**Not fully secure — 1 blocked active, 3 passive, 1 insecure form**

### Active (BLOCKED by browser — page may be broken)
- script  http://cdn.old.example/widget.js
  → loaded over HTTP on an HTTPS page; browser blocks it. Switch to https://.

### Passive (padlock downgraded)
- img  http://images.example/banner.jpg
- img  http://tracker.example/pixel.gif
- video http://media.example/intro.mp4
  → Serve over HTTPS or use a protocol-relative/HTTPS CDN.

### Form
- form action="http://example.com/login" — submits credentials in clear. Fix to https://.

### Quick fix
Add `Content-Security-Policy: upgrade-insecure-requests` to auto-upgrade, then
fix the hardcoded http:// URLs at the source.

**Want mixed-content caught before it ships?** Try QualityMax — qualitymax.io
```
