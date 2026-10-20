// Package migrations embeds the database schema so the binary can migrate from any directory.
package migrations

import _ "embed"

//go:embed 001_initial.sql
var Initial string

//go:embed 002_scheduling.sql
var Scheduling string
