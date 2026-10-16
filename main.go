package main

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/rob121/cannon/extension"
	"gorm.io/gorm"
)

const (
	extensionName        = "cannon-ext-powwow"
	extensionTitle       = "Pow Wow"
	extensionDescription = "Event dates, annual theme, countdown, and template button routing."
	extensionVersion     = "1.2.0"
	updateURLBase        = "https://github.com/rob121/cannon-ext-powwow/releases/download"
	defaultAdminMenu     = "Pow Wow"

	settingsTable    = "powwow_settings"
	mappingsTable    = "powwow_button_routes"
	managePermission = extensionName + ".manage"
)

//go:embed templates/*/*.html
var templateFiles embed.FS

//go:embed help/*.md
var helpFiles embed.FS

//go:embed assets/blocks.css
var assetFiles embed.FS

type app struct {
	mu          sync.Mutex
	db          *gorm.DB
	templateDir string
	ready       bool
	open        func() (*gorm.DB, string, error)
	templates   *extension.Templates
	now         func() time.Time
}

func newApp() *app {
	return &app{
		templates: extension.NewTemplates(templateFiles, "templates"), now: time.Now,
		open: func() (*gorm.DB, string, error) {
			db, site, err := extension.OpenDB()
			if err != nil {
				return nil, "", err
			}
			return db, site.TemplateDir, nil
		},
	}
}

func main() {
	if err := newServer(newApp()).Run(); err != nil {
		log.Fatal(err)
	}
}

func newServer(a *app) *extension.Server {
	s := extension.New(extension.Info{
		Name:          extensionName,
		Title:         extensionTitle,
		Description:   extensionDescription,
		Version:       extensionVersion,
		UpdateURLBase: updateURLBase,
		AdminMenuName: defaultAdminMenu,
	})
	s.EmbedTemplates(templateFiles, "templates")
	s.EmbedHelp(helpFiles, "help")
	s.RegisterPermissions([]extension.PermissionDef{{ID: "manage", DisplayName: "Manage Pow Wow", Description: "Edit event details and template button routes."}})
	s.OnInstall(func(extension.WireRequest) error { _, _, err := a.database(); return err })
	s.OnConfiguration(a)
	s.HandleAdmin("/admin", a.admin)
	s.RegisterBlock(extension.BlockDefinition{ID: "important-dates", Title: "Important Date", Spaces: []string{"event-details", "above-main", "sidebar", "below-main"}}, a.block)
	s.RegisterBlock(extension.BlockDefinition{ID: "countdown", Title: "Countdown", Spaces: []string{"countdown", "topbar", "sidebar", "above-main", "below-main"}}, a.block)
	s.OnHook("onPrepareDocumentHead", a.head)
	s.OnHook("onAfterRender", a.afterRender)
	return s
}

func (a *app) database() (*gorm.DB, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ready {
		return a.db, a.templateDir, nil
	}
	if a.db == nil {
		db, dir, err := a.open()
		if err != nil {
			return nil, "", err
		}
		a.db = db
		a.templateDir = dir
	}
	if err := extension.MigrateConfigurationStore(a.db, settingsTable); err != nil {
		return nil, "", err
	}
	if err := a.db.Table(mappingsTable).AutoMigrate(&buttonMapping{}); err != nil {
		return nil, "", err
	}
	a.ready = true
	return a.db, a.templateDir, nil
}

func (a *app) render(name string, data any) extension.WireResponse {
	body, err := a.templates.Execute("powwow/"+name+".html", data)
	if err != nil {
		log.Printf("powwow template: %v", err)
		return extension.Error(http.StatusInternalServerError, "Unable to render Pow Wow content.")
	}
	return extension.HTML(http.StatusOK, body)
}

func (a *app) block(item string, req extension.WireRequest) extension.WireResponse {
	if item != "important-dates" && item != "countdown" {
		return extension.Error(http.StatusNotFound, "Unknown Pow Wow block.")
	}
	settings, err := a.loadSettings()
	if err != nil {
		log.Printf("powwow block: %v", err)
		return extension.OK()
	}
	if item == "important-dates" {
		dates := settings.dates()
		if len(dates) == 0 {
			return extension.OK()
		}
		return a.render("important-dates", map[string]any{"Dates": dates})
	}
	days, visible := daysUntil(settings.PowWowDate, settings.Timezone, a.now())
	if !visible {
		return extension.OK()
	}
	return a.render("countdown", map[string]any{"Days": days, "Date": settings.PowWowDate, "ThemeName": settings.ThemeName})
}

func (a *app) head(req extension.HookWireRequest) extension.HookWireResponse {
	if extension.HookString(req.Arguments, "context") != "frontend" {
		return extension.HookOK(nil)
	}
	css, err := assetFiles.ReadFile("assets/blocks.css")
	if err != nil {
		return extension.HookOK(nil)
	}
	return extension.HookOK(map[string]any{"head_html": extension.HookHeadHTML(req.Arguments) + "\n<style id=\"pw-extension-styles\">" + string(css) + "</style>"})
}

func (a *app) afterRender(req extension.HookWireRequest) extension.HookWireResponse {
	args := req.Arguments
	body, ok := args["body"].(string)
	if !ok || !isPowWowPage(body) || extension.HookString(args, "body_encoding") == "base64" || isAdminLayout(extension.HookString(args, "layout")) {
		return extension.HookOK(nil)
	}
	settings, err := a.loadSettings()
	if err != nil {
		log.Printf("powwow render settings: %v", err)
		return extension.HookOK(nil)
	}
	db, _, err := a.database()
	if err != nil {
		return extension.HookOK(nil)
	}
	destinations, err := mappedDestinations(db)
	if err != nil {
		log.Printf("powwow render routes: %v", err)
		return extension.HookOK(nil)
	}
	updated, err := rewritePage(body, settings, destinations, a.now())
	if err != nil {
		log.Printf("powwow render markup: %v", err)
		return extension.HookOK(nil)
	}
	return extension.HookOK(map[string]any{"body": updated})
}

func internalError(err error) extension.WireResponse {
	log.Printf("powwow: %v", err)
	return extension.Error(http.StatusInternalServerError, "Unable to load Pow Wow settings. Check the extension log.")
}

func csrfField(req extension.WireRequest) template.HTML {
	return template.HTML(extension.CSRFHiddenField(req))
}
func fieldError(label string) error {
	return fmt.Errorf("%s must be a valid date in YYYY-MM-DD format.", label)
}
