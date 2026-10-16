package main

import (
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
	"gorm.io/gorm"
)

type buttonMapping struct {
	ButtonID  string `gorm:"primaryKey;size:100"`
	RouteID   uint   `gorm:"not null;index"`
	UpdatedAt time.Time
}
type siteRoute struct {
	RouteID            uint `gorm:"column:route_id"`
	Name, Path, Status string
	Sort               int
}
type button struct {
	ID, Label, Group, Fallback, Source string
	RouteID                            uint
	Warning                            string
}

var buttonIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,99}$`)

// Only explicit public paths are selectable. Wildcard/regex routes need a concrete route first.
func concreteRoute(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "*[](){}\\^$|\r\n\t?#") {
		return false
	}
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" {
		return false
	}
	p := u.Path
	if strings.ContainsAny(p, "\\\r\n\t") || strings.HasPrefix(p, "//") {
		return false
	}
	if p == "/admin" || strings.HasPrefix(p, "/admin/") {
		return false
	}
	return true
}
func activeRoutes(db *gorm.DB) ([]siteRoute, error) {
	var rows []siteRoute
	if err := db.Table("routes").Select("route_id, name, path, status, sort").Where("status = ?", "active").Order("sort asc, name asc, route_id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]siteRoute, 0, len(rows))
	for _, row := range rows {
		if concreteRoute(row.Path) {
			out = append(out, row)
		}
	}
	return out, nil
}
func readMappings(db *gorm.DB) ([]buttonMapping, error) {
	var rows []buttonMapping
	err := db.Table(mappingsTable).Find(&rows).Error
	return rows, err
}
func mappedDestinations(db *gorm.DB) (map[string]string, error) {
	mappings, err := readMappings(db)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if len(mappings) == 0 {
		return out, nil
	}
	routes, err := activeRoutes(db)
	if err != nil {
		return nil, err
	}
	paths := map[uint]string{}
	for _, r := range routes {
		paths[r.RouteID] = r.Path
	}
	for _, m := range mappings {
		if path := paths[m.RouteID]; path != "" {
			out[m.ButtonID] = path
		}
	}
	return out, nil
}
func saveMappings(db *gorm.DB, buttons []button, values map[string]uint) error {
	known := map[string]bool{}
	for _, b := range buttons {
		known[b.ID] = true
	}
	routes, err := activeRoutes(db)
	if err != nil {
		return err
	}
	allowed := map[uint]bool{}
	for _, r := range routes {
		allowed[r.RouteID] = true
	}
	for id, route := range values {
		if !known[id] {
			return fmt.Errorf("Unknown template button %q. Reload this page before saving.", id)
		}
		if route != 0 && !allowed[route] {
			return fmt.Errorf("The selected route for %q is unavailable. Choose an active route.", id)
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for id, route := range values {
			if route == 0 {
				if err := tx.Table(mappingsTable).Where("button_id = ?", id).Delete(&buttonMapping{}).Error; err != nil {
					return err
				}
				continue
			}
			if err := tx.Table(mappingsTable).Save(&buttonMapping{ButtonID: id, RouteID: route, UpdatedAt: time.Now()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Discover the annotated navigation links in the site's pw templates. Symlinks are not followed.
func discoverButtons(templateDir string) ([]button, error) {
	root := filepath.Join(templateDir, "pw")
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("Cannot find the pw theme at %s: %w", root, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("The pw theme must be a regular directory.")
	}
	var buttons []button
	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root && (d.Name() == "assets" || d.Name() == "licenses" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(d.Name(), ".html") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			return err
		}
		if stat.Size() > 2<<20 {
			return fmt.Errorf("Template %s exceeds the 2 MB discovery limit.", d.Name())
		}
		z := html.NewTokenizer(f)
		for {
			kind := z.Next()
			if kind == html.ErrorToken {
				if z.Err() != io.EOF {
					return z.Err()
				}
				break
			}
			if kind != html.StartTagToken {
				continue
			}
			token := z.Token()
			if token.Data != "a" {
				continue
			}
			id := attribute(token, "data-pw-link")
			if id == "" {
				continue
			}
			if !buttonIDPattern.MatchString(id) {
				return fmt.Errorf("Invalid button identifier %q in %s.", id, path)
			}
			if seen[id] {
				return fmt.Errorf("Duplicate button identifier %q. Each placement needs its own ID.", id)
			}
			seen[id] = true
			label := attribute(token, "data-pw-label")
			if label == "" {
				return fmt.Errorf("Button %q needs a data-pw-label.", id)
			}
			source, _ := filepath.Rel(root, path)
			buttons = append(buttons, button{ID: id, Label: label, Group: attribute(token, "data-pw-group"), Fallback: attribute(token, "href"), Source: filepath.ToSlash(source)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(buttons, func(i, j int) bool {
		if buttons[i].Group != buttons[j].Group {
			return buttons[i].Group < buttons[j].Group
		}
		return buttons[i].ID < buttons[j].ID
	})
	return buttons, nil
}
func attribute(t html.Token, name string) string {
	for _, a := range t.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}
