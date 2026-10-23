package config

import (
	"net"
	"testing"
)

func TestBindHost(t *testing.T) {
	if got := (Defaults{}).BindHost(); got != "127.0.0.1" {
		t.Errorf("empty host = %q, want 127.0.0.1", got)
	}
	if got := (Defaults{Host: "0.0.0.0"}).BindHost(); got != "0.0.0.0" {
		t.Errorf("literal host = %q", got)
	}
	d := Defaults{Host: HostLAN}
	if !d.DynamicHost() {
		t.Error("lan host should be dynamic")
	}
	got := d.BindHost()
	ip := net.ParseIP(got)
	if ip == nil || ip.To4() == nil || !(ip.IsPrivate() || ip.IsLoopback()) {
		t.Errorf("lan host = %q, want a private or loopback IPv4", got)
	}
	t.Logf("lan resolved to %s", got)
}
