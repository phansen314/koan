// Package migrations holds koan's migration steps, in order, as data
// (implementation-spec.md, migrate; design-spec.md, Migrations).
//
// Each step has a number, a name, and, for each kind of file it covers, the
// schema it reads, the JSON Schema and rules of that older format, and a pure
// function that converts one file's ordered tree. Nothing here does I/O.
package migrations
