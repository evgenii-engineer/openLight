package system

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"openlight/internal/skills"
)

type MemoryStats struct {
	Total     uint64
	Available uint64
	Used      uint64
}

type DiskStats struct {
	Path  string
	Total uint64
	Free  uint64
	Used  uint64
}

// Provider is the cross-platform interface that all OS-specific providers
// implement. Linux uses /proc and /sys; Darwin uses sysctl and friends.
// See provider_linux.go and provider_darwin.go for the OS-specific halves
// of LocalProvider's method set.
type Provider interface {
	CPUUsage(ctx context.Context) (float64, error)
	MemoryStats(ctx context.Context) (MemoryStats, error)
	DiskStats(ctx context.Context, path string) (DiskStats, error)
	Uptime(ctx context.Context) (time.Duration, error)
	Hostname(ctx context.Context) (string, error)
	IPAddresses(ctx context.Context) ([]string, error)
	Temperature(ctx context.Context) (float64, error)
}

type LocalProvider struct{}

func NewLocalProvider() Provider {
	return LocalProvider{}
}

// DiskStats works on both Linux and Darwin via the POSIX statfs syscall.
// stat.Bsize is int64 on Linux but int32 on Darwin; the uint64 cast keeps
// the same call site compiling on both.
func (LocalProvider) DiskStats(_ context.Context, path string) (DiskStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return DiskStats{}, wrapUnavailable("disk", err)
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := total - free

	return DiskStats{
		Path:  path,
		Total: total,
		Free:  free,
		Used:  used,
	}, nil
}

func (LocalProvider) Hostname(_ context.Context) (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", wrapUnavailable("hostname", err)
	}
	return host, nil
}

func (LocalProvider) IPAddresses(_ context.Context) ([]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, wrapUnavailable("ip", err)
	}

	var addresses []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip == nil || ip.IsLoopback() {
				continue
			}
			if ip.To4() == nil {
				continue
			}
			addresses = append(addresses, ip.String())
		}
	}

	if len(addresses) == 0 {
		return nil, wrapUnavailable("ip", fmt.Errorf("no non-loopback IPv4 addresses found"))
	}

	return addresses, nil
}

func wrapUnavailable(metric string, err error) error {
	return fmt.Errorf("%w: %s unavailable: %v", skills.ErrUnavailable, metric, err)
}
