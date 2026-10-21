// Package configfile embarque la configuration par défaut livrée (default.toml), écrite par
// « etl init » : le fichier du dépôt et celui créé chez l'opérateur sont toujours identiques.
package configfile

import _ "embed"

// Default est le contenu de config/default.toml.
//
//go:embed default.toml
var Default string
