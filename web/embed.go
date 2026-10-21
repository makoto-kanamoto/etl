// Package web embarque l'interface (dashboard, wallet, langues) dans le binaire du nœud.
package web

import "embed"

// FS contient les fichiers servis par internal/api. Les tests et données de test y figurent mais ne
// sont jamais servis (filtrés par le serveur). « all: » est indispensable : sans lui, go:embed
// ignore les fichiers dont le nom commence par « _ » (modules _blake.js, _md.js, _u64.js de noble).
//
//go:embed i18n all:wallet dashboard
var FS embed.FS
