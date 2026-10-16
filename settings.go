package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rob121/cannon/extension"
)

const defaultTimezone = "America/New_York"

type eventSettings struct {
	ThemeName                 string `json:"theme_name"`
	ThemeDescription          string `json:"theme_description"`
	Workday                   string `json:"workday"`
	EarlyRegistrationDeadline string `json:"early_registration_deadline"`
	OnlineRegistrationClose   string `json:"online_registration_close"`
	PowWowDate                string `json:"powwow_date"`
	Timezone                  string `json:"timezone"`
}
type eventDate struct{ Label, ISO, Display string }

func normalizeSettings(s eventSettings) (eventSettings, error) {
	s.ThemeName = strings.TrimSpace(s.ThemeName)
	s.ThemeDescription = strings.TrimSpace(s.ThemeDescription)
	s.Timezone = strings.TrimSpace(s.Timezone)
	if s.Timezone == "" {
		s.Timezone = defaultTimezone
	}
	if utf8.RuneCountInString(s.ThemeName) > 180 {
		return s, fmt.Errorf("Theme name must be 180 characters or fewer.")
	}
	if utf8.RuneCountInString(s.ThemeDescription) > 2000 {
		return s, fmt.Errorf("Theme description must be 2,000 characters or fewer.")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return s, fmt.Errorf("Choose a valid IANA timezone, for example America/New_York.")
	}
	fields := []struct {
		label string
		value *string
	}{{"Pow Wow Workday", &s.Workday}, {"Early Registration Deadline Date", &s.EarlyRegistrationDeadline}, {"Online Registration Close Date", &s.OnlineRegistrationClose}, {"Pow Wow Date", &s.PowWowDate}}
	for _, f := range fields {
		*f.value = strings.TrimSpace(*f.value)
		if *f.value == "" {
			continue
		}
		t, err := time.Parse(time.DateOnly, *f.value)
		if err != nil || t.Format(time.DateOnly) != *f.value || t.Year() < 1 {
			return s, fieldError(f.label)
		}
	}
	return s, nil
}
func (s eventSettings) dates() []eventDate {
	rows := []eventDate{{Label: "Pow Wow Workday", ISO: s.Workday}, {Label: "Early Registration Deadline Date", ISO: s.EarlyRegistrationDeadline}, {Label: "Online Registration Close Date", ISO: s.OnlineRegistrationClose}, {Label: "Pow Wow Date", ISO: s.PowWowDate}}
	out := make([]eventDate, 0, 4)
	for _, d := range rows {
		if t, err := time.Parse(time.DateOnly, d.ISO); err == nil {
			d.Display = t.Format("Monday, January 2, 2006")
			out = append(out, d)
		}
	}
	return out
}

// Convert local calendar dates to UTC date ordinals; elapsed hours are wrong at DST boundaries.
func daysUntil(date, zone string, now time.Time) (int, bool) {
	if zone == "" {
		zone = defaultTimezone
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return 0, false
	}
	event, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return 0, false
	}
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	days := int(event.Unix()/86400 - today.Unix()/86400)
	return days, days >= 0
}
func (a *app) loadSettings() (eventSettings, error) {
	db, _, err := a.database()
	if err != nil {
		return eventSettings{}, err
	}
	data, err := extension.DBConfigurationStore(db, settingsTable).Load("event")
	if err != nil {
		return eventSettings{}, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return eventSettings{}, err
	}
	var s eventSettings
	if err = json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	return normalizeSettings(s)
}
func (a *app) saveSettings(s eventSettings) error {
	s, err := normalizeSettings(s)
	if err != nil {
		return err
	}
	db, _, err := a.database()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(s)
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		return err
	}
	return extension.DBConfigurationStore(db, settingsTable).Save("event", data)
}
func (a *app) Configuration() (extension.ConfigurationDocument, error) {
	s, err := a.loadSettings()
	if err != nil {
		return extension.ConfigurationDocument{}, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return extension.ConfigurationDocument{}, err
	}
	return extension.ConfigurationDocument{Sections: []extension.ConfigurationSection{{ID: "event", Title: "Pow Wow event", Schema: eventSchema, UISchema: eventUI, Data: raw}}}, nil
}
func (a *app) SaveConfiguration(req extension.ConfigurationSaveRequest) error {
	if req.Section != "event" {
		return fmt.Errorf("Unknown Pow Wow configuration section.")
	}
	if len(req.Data) == 0 || string(req.Data) == "null" {
		return fmt.Errorf("Event settings are required.")
	}
	var s eventSettings
	if err := json.Unmarshal(req.Data, &s); err != nil {
		return fmt.Errorf("Invalid event settings: %w", err)
	}
	return a.saveSettings(s)
}

var eventSchema = json.RawMessage(`{
 "type":"object","properties":{
 "theme_name":{"type":"string","title":"Theme name","maxLength":180},
 "theme_description":{"type":"string","title":"Theme description","maxLength":2000},
 "workday":{"type":"string","title":"Pow Wow Workday","anyOf":[{"format":"date"},{"const":""}],"description":"Optional date in YYYY-MM-DD format; leave blank to omit."},
 "early_registration_deadline":{"type":"string","title":"Early Registration Deadline Date","anyOf":[{"format":"date"},{"const":""}],"description":"Optional date in YYYY-MM-DD format; leave blank to omit."},
 "online_registration_close":{"type":"string","title":"Online Registration Close Date","anyOf":[{"format":"date"},{"const":""}],"description":"Optional date in YYYY-MM-DD format; leave blank to omit."},
 "powwow_date":{"type":"string","title":"Pow Wow Date","anyOf":[{"format":"date"},{"const":""}],"description":"Optional date in YYYY-MM-DD format; leave blank to omit."},
 "timezone":{"type":"string","title":"Event timezone","default":"America/New_York","description":"IANA timezone used to determine today for the countdown."}
 }}`)
var eventUI = json.RawMessage(`{"type":"VerticalLayout","elements":[
 {"type":"Control","scope":"#/properties/theme_name"},
 {"type":"Control","scope":"#/properties/theme_description","options":{"multi":true}},
 {"type":"Control","scope":"#/properties/workday"},
 {"type":"Control","scope":"#/properties/early_registration_deadline"},
 {"type":"Control","scope":"#/properties/online_registration_close"},
 {"type":"Control","scope":"#/properties/powwow_date"},
 {"type":"Control","scope":"#/properties/timezone"}]}`)
