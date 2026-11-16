package server

import "regexp"

// Device-attribute requests are questions to a live terminal, not screen state.
// Replaying one can produce a late DA reply after tmux's request timed out;
// tmux 3.2a then forwards that reply into the user's shell as input.
var replayDeviceQuery = regexp.MustCompile("\x1b\\[[>=]?0*c")

func screenReplay(data []byte) []byte {
	return replayDeviceQuery.ReplaceAll(data, nil)
}
