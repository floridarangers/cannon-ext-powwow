package main

import (
	"fmt"
	htmlstd "html"
	"io"
	"strings"
	"time"

	"golang.org/x/net/html"
)

func isAdminLayout(layout string) bool { return strings.HasPrefix(layout, "admin/") }
func isPowWowPage(body string) bool    { return strings.Contains(body, `data-pw-site="powwow"`) }

// Change only annotated start tags / theme text. Preserve scripts, styles, comments,
// forms, whitespace and all unrelated markup byte-for-byte.
func rewritePage(body string, settings eventSettings, destinations map[string]string, now time.Time) (string, error) {
	z := html.NewTokenizer(strings.NewReader(body))
	var out strings.Builder
	out.Grow(len(body))
	skipTag := ""
	depth := 0
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return "", z.Err()
			}
			if depth != 0 {
				return "", fmt.Errorf("unclosed theme text element")
			}
			break
		}
		raw := string(z.Raw())
		if depth > 0 {
			if kind == html.StartTagToken || kind == html.EndTagToken {
				t := z.Token()
				if t.Data == skipTag {
					if kind == html.StartTagToken {
						depth++
					} else {
						depth--
						if depth == 0 {
							out.WriteString(raw)
						}
					}
				}
			}
			continue
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			out.WriteString(raw)
			continue
		}
		token := z.Token()
		changed := false
		if token.Data == "a" {
			if id := attribute(token, "data-pw-link"); id != "" {
				existing := attribute(token, "href")
				path := "#"
				if mapped := destinations[id]; mapped != "" && concreteRoute(mapped) {
					path = mapped
				} else if strings.HasPrefix(existing, "#") && len(existing) > 1 {
					// Keep in-page anchors (e.g. #pw-event-strip) when unmapped.
					path = existing
				}
				found := false
				for i := range token.Attr {
					if token.Attr[i].Key == "href" {
						token.Attr[i].Val = path
						found = true
					}
				}
				if !found {
					token.Attr = append(token.Attr, html.Attribute{Key: "href", Val: path})
				}
				changed = true
			}
		}
		replacement := ""
		replaceEmpty := false
		switch attribute(token, "data-pw-field") {
		case "theme-name":
			replacement = settings.ThemeName
		case "theme-description":
			replacement = settings.ThemeDescription
		case "countdown":
			replaceEmpty = true
			if days, visible := daysUntil(settings.PowWowDate, settings.Timezone, now); visible {
				switch days {
				case 0:
					replacement = "0 days — Pow Wow is today!"
				case 1:
					replacement = "1 day until Pow Wow"
				default:
					replacement = fmt.Sprintf("%d days until Pow Wow", days)
				}
			}
		}
		if (replacement != "" || replaceEmpty) && kind == html.StartTagToken && (token.Data == "h1" || token.Data == "h2" || token.Data == "p" || token.Data == "span" || token.Data == "div") {
			out.WriteString(raw)
			out.WriteString(htmlstd.EscapeString(replacement))
			skipTag = token.Data
			depth = 1
			continue
		}
		if changed {
			out.WriteString(token.String())
		} else {
			out.WriteString(raw)
		}
	}
	return out.String(), nil
}
