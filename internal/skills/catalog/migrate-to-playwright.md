# Migrate to Playwright

Migrate an existing test-automation framework from Cypress or Selenium to
Playwright. Analyze the existing suite, map patterns, convert tests one-by-one
with AI-powered code generation and validation.

## Workflow

1. **Inventory** — list every spec file, page object, fixture, and custom
   command in the source framework. Record the runner config (timeouts,
   retries, base URL, env handling).
2. **Map idioms** — translate source constructs to Playwright equivalents:
   - Cypress `cy.get().click()` → `await page.locator(...).click()`
   - Selenium `WebDriverWait` → Playwright auto-waiting / `expect().toBeVisible()`
   - custom commands → fixtures or helper functions
3. **Convert** — port one spec at a time. Prefer role/text locators over CSS
   where the source allowed it. Replace implicit sleeps with web-first
   assertions.
4. **Validate** — run each converted spec with the qmax test tools and confirm
   parity against the original before deleting the source spec.
5. **Report** — summarize coverage delta, flaky-risk reductions, and any tests
   that could not be mechanically converted.

Use the qmax QA tools (list, run, generate, review) to execute and verify each
converted spec as you go — do not consume model tokens re-running suites you can
run through qmax.
