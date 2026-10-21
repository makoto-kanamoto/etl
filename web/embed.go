// Package web embarque l'interface (dashboard, wallet, langues) dans le binaire du nœud.
package web

import "embed"

// FS contient les fichiers servis par internal/api. Les tests et données de test y figurent mais ne
// sont jamais servis (filtrés par le serveur).
//
//go:embed i18n wallet dashboard
var FS embed.FS
