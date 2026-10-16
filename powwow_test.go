package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rob121/cannon/extension"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func fixture(t *testing.T) *app {
	t.Helper()
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "test.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	if err = db.Table("routes").AutoMigrate(&siteRoute{}); err != nil {
		t.Fatal(err)
	}
	rows := []siteRoute{{RouteID: 1, Name: "Registration", Path: "/register", Status: "active"}, {RouteID: 2, Name: "Plan", Path: "/plan", Status: "active"}, {RouteID: 3, Name: "Disabled", Path: "/disabled", Status: "inactive"}, {RouteID: 4, Name: "Dynamic", Path: "/news/*", Status: "active"}}
	if err = db.Table("routes").Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "pw"), 0755); err != nil {
		t.Fatal(err)
	}
	markup := `<a data-pw-link="hero-register" data-pw-label="Get ready for Pow Wow" data-pw-group="Hero" href="https://powwow.ws/register-online">Register</a><a data-pw-link="hero-plan" data-pw-label="Plan your visit" data-pw-group="Hero" href="#plan">Plan</a>`
	if err = os.WriteFile(filepath.Join(dir, "pw", "layout.html"), []byte(markup), 0644); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	a.db = db
	a.templateDir = dir
	a.templates = a.templates.WithTemplateDir(dir)
	a.now = func() time.Time { return time.Date(2027, 3, 13, 17, 0, 0, 0, time.UTC) }
	if _, _, err = a.database(); err != nil {
		t.Fatal(err)
	}
	return a
}
func mustSave(t *testing.T, a *app, s eventSettings) {
	t.Helper()
	if err := a.saveSettings(s); err != nil {
		t.Fatal(err)
	}
}
func adminRequest(method string, form url.Values) extension.WireRequest {
	return extension.WireRequest{Method: method, URL: adminPath, CSRF: "test-token", Body: form.Encode(), User: map[string]any{"authenticated": true, "permissions": []any{managePermission}}}
}

