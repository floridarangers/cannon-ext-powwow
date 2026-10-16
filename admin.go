package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/rob121/cannon/extension"
)

const adminPath = "/admin/extension-apps/" + extensionName

func (a *app) admin(req extension.WireRequest) extension.WireResponse {
	if !extension.UserAuthenticated(req) || !extension.UserCan(req, managePermission) {
		return extension.Error(http.StatusForbidden, "You need the Manage Pow Wow permission.")
	}
	if req.Method != "GET" && req.Method != "POST" {
		return extension.Error(http.StatusMethodNotAllowed, "Method not allowed.")
	}
	if req.Method == "GET" {
		return a.adminPage(req, "", "", nil, nil)
	}
	if len(req.Body) > 256<<10 {
		return extension.Error(http.StatusRequestEntityTooLarge, "The form is too large.")
	}
	form, err := url.ParseQuery(req.Body)
	if err != nil {
		return extension.Error(http.StatusBadRequest, "Invalid form.")
	}
	if req.CSRF == "" || subtle.ConstantTimeCompare([]byte(req.CSRF), []byte(form.Get("_csrf"))) != 1 {
		return extension.Error(http.StatusForbidden, "Your form has expired. Reload the page and try again.")
	}
	switch form.Get("action") {
	case "settings":
		s := eventSettings{ThemeName: form.Get("theme_name"), ThemeDescription: form.Get("theme_description"), Workday: form.Get("workday"), EarlyRegistrationDeadline: form.Get("early_registration_deadline"), OnlineRegistrationClose: form.Get("online_registration_close"), PowWowDate: form.Get("powwow_date"), Timezone: form.Get("timezone")}
		if err := a.saveSettings(s); err != nil {
			return a.adminPage(req, "", err.Error(), &s, nil)
		}
	case "mappings":
		db, dir, err := a.database()
		if err != nil {
			return internalError(err)
		}
		buttons, err := discoverButtons(dir)
		if err != nil {
			return a.adminPage(req, "", err.Error(), nil, nil)
		}
		values := map[string]uint{}
		for key, entries := range form {
			if !strings.HasPrefix(key, "route.") {
				continue
			}
			if len(entries) != 1 {
				return extension.Error(http.StatusBadRequest, "Duplicate route field.")
			}
			n, err := strconv.ParseUint(entries[0], 10, 32)
			if err != nil {
				return a.adminPage(req, "", "Choose a valid route for every button.", nil, nil)
			}
			values[strings.TrimPrefix(key, "route.")] = uint(n)
		}
		if err := saveMappings(db, buttons, values); err != nil {
			return a.adminPage(req, "", err.Error(), nil, values)
		}
	default:
		return extension.Error(http.StatusBadRequest, "Unknown form action.")
	}
	// POST/redirect/GET prevents a refresh from resubmitting changes.
	return extension.Redirect(http.StatusSeeOther, adminPath+"?saved="+url.QueryEscape(form.Get("action")))
}

func (a *app) adminPage(req extension.WireRequest, notice, errMsg string, submitted *eventSettings, pending map[string]uint) extension.WireResponse {
	db, dir, err := a.database()
	if err != nil {
		return internalError(err)
	}
	s, err := a.loadSettings()
	if err != nil {
		return internalError(err)
	}
	if submitted != nil {
		s = *submitted
	}
	buttons, discoveryErr := discoverButtons(dir)
	routes, err := activeRoutes(db)
	if err != nil {
		return internalError(err)
	}
	mappings, err := readMappings(db)
	if err != nil {
		return internalError(err)
	}
	selected := map[string]uint{}
	for _, m := range mappings {
		selected[m.ButtonID] = m.RouteID
	}
	for id, n := range pending {
		selected[id] = n
	}
	routeIDs := map[uint]bool{}
	for _, r := range routes {
		routeIDs[r.RouteID] = true
	}
	for i := range buttons {
		buttons[i].RouteID = selected[buttons[i].ID]
		if buttons[i].RouteID != 0 && !routeIDs[buttons[i].RouteID] {
			buttons[i].Warning = "This route was removed, disabled, or is no longer a concrete public path. A placeholder (#) is being used."
		}
	}
	if u, err := url.Parse(req.URL); err == nil && req.Method == "GET" {
		switch u.Query().Get("saved") {
		case "settings":
			notice = "Event settings saved."
		case "mappings":
			notice = "Button routes saved."
		}
	}
	discoveryWarning := ""
	if discoveryErr != nil {
		discoveryWarning = discoveryErr.Error()
	}
	day, visible := daysUntil(s.PowWowDate, s.Timezone, a.now())
	countdown := "Hidden — no upcoming Pow Wow date."
	if visible {
		countdown = fmt.Sprintf("%d days until Pow Wow", day)
		if day == 1 {
			countdown = "1 day until Pow Wow"
		}
		if day == 0 {
			countdown = "0 days — Pow Wow is today."
		}
	}
	resp := a.render("admin", map[string]any{"Settings": s, "Buttons": buttons, "Routes": routes, "Notice": notice, "Error": errMsg, "DiscoveryWarning": discoveryWarning, "Countdown": countdown, "CSRFField": csrfField(req), "AdminURL": adminPath})
	if errMsg != "" && resp.StatusCode == http.StatusOK {
		resp.StatusCode = http.StatusUnprocessableEntity
	}
	return resp
}
