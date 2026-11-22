// Package search implements the deal query language and deterministic
// search engine: a small human-readable query
// language, typed structured filters, concept/alias expansion and
// explainable matching over an internal offer representation.
//
// The engine is deliberately isolated from Discord, Tjek API clients and
// persistence: it operates only on SearchOffer values. Upstream offer
// types are adapted at the integration boundary.
//
// Everything is deterministic. There are no embeddings, no vector search,
// no LLM classification and no fuzzy matching beyond the explicitly
// documented token/phrase rules in this package.
package search
