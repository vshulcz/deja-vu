// Package jsonout defines the stable schema version for deja CLI JSON envelopes.
package jsonout

// Version 2 made the search envelope unconditional and added the fields a
// caller needs to interpret a result set rather than only read it: which tier
// answered, how many sessions matched before the cap, and whether the cap hid
// any. Until then the exact path emitted a bare array while every fallback path
// emitted an object, so a consumer had to handle two shapes — and could not
// tell relevance fallback from real matches at all.
const Version = 2
