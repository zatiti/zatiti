// Command zatiti root module placeholder.
//
// This file exists so the root module has at least one buildable package:
// the pre-commit test gate runs `go test ./...`, which matches no packages
// while every root file is build-tag gated. The frozen dependency families
// stay behind the `tools` build tag in tools_deps.go until real package
// imports replace that anchor (ADR 001, dependencies.lock.json).
package zatiti
