//go:build !darwin && !linux

package guard

func listXattr(int) ([]string, error)                { return nil, nil }
func getXattr(int, string, int) ([]byte, int, error) { return nil, 0, nil }
