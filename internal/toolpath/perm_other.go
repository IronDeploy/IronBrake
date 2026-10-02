//go:build !windows

package toolpath

// checkWindows só existe no Windows; o checkFile não o chama em outros sistemas.
func checkWindows(_, real string) (string, error) { return real, nil }
