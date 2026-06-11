//go:build !windows

package display

import "syscall"

func sigUSR1() syscall.Signal { return syscall.SIGUSR1 }
