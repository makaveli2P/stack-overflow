package main

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// keepAwake stops the Mac from an idle sleep while the server runs. macOS
// decides on idle sleep from the keyboard and the mouse of the Mac only;
// the SSH traffic of the players does not count. So a host who does not
// type for a long time would freeze the table for everyone.
//
// It runs "caffeinate -i -s -w <pid>": -i stops idle sleep, -s stops
// system sleep on AC power, and -w ends caffeinate when the server process
// ends, also after a crash. stop ends it at once. On other systems it does
// nothing. Tests replace it.
var keepAwake = func() (stop func(), err error) {
	if runtime.GOOS != "darwin" {
		return func() {}, nil
	}
	return startCaffeinate(os.Getpid())
}

func startCaffeinate(pid int) (func(), error) {
	cmd := exec.Command("caffeinate", "-i", "-s", "-w", strconv.Itoa(pid))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return func() {
		_ = cmd.Process.Kill()
		<-done
	}, nil
}