func TestCalendarCountdown(t *testing.T) {
	cases := []struct {
		name, date, zone, now string
		want                  int
		visible               bool
	}{
		{"spring DST", "2027-03-15", "America/New_York", "2027-03-13T23:30:00-05:00", 2, true},
		{"fall DST", "2027-11-08", "America/New_York", "2027-11-06T23:30:00-04:00", 2, true},
		{"event day", "2027-03-13", "America/New_York", "2027-03-13T23:59:59-05:00", 0, true},
		{"past", "2027-03-12", "America/New_York", "2027-03-13T00:00:00-05:00", -1, false},
		{"local midnight", "2027-03-13", "America/Los_Angeles", "2027-03-13T01:00:00Z", 1, true},
		{"unset", "", "America/New_York", "2027-03-13T00:00:00Z", 0, false},
		{"invalid timezone", "2027-03-15", "Unknown/Place", "2027-03-13T00:00:00Z", 0, false},
		{"distant date", "9999-12-31", "UTC", "9999-12-30T00:00:00Z", 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, c.now)
			if err != nil {
				t.Fatal(err)
			}
			got, visible := daysUntil(c.date, c.zone, now)
			if got != c.want || visible != c.visible {
				t.Fatalf("got %d,%v; want %d,%v", got, visible, c.want, c.visible)
			}
		})
	}
}
func TestSettingsAndBlocks(t *testing.T) {
	a := fixture(t)
	for _, s := range []eventSettings{{PowWowDate: "2027-02-29"}, {Workday: "2027-2-01"}, {Timezone: "bad"}, {ThemeName: strings.Repeat("x", 181)}} {
		if a.saveSettings(s) == nil {
			t.Fatalf("accepted invalid settings: %#v", s)
		}
	}
	s := eventSettings{ThemeName: "  Strong & <brave>  ", ThemeDescription: "Together", Workday: "2027-03-01", EarlyRegistrationDeadline: "2027-03-05", OnlineRegistrationClose: "2027-03-12", PowWowDate: "2027-03-14"}
	mustSave(t, a, s)
	got, err := a.loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.ThemeName != "Strong & <brave>" || got.Timezone != defaultTimezone {
		t.Fatalf("normalization: %#v", got)
	}
	dates := a.block("important-dates", extension.WireRequest{})
	if dates.StatusCode != 200 || strings.Count(dates.Body, "<time ") != 4 {
		t.Fatalf("dates: %+v", dates)
	}
	count := a.block("countdown", extension.WireRequest{})
	if !strings.Contains(count.Body, "day until Pow Wow") || strings.Contains(count.Body, "<brave>") {
		t.Fatalf("countdown escaping/singular: %s", count.Body)
	}
	s.PowWowDate = "2027-03-13"
	mustSave(t, a, s)
	if !strings.Contains(a.block("countdown", extension.WireRequest{}).Body, "Pow Wow is today!") {
		t.Fatal("missing event day countdown")
	}
	s.PowWowDate = "2027-03-12"
	mustSave(t, a, s)
	if a.block("countdown", extension.WireRequest{}).Body != "" {
		t.Fatal("past countdown visible")
	}
	mustSave(t, a, eventSettings{})
	for _, id := range []string{"important-dates", "countdown"} {
		if a.block(id, extension.WireRequest{}).Body != "" {
			t.Fatal("unset dates visible")
		}
	}
	if err = a.SaveConfiguration(extension.ConfigurationSaveRequest{Section: "unknown", Data: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("unknown section accepted")
	}
	if err = a.SaveConfiguration(extension.ConfigurationSaveRequest{Section: "event", Data: json.RawMessage(`null`)}); err == nil {
		t.Fatal("null configuration accepted")
	}
	var names []string
	a.db.Raw("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name").Scan(&names)
	if strings.Join(names, ",") != "powwow_button_routes,powwow_settings,routes" {
		t.Fatalf("unexpected tables: %v", names)
	}
}
func TestRouteMappings(t *testing.T) {
	a := fixture(t)
	buttons, err := discoverButtons(a.templateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveMappings(a.db, buttons, map[string]uint{"hero-register": 1, "hero-plan": 2}); err != nil {
		t.Fatal(err)
	}
	if err = saveMappings(a.db, buttons, map[string]uint{"hero-register": 2, "hero-plan": 3}); err == nil {
		t.Fatal("inactive route accepted")
	}
	if err = saveMappings(a.db, buttons, map[string]uint{"unknown": 1}); err == nil {
		t.Fatal("unknown button accepted")
	}
	got, _ := mappedDestinations(a.db)
	if got["hero-register"] != "/register" {
		t.Fatal("failed save partially changed mappings")
	}
	a.db.Table("routes").Where("route_id = ?", 1).Update("path", "/tickets")
	got, _ = mappedDestinations(a.db)
	if got["hero-register"] != "/tickets" {
		t.Fatal("mapping did not follow route rename")
	}
	a.db.Table("routes").Where("route_id = ?", 1).Update("status", "inactive")
	got, _ = mappedDestinations(a.db)
	if _, ok := got["hero-register"]; ok {
		t.Fatal("disabled route remains mapped")
	}
	if err = saveMappings(a.db, buttons, map[string]uint{"hero-plan": 0}); err != nil {
		t.Fatal(err)
	}
	got, _ = mappedDestinations(a.db)
	if len(got) != 0 {
		t.Fatalf("expected fallback, got %v", got)
	}
	for _, path := range []string{"//evil.test", "https://evil.test", "javascript:alert(1)", "/news/*", "/admin", "/admin/settings", "/%2fexample.com", "/bad%0apath", "/bad\\path", "/news/(.*)"} {
		if concreteRoute(path) {
			t.Errorf("unsafe/non-concrete path accepted: %s", path)
		}
	}
	for _, path := range []string{"/", "/register", "/info/event-guide"} {
		if !concreteRoute(path) {
			t.Errorf("valid path rejected: %s", path)
		}
	}
}
func TestDiscovery(t *testing.T) {
	a := fixture(t)
	buttons, err := discoverButtons(a.templateDir)
	if err != nil || len(buttons) != 2 {
		t.Fatalf("discovery %v %v", buttons, err)
	}
	os.WriteFile(filepath.Join(a.templateDir, "pw", "duplicate.html"), []byte(`<a data-pw-link="hero-register" data-pw-label="Duplicate">`), 0644)
	if _, err = discoverButtons(a.templateDir); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	os.Remove(filepath.Join(a.templateDir, "pw", "duplicate.html"))
	outside := filepath.Join(t.TempDir(), "outside.html")
	os.WriteFile(outside, []byte(`<a data-pw-link="outside" data-pw-label="Outside">`), 0644)
	if err = os.Symlink(outside, filepath.Join(a.templateDir, "pw", "linked.html")); err != nil {
		t.Fatal(err)
	}
	buttons, err = discoverButtons(a.templateDir)
	if err != nil || len(buttons) != 2 {
		t.Fatal("followed external symlink")
	}
	if dir := os.Getenv("PW_TEMPLATE_DIR"); dir != "" {
		buttons, err = discoverButtons(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(buttons) != 36 {
			t.Fatalf("expected 36 companion links, got %d", len(buttons))
		}
	}
}
func TestPageRewrite(t *testing.T) {
	raw := `<!doctype html><body data-pw-site="powwow"><!--keep--><h1 data-pw-field="theme-name">Original <span>heading</span></h1><p data-pw-field="theme-description">Default</p><a class='button' data-pw-link="hero-register" href="https://powwow.ws/register-online">Register</a><a data-pw-link="hero-plan" href="#pw-event-strip">Plan</a><a href='/untouched'>Keep</a><script>const x = '<a href="/keep">';</script></body>`
	got, err := rewritePage(raw, eventSettings{}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `data-pw-link="hero-register" href="#"`) || strings.Contains(got, "powwow.ws") {
		t.Fatalf("unmapped external button should render #, got %s", got)
	}
	if !strings.Contains(got, `href="#pw-event-strip"`) {
		t.Fatalf("unmapped in-page anchor should be preserved, got %s", got)
	}
	if !strings.Contains(got, `<a href='/untouched'>Keep</a>`) || !strings.Contains(got, `<script>const x = '<a href="/keep">';</script>`) {
		t.Fatalf("unrelated markup changed: %s", got)
	}
	got, err = rewritePage(raw, eventSettings{ThemeName: `<script>alert(1)</script>`, ThemeDescription: "Faith & adventure"}, map[string]string{"hero-register": "/tickets"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`href="/tickets"`, `&lt;script&gt;alert(1)&lt;/script&gt;`, `Faith &amp; adventure`, `<a href='/untouched'>Keep</a>`, `<script>const x = '<a href="/keep">';</script>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "powwow.ws") {
		t.Fatal("mapped rewrite left powwow.ws")
	}
	a := fixture(t)
	mustSave(t, a, eventSettings{ThemeName: "Custom theme"})
	resp := a.afterRender(extension.HookWireRequest{Arguments: map[string]any{"body": raw, "layout": "pw/layout.html"}})
	if !strings.Contains(resp.Arguments["body"].(string), "Custom theme") {
		t.Fatal("hook did not rewrite")
	}
	for _, args := range []map[string]any{{"body": raw, "layout": "admin/layout.html"}, {"body": raw, "body_encoding": "base64"}, {"body": "<h1>Other theme</h1>"}} {
		if len(a.afterRender(extension.HookWireRequest{Arguments: args}).Arguments) != 0 {
			t.Fatal("unrelated response rewritten")
		}
	}
}
func TestAdminSecurityAndSaving(t *testing.T) {
	a := fixture(t)
	if a.admin(extension.WireRequest{Method: "GET"}).StatusCode != 403 {
		t.Fatal("anonymous admin access")
	}
	denied := adminRequest("GET", nil)
	denied.User["denied_permissions"] = []any{managePermission}
	if a.admin(denied).StatusCode != 403 {
		t.Fatal("explicit permission deny ignored")
	}
	form := url.Values{"action": {"settings"}, "theme_name": {"Annual adventure"}, "powwow_date": {"2027-03-14"}}
	if a.admin(adminRequest("POST", form)).StatusCode != 403 {
		t.Fatal("missing csrf accepted")
	}
	form.Set("_csrf", "test-token")
	resp := a.admin(adminRequest("POST", form))
	if resp.StatusCode != 303 {
		t.Fatalf("settings save failed: %+v", resp)
	}
	s, _ := a.loadSettings()
	if s.ThemeName != "Annual adventure" {
		t.Fatal("settings not persisted")
	}
	form.Set("powwow_date", "2027-02-29")
	form.Set("theme_name", "Keep my input")
	resp = a.admin(adminRequest("POST", form))
	if resp.StatusCode != 422 || !strings.Contains(resp.Body, "Keep my input") {
		t.Fatalf("validation failed: %+v", resp)
	}
	s, _ = a.loadSettings()
	if s.ThemeName != "Annual adventure" {
		t.Fatal("invalid settings written")
	}
	form = url.Values{"action": {"mappings"}, "_csrf": {"test-token"}, "route.hero-register": {"1"}, "route.hero-plan": {"2"}}
	if resp = a.admin(adminRequest("POST", form)); resp.StatusCode != 303 {
		t.Fatalf("mapping save: %+v", resp)
	}
	resp = a.admin(adminRequest("GET", nil))
	if resp.StatusCode != 200 || !strings.Contains(resp.Body, `value="1" selected`) || !strings.Contains(resp.Body, "Get ready for Pow Wow") {
		t.Fatalf("admin render: %+v", resp)
	}
}
func TestWireEndpoints(t *testing.T) {
	a := fixture(t)
	h := newServer(a).Handler()
	for _, path := range []string{"/capabilities", "/configuration", "/block", "/hooks", "/templates", "/help", "/health"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
			}
		})
	}
	mustSave(t, a, eventSettings{PowWowDate: "2027-03-14"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/block/countdown", strings.NewReader(`{"method":"GET"}`)))
	var resp extension.WireResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.Contains(resp.Body, "day until Pow Wow") {
		t.Fatalf("wire block: %s", w.Body.String())
	}
}

// Optional local integration: use actual pw templates and a native Cannon render.
// All database writes remain confined to the temporary fixture database.
func TestCompanionIntegration(t *testing.T) {
	dir := os.Getenv("PW_TEMPLATE_DIR")
	if dir == "" {
		t.Skip("set PW_TEMPLATE_DIR for companion integration")
	}
	a := fixture(t)
	a.templateDir = dir
	mustSave(t, a, eventSettings{ThemeName: "Adventure with purpose", ThemeDescription: "A weekend of faith, friendship, and outdoor adventure.", Workday: "2027-03-01", EarlyRegistrationDeadline: "2027-03-05", OnlineRegistrationClose: "2027-03-12", PowWowDate: "2027-03-14"})
	buttons, err := discoverButtons(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveMappings(a.db, buttons, map[string]uint{"hero-get-ready-for-pow-wow": 1, "hero-plan-your-visit": 2}); err != nil {
		t.Fatal(err)
	}
	admin := a.admin(adminRequest("GET", nil))
	if admin.StatusCode != 200 || strings.Count(admin.Body, `name="route.`) != 36 {
		t.Fatal("real template catalog failed to render in admin")
	}
	if file := os.Getenv("PW_RENDERED_PAGE"); file != "" {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		resp := a.afterRender(extension.HookWireRequest{Arguments: map[string]any{"body": string(body), "layout": "pw/layout.html"}})
		updated, _ := resp.Arguments["body"].(string)
		for _, want := range []string{"Adventure with purpose", `href="/register"`, `href="/plan"`} {
			if !strings.Contains(updated, want) {
				t.Fatalf("missing %q in native render", want)
			}
		}
	}
	if out := os.Getenv("PW_PREVIEW_DIR"); out != "" {
		if err = os.MkdirAll(out, 0755); err != nil {
			t.Fatal(err)
		}
		css, err := os.ReadFile(os.Getenv("PW_ADMIN_CSS"))
		if err != nil {
			t.Fatal(err)
		}
		shell := `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Pow Wow extension preview</title><style>` + string(css) + `body{padding:24px;font-family:system-ui;background:#f5f7fa}*{box-sizing:border-box} .pw-admin{margin:auto} </style><body><p style="text-align:center">Preview with sample event details — forms are disabled.</p>`
		// Disable preview submission so this standalone artifact cannot mutate site data.
		preview := strings.ReplaceAll(admin.Body, `type="submit"`, `type="button" disabled`)
		if err = os.WriteFile(filepath.Join(out, "admin.html"), []byte(shell+preview+`</body></html>`), 0644); err != nil {
			t.Fatal(err)
		}
		styles, _ := assetFiles.ReadFile("assets/blocks.css")
		blocks := `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Pow Wow block previews</title><style>body{font-family:system-ui;margin:0;color:#153b58;background:#fff9ef}main{max-width:1120px;margin:auto;padding:48px 24px}section.preview{padding:32px;background:#ffe458;margin:24px 0;border-radius:12px}` + string(styles) + `</style><body><main><h1>Pow Wow blocks</h1><p>Sample dates for preview.</p><section class="preview">` + a.block("important-dates", extension.WireRequest{}).Body + `</section><section class="preview">` + a.block("countdown", extension.WireRequest{}).Body + `</section></main></body></html>`
		if err = os.WriteFile(filepath.Join(out, "blocks.html"), []byte(blocks), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTopbarCountdown(t *testing.T) {
	a := fixture(t)
	raw := `<body data-pw-site="powwow"><div class="pw-topbar"><span>OUTDOORS. TOGETHER. WITH PURPOSE.</span><span class="pw-topbar-countdown" data-pw-field="countdown">Stale countdown</span></div></body>`
	for _, tc := range []struct{ date, want string }{{"2027-03-16", "3 days until Pow Wow"}, {"2027-03-14", "1 day until Pow Wow"}, {"2027-03-13", "0 days — Pow Wow is today!"}, {"2027-03-12", ""}, {"", ""}} {
		t.Run(tc.date, func(t *testing.T) {
			mustSave(t, a, eventSettings{PowWowDate: tc.date})
			resp := a.afterRender(extension.HookWireRequest{Arguments: map[string]any{"body": raw, "layout": "pw/layout.html"}})
			body, _ := resp.Arguments["body"].(string)
			want := `data-pw-field="countdown">` + tc.want + `</span>`
			if !strings.Contains(body, want) || strings.Contains(body, "Stale countdown") || !strings.Contains(body, "OUTDOORS. TOGETHER. WITH PURPOSE.") {
				t.Fatalf("incorrect topbar: %s", body)
			}
		})
	}
}
