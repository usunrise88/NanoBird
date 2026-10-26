// Package tools implements the built-in tools and their output limits (SPEC §6.1,
// §6.3). A tool describes itself to the model and runs one call. A failed call
// is an answer the model reads, so an error from a tool goes back as the result
// and the run goes on.
package tools
