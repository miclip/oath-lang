//go:build !(js && wasm)

package main

// isWasm is false for every native build: main() runs the ordinary CLI. The
// wasm build replaces this (see wasm.go), where main() parks so JavaScript can
// call the exported entry point.
var isWasm = false
