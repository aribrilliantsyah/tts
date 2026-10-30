// Package web embeds the frontend so the server ships as one binary.
package web

import "embed"

//go:embed *.html *.js *.css live vendor fonts sounds icons
var FS embed.FS
