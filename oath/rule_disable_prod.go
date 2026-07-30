//go:build !conformance_mutation

package main

// ruleOn in a PRODUCTION build. There is no mutation state to consult and no way to
// return false: the compiler inlines this and folds every enforcement site into an
// unconditional check, so a shipped binary contains no code path capable of disabling
// a verification rule.
//
// The mutation build (rule_disable_mutation.go) replaces this with a switchable version. Only
// that build can measure conformance coverage, which is the correct trade: a scoring
// mechanism must not exist in the artifact whose guarantees it can switch off.
func ruleOn(string) bool { return true }

// reportVectorClaims is a no-op in a production build: measuring what the suite would
// catch requires disabling rules, which this build cannot do.
func reportVectorClaims([]vectorRecord) {}

// harnessCommands is empty in a production build, so `oath conformance-score` does
// not exist there — the command is absent, not merely refused.
var harnessCommands = map[string]func([]string){}
