package i18n

import "embed"

// LocalesFS embeds the backend translation packs.
//
//go:embed all:locales
var LocalesFS embed.FS
