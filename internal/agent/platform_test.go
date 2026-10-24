package agent

import "runtime"

func testLongCommand() string {
	if runtime.GOOS == "windows" {
		return `ping -n 31 127.0.0.1 >NUL`
	}
	return "sleep 30"
}

func testDelayedCommand() string {
	if runtime.GOOS == "windows" {
		return `echo one & ping -n 2 127.0.0.1 >NUL & echo two & ping -n 31 127.0.0.1 >NUL`
	}
	return "echo one; sleep 0.3; echo two; sleep 30"
}
