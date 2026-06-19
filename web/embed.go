package webui

import "embed"

// Files contains the Vite production build. Run `make build-web` before Go builds.
//
//go:embed dist dist/assets/*
var Files embed.FS
