package service

// SwapCompareHash replaces the bcrypt comparison used by Login and returns a
// function that restores it. Tests use it to observe whether every login path
// pays the bcrypt cost (F-2) without relying on wall-clock timing.
func SwapCompareHash(f func(hash, password []byte) error) (restore func()) {
	orig := compareHashAndPassword
	compareHashAndPassword = f
	return func() { compareHashAndPassword = orig }
}
