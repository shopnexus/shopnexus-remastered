// Package idtest installs a fixed id cipher for tests. The codec is a process
// global, so every test binary that marshals or parses an ID needs one — this
// exists so that setup is two lines instead of six in each package.
package idtest

import "shopnexus/internal/shared/id"

// Key is fixed so encodings are reproducible across packages and a test can pin
// an exact wire string.
const Key = "0123456789abcdef0123456789abcdef"

// Install sets the process-wide cipher, panicking on failure: a test binary that
// cannot encode ids has nothing left to test.
//
//	func TestMain(m *testing.M) { idtest.Install(); m.Run() }
func Install() {
	if err := id.SetCipher([]byte(Key)); err != nil {
		panic(err)
	}
}
