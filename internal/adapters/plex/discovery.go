package plex

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// GDM (Good Day Mate) is Plex's LAN discovery protocol. The bridge joins
// multicast group 239.0.0.250 on UDP/32412 to listen for M-SEARCH queries
// from Plex controllers, replies with a unicast HTTP-like descriptor, and
// broadcasts an unsolicited HELLO advertisement on 32413 on a heartbeat
// (see Task 3 — added below in this plan).
//
// Spec: docs/superpowers/specs/2026-05-06-plex-gdm-heartbeat-design.md.

// DiscoveryConfig is the minimal set of fields the responder splices into
// the M-SEARCH reply. DeviceName is user-facing (appears in the Plex cast
// picker); DeviceUUID must be stable across restarts so controllers dedupe
// correctly; HTTPPort is the Companion server's TCP port.
type DiscoveryConfig struct {
	DeviceName string
	DeviceUUID string
	HTTPPort   int
}

// helloInterval is the cadence at which Discovery rebroadcasts the
// HELLO multicast advertisement. Plex's GDM presence TTLs are typically
// 60-120 s, so 30 s gives PMS multiple recovery chances if a packet
// drops or PMS started after the bridge. Exposed as a var so tests
// shorten it to ~50 ms; matches the var-based-knob pattern used by
// pollInterval and registerInterval in linking.go.
var helloInterval = 30 * time.Second

// packetWriter is the small interface Discovery uses for outbound UDP. In
// production it's a *net.UDPConn from net.ListenPacket; tests substitute
// a counting fake to assert HELLO heartbeats and M-SEARCH replies without
// binding real sockets or relying on flaky loopback multicast delivery.
type packetWriter interface {
	WriteTo(b []byte, addr net.Addr) (int, error)
	Close() error
}

// Discovery owns the GDM listen socket and a separate outbound sender.
// Splitting the two avoids a Windows quirk where sending unicast from a
// multicast-joined socket can fail or use an unexpected source IP.
type Discovery struct {
	cfg       DiscoveryConfig
	listen    *net.UDPConn
	sender    packetWriter
	closeOnce sync.Once
	closeErr  error
	stop      chan struct{}
	wg        sync.WaitGroup
}

// NewDiscovery joins the GDM multicast group and launches a heartbeat
// goroutine that broadcasts HELLO immediately and then on a helloInterval
// ticker. Callers are expected to invoke Run in a goroutine and Close on
// shutdown.
func NewDiscovery(cfg DiscoveryConfig) (*Discovery, error) {
	group := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32412}
	listen, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, err
	}
	sender, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		listen.Close()
		return nil, fmt.Errorf("plex GDM: bind sender: %w", err)
	}

	d := &Discovery{
		cfg:    cfg,
		listen: listen,
		sender: sender.(*net.UDPConn),
		stop:   make(chan struct{}),
	}
	d.wg.Add(1)
	go d.runHeartbeat()
	return d, nil
}

// runHeartbeat fires HELLO immediately on entry (preserving the working
// co-located-Docker discovery latency) and then re-broadcasts every
// helloInterval until Close signals stop. Send errors propagate through
// sendHello's WARN logging (added in a later task).
func (d *Discovery) runHeartbeat() {
	defer d.wg.Done()
	_ = d.sendHello()
	t := time.NewTicker(helloInterval)
	defer t.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-t.C:
			_ = d.sendHello()
		}
	}
}

// sendHello announces our presence by writing a HELLO datagram to the GDM
// advertisement port (32413, distinct from the listen group port 32412).
func (d *Discovery) sendHello() error {
	dst := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32413}
	if _, err := d.sender.WriteTo([]byte("HELLO * HTTP/1.0\r\n\r\n"), dst); err != nil {
		slog.Warn("plex GDM HELLO send failed",
			"dst", dst.String(),
			"err", err,
		)
		return err
	}
	return nil
}

// Run reads datagrams until the listen socket is closed and responds to
// each M-SEARCH with a unicast descriptor targeted at the source address.
func (d *Discovery) Run() {
	buf := make([]byte, 4096)
	for {
		n, src, err := d.listen.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req := string(buf[:n])
		if strings.HasPrefix(req, "M-SEARCH") {
			slog.Debug("plex GDM M-SEARCH received",
				"src", src.String(),
				"reply_uuid", d.cfg.DeviceUUID,
				"reply_name", d.cfg.DeviceName,
				"reply_port", d.cfg.HTTPPort,
			)
			d.respondToMSearch(src)
		}
	}
}

// respondToMSearch sends the GDM descriptor fields Plex controllers look
// for when populating the cast target list.
func (d *Discovery) respondToMSearch(dst *net.UDPAddr) {
	body := fmt.Sprintf("HTTP/1.0 200 OK\r\n"+
		"Name: %s\r\n"+
		"Port: %d\r\n"+
		"Resource-Identifier: %s\r\n"+
		"Product: MiSTer_GroovyRelay\r\n"+
		"Version: 1.0\r\n"+
		"Content-Type: plex/media-player\r\n"+
		"Protocol: plex\r\n"+
		"Protocol-Capabilities: timeline,playback,playqueues\r\n"+
		"Device-Class: stb\r\n"+
		"Protocol-Version: 1\r\n\r\n",
		d.cfg.DeviceName, d.cfg.HTTPPort, d.cfg.DeviceUUID)
	if _, err := d.sender.WriteTo([]byte(body), dst); err != nil {
		slog.Warn("plex GDM M-SEARCH reply send failed",
			"dst", dst.String(),
			"err", err,
		)
	}
}

// interfaceForIP returns the network interface that owns the given IPv4
// address. Side-effect free — reads system interface state via
// net.Interfaces but does not mutate it and binds no sockets. Returns
// an error for non-IPv4 input or when no interface owns the address.
//
// Used by NewDiscovery to make multicast egress deterministic on
// multi-NIC hosts. GDM is udp4-only, so non-IPv4 input is a
// configuration mistake worth surfacing rather than silently ignoring.
func interfaceForIP(hostIP string) (*net.Interface, error) {
	target := net.ParseIP(hostIP)
	if target == nil {
		return nil, fmt.Errorf("interfaceForIP: invalid IP %q", hostIP)
	}
	target = target.To4()
	if target == nil {
		return nil, fmt.Errorf("interfaceForIP: %q is not IPv4", hostIP)
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("interfaceForIP: enumerate interfaces: %w", err)
	}
	for i := range ifaces {
		addrs, err := ifaces[i].Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			if ip.To4() != nil && ip.Equal(target) {
				return &ifaces[i], nil
			}
		}
	}
	return nil, fmt.Errorf("interfaceForIP: no interface owns %s", hostIP)
}

// Close signals the heartbeat goroutine to stop, waits for it to exit,
// then releases the listen and sender sockets. Run will return shortly
// after the listen socket closes. Idempotency lands in a later task.
func (d *Discovery) Close() error {
	close(d.stop)
	d.wg.Wait()
	listenErr := d.listen.Close()
	senderErr := d.sender.Close()
	if listenErr != nil {
		return listenErr
	}
	return senderErr
}
