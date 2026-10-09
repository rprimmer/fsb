package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// openBrowser runs `open`. It returns promptly, so its exit status says
// whether the application was found.
func openBrowser(name string, args []string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
