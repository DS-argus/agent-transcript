//go:build !darwin

package harness

import "fmt"

func ProcessIdentity(pid int) (string, error) {
	return "", fmt.Errorf("GJC automatic identity verification is not implemented on this platform; currently requires macOS")
}
