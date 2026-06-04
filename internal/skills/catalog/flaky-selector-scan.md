# Flaky Selector Scan

Scan a UI test suite (Playwright, Cypress, Selenium) for brittle locators that
will break on the next redesign, and suggest stable replacements.

## Workflow

1. **Find UI tests** (`*.spec.ts`, `*.cy.js`, Selenium page objects) and extract
   locators from `page.locator`, `getBy*`, `$(...)`, `find_element`, `cy.get`.
2. **Classify each by fragility**:
   - **Absolute XPath** — `/html/body/div[2]/…`, breaks on any DOM reshuffle.
   - **Positional CSS** — `:nth-child(3)`, breaks when order/count changes.
   - **Generated class** — `.css-1a2b3c`, `.MuiBox-root-42`, changes every build.
   - **Deep descendant** — `div div span.label`, coupled to layout nesting.
   - **Index-based** — `.locator('button').nth(4)`, breaks when buttons are added.
   Stable (good): `getByRole(..., { name })`, `data-test`/`data-testid`,
   `getByLabel`, stable `id`, ARIA roles.
3. **For each brittle locator** give `file:line`, the fragility class, and a
   concrete stable alternative (often: add a `data-test` attribute and target it).
4. **Report** ranked by risk, plus a per-file count so the worst suites stand out.

Prefer role- and test-id-based locators in every suggestion — they survive
restyles and copy changes that positional selectors can't.
