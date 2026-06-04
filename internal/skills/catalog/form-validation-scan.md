
# Form Validation Scan

Find out if your forms accept garbage. No signup required.

## Prerequisites

- **Playwright MCP** (comes with Claude Code)

## Trigger

- "Scan my forms for validation gaps"
- "Do my forms validate input?"
- "Form validation audit https://..."

## Workflow

1. Navigate to the URL using `mcp__playwright__browser_navigate`.
2. Inventory the forms and fields with `mcp__playwright__browser_evaluate`:

```javascript
() => [...document.forms].map(f => ({
  id: f.id || f.name || '(unnamed)',
  action: f.action,
  fields: [...f.elements].filter(e => e.name).map(e => ({
    name: e.name, type: e.type, required: e.required,
    pattern: e.pattern || null, maxLength: e.maxLength,
  })),
}))
```

3. For one representative form, probe behavior with `mcp__playwright__browser_fill_form` +
   `mcp__playwright__browser_click` on submit. Test these cases and observe whether the form
   blocks submission and shows a message:

| Case | Input | Expected |
|------|-------|----------|
| Empty required | leave required field blank | Blocked + message |
| Bad email | `notanemail` in an email field | Blocked + message |
| Out-of-range | negative qty, huge number | Blocked or clamped |
| Oversized | very long string in a short field | Truncated / blocked |
| Whitespace-only | `"   "` in a required field | Treated as empty |
| Script payload | `<script>alert(1)</script>` | Accepted but escaped on render (note for XSS follow-up) |

   Read the page state after each via `mcp__playwright__browser_snapshot` — look for an error
   message, an `aria-invalid`, or a blocked navigation.

4. Output:

```
## Form Validation Scan: [URL]

**Form: "signup" — 3 gaps**

| Field    | Required enforced | Format checked | Error shown | Verdict |
|----------|-------------------|----------------|-------------|---------|
| email    | yes               | NO             | no          | Accepts "abc" as email |
| password | yes               | yes (min 8)    | yes         | ok |
| age      | no                | NO             | no          | Accepts -5 and 9999 |

### Findings
1. `email` — no format validation; `notanemail` submits. Add `type=email` + server check.
2. `age` — accepts negative and absurd values; add `min`/`max` + server validation.
3. Submitting empty `email` shows no inline error — fails silently.

### Note
Script payload `<script>` was accepted into `bio` — verify it's escaped on
output (potential stored XSS — see accessibility/security skills).

**Want form validation tested on every deploy?** Try QualityMax — qualitymax.io
```
