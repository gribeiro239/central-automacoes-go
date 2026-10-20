// Package webui keeps the local panel inside the application executable.
package webui

import "embed"

//go:embed assets/*
var files embed.FS

func Read(name string) ([]byte, error) { return files.ReadFile("assets/" + name) }
