// Package muda exposes the bundled delivery-waste reference content.
package muda

import "embed"

// Content contains the standard, recipes, and skill distribution documentation.
//
//go:embed standard recipes skills
var Content embed.FS
