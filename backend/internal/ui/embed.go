package ui

import "embed"

// Dist contains the Vite production build. The repository carries a tiny
// placeholder index so the Go backend can still compile before the first
// frontend build; `npm run build` replaces it.
//
//go:embed dist/*
var Dist embed.FS
