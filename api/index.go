// Package handler exposes the PR Tombstone API as a single Vercel Go
// Function. The implementation lives in a module-owned package so Vercel's
// generated handler package never imports pr-tombstone/internal directly.
package handler

import (
	"net/http"

	"pr-tombstone/vercelapp"
)

// Handler is the Vercel Go Runtime entrypoint.
func Handler(w http.ResponseWriter, r *http.Request) {
	vercelapp.Handler(w, r)
}
