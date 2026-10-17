package setup

import (
	"os"
	"strconv"
	"strings"
)

// processStart identifies the process pid's start: the kernel's boot id and the process's start time in clock ticks
// after boot (/proc/<pid>/stat, field 22), "" when unknown. The locks record it beside the pid, because a pid is
// reused: after a reboot, or in a new container whose first processes get the same small pids (start-server as
// Docker's pid 1, or tini's pid 1 beside it), a lock left by a killed server would otherwise read as held by a running
// process.
func processStart(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')') // the command name may contain blanks and parentheses
	if i < 0 {
		return ""
	}
	f := strings.Fields(s[i+1:]) // from field 3 (the state) on
	if len(f) < 20 {
		return ""
	}
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(boot)) + ":" + f[19]
}
