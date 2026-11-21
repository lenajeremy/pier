package router

import (
	"embed"
	"encoding/base64"
	"strings"
)

// The page Caddy answers with at an address nothing is deployed at, and the
// fonts it inlines. The fonts are Red Hat Mono and Schibsted Grotesk, both
// under the SIL Open Font License.
//
//go:embed pages/notfound.html pages/*.woff2
var pages embed.FS

// notFoundText is the answer for anything that isn't a browser: curl,
// scripts and health checks get one plain line.
const notFoundText = "Nothing is deployed at this address.\n"

// notFoundPage is the page with its fonts inlined, built once. It loads
// nothing from anywhere else, so it works on a server with no way out.
var notFoundPage = func() string {
	b, err := pages.ReadFile("pages/notfound.html")
	if err != nil {
		return notFoundText
	}
	page := string(b)
	for marker, file := range map[string]string{
		"{{MONO}}":    "pages/mono-500.woff2",
		"{{SANS400}}": "pages/sans-400.woff2",
		"{{SANS500}}": "pages/sans-500.woff2",
	} {
		font, err := pages.ReadFile(file)
		if err != nil {
			return notFoundText
		}
		page = strings.ReplaceAll(page, marker, base64.StdEncoding.EncodeToString(font))
	}
	return page
}()
