// Package testkit holds the shared test harnesses every component uses (T17): a fake clock, a
// private home directory, valid-by-default ping builders, a deterministic combat-log session
// generator, golden files, a canary key for leak tests, a real in-process API server and the
// faultapi adversarial server (subpackage).
//
// Only _test.go files may import it (internal/archtest enforces this). Example:
//
//	func TestUpload_WhenServerSlow_GivesUp(t *testing.T) {
//		clock := testkit.NewFakeClock()
//		home := testkit.TempHome(t)
//		ping := testkit.APing(testkit.WithContacts(testkit.AContact(testkit.WithFixes(testkit.AFix()))))
//		testkit.AssertNoCanary(t, logs.String())
//		_, _, _ = clock, home, ping
//	}
package testkit

// T is the part of *testing.T the helpers use; tests of the helpers pass a recorder instead.
type T interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Name() string
}
