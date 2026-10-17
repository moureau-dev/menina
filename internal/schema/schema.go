// Package schema embeds the published Menina manifest JSON Schema.
package schema

import _ "embed"

const (
	// ManifestVersion is the schema version the client understands.
	ManifestVersion = 1
	// ManifestURL is the public schema URL.
	ManifestURL = "https://menina.moureau.dev/schema/menina.schema.json"
)

// Manifest is the embedded .menina.yml JSON Schema.
//
//go:embed menina.schema.json
var Manifest []byte
