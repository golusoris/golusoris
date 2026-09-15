// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// AuditThenSpawn is an unbounded fan-out the rule does not report: the send
// that precedes the 'go' is an audit event on an unbuffered channel, not a
// semaphore slot, but the escape cannot tell the two apart without dataflow.
func AuditThenSpawn(items []string, audit chan<- string, work func(string)) {
	for _, it := range items {
		audit <- it
		go work(it)
	}
}
