//go:build !darwin

package player

// Everywhere that is not macOS there is no Neural Engine to ask and no Metal
// to ask it with, so the portable estimate is the only path. It is named in
// the log for the same reason the accelerated one is: a converter that quietly
// fell back would look identical from the outside except for being worse.
func newConverter(modelPath string, maxShift, radius int, logf func(string, ...any)) converter {
	if modelPath != "" {
		logf("  a depth model was given, but only macOS can run one here")
	}
	return &cueConverter{maxShift: maxShift, radius: radius}
}
