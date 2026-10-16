# Cannon Pow Wow extension

Companion to the `pw` frontend template. Provides annual event settings, Important Date and Countdown blocks, and admin route mapping for 36 static template links.

## Build and enable

The source lives at `/Users/ralfonso/go/src/github.com/rob121/cannon-ext-powwow`. The companion theme lives at `/Users/ralfonso/tmp/cannon/data/pow-wow/templates/pw`.

This project follows the sibling Cannon extensions: `go.mod` uses `replace github.com/rob121/cannon => ../cannon`. Keep the Cannon checkout next to this folder. Use Go 1.26.4 or newer and a C compiler for the SQLite driver.

```sh
go test ./...
go vet ./...
go build -o bin/cannon-ext-powwow .
```

1. Copy the built `bin/cannon-ext-powwow` executable into the site's configured extensions directory. Build on the deployment platform; the included local build is macOS, not a Linux deployment binary.
2. Discover/install and enable **Pow Wow** in Cannon's Extensions admin. Cannon starts it with its site configuration and Unix socket arguments and creates the extension's tables.
3. Open **Admin → Pow Wow**. Grant **Manage Pow Wow** (`cannon-ext-powwow.manage`) to non-administrator roles that need access.
4. Enter the event details and save. The same event fields are exposed through Cannon's extension Configuration interface.
5. Map template buttons to existing active routes and save.
6. Add the blocks in Cannon's Blocks admin and select the desired visibility/routes.

Implementation and local binary are complete. The extension has not been copied into a live extensions directory or activated. No live dates, routes, or block placements were changed.

## Event settings

- Theme name: replaces the default homepage hero heading; blank preserves the original.
- Theme description: replaces the default hero introduction; plain text with line breaks. Blank preserves the original.
- Pow Wow Workday.
- Early Registration Deadline Date.
- Online Registration Close Date.
- Pow Wow Date.
- Event timezone: defaults to `America/New_York`; accepts IANA timezone names.

Dates are optional `YYYY-MM-DD` calendar dates. No sample event dates are saved on installation. A custom block replacing the whole hero space takes precedence over the default hero text.

## Blocks

| Block title | Item ID | Suggested placement |
|---|---|---|
| Important Date | `important-dates` | `event-details`, `above-main`, `sidebar`, `below-main` |
| Countdown | `countdown` | `countdown` (default homepage hero), `topbar`, `sidebar`, `above-main`, `below-main` |

**Important Date** shows all configured dates in the field order above, including past dates, omitting unset fields. If every date is blank, it emits no content.

**Countdown** uses calendar days in the event timezone, including daylight saving changes. It shows **0 days — Pow Wow is today!** on event day. Once the difference is negative, or the date is unset, it emits no content. It is calculated when Cannon renders a page; configure any full-page cache to expire by local midnight if exact midnight rollover matters.

Use Cannon's plain block wrapper and leave the placement title blank to avoid an empty heading after the countdown ends. The extension supplies responsive, class-scoped CSS through `onPrepareDocumentHead`.

## Button routing

The admin scans the site's `pw` HTML templates for annotated links. Each placement has its own stable ID, human label, section, and source file. Repeated labels (for example “Plan your visit”) are independently configurable.

```html
<a href="/original-destination"
   data-pw-link="hero-plan-your-visit"
   data-pw-label="Plan your visit"
   data-pw-group="Hero">Plan your visit</a>
```

Choose **No link (#)** to clear an assignment. Unmapped buttons (and mappings whose routes are disabled, deleted, or no longer concrete) render as `href="#"`. Route IDs are stored, so editing a route's path automatically updates its buttons. Unavailable mappings also show a warning in the admin. Only active, concrete public paths are selectable; wildcard/regex routes and admin paths are excluded. Create a specific route for dynamic destinations first. URL/redirect routes work through their public Cannon path.

The page must include `data-pw-site="powwow"`; the supplied layout does. The extension rewrites only annotated anchor destinations and the `data-pw-field="theme-name"` / `theme-description` text fields in rendered HTML. No browser JavaScript or Cannon core modification is required. Other markup is preserved. Native menus, article links, categories, skip links, the home logo, and back-to-top remain controlled by their existing behavior.

If a compression extension uses `onAfterRender`, order Pow Wow before compression. Already-compressed responses are deliberately left untouched. Other extensions that replace annotated markup should likewise run after Pow Wow or preserve the markers.

When adding links, use a unique lowercase identifier of at most 100 characters (`a-z`, digits, hyphens, underscores); keep it stable when changing the label. Symlinks are not followed during discovery.

## Storage and templates

The extension writes only `powwow_settings` and `powwow_button_routes` in the selected site's database. Core `routes` are read-only. Admin POSTs require a signed-in user with the manage permission and a matching CSRF token. Invalid dates and unavailable mappings are rejected before saving.

Embedded templates can be overridden through Cannon at:

- `extension/powwow/admin.html`
- `extension/powwow/important-dates.html`
- `extension/powwow/countdown.html`

Help is also available through Cannon's aggregated Help area. Disabling the extension preserves its stored settings and restores the theme's original link destinations and default hero text.

## Validation

Automated tests cover calendar boundaries and timezones, date validation, empty/past countdowns, HTML escaping, route changes and fallback, atomic mapping validation, template discovery, permission/CSRF checks, admin saving, and extension wire endpoints. All database tests use temporary SQLite databases.

Optional companion check against an actual template directory:

```sh
PW_TEMPLATE_DIR=/Users/ralfonso/tmp/cannon/data/pow-wow/templates go test ./... -count=1
```

The paired theme was also rendered through Cannon's native template engine for home, news, modules, and article views. A rendered homepage was passed through the extension hook to verify annual text and route overrides. The admin and block previews were checked in a browser, including narrow layouts.

## Top bar countdown

The default blue top bar keeps “Outdoors. Together. With Purpose.” on the left and shows the days until the configured Pow Wow Date on the right. This is automatic when `cannon-ext-powwow` 1.1.0 or newer is enabled; no block placement is required. It shows 0 on event day and no countdown when the date is blank or past. The configured event timezone determines the day. An assigned `topbar` block still replaces the whole bar. Restart/reload the extension after installing the updated binary.
