//go:build !windows && !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package codex

import "os/exec"

func configureProcessTree(*exec.Cmd) {}
