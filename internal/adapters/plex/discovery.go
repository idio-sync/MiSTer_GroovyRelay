package plex

import (
	"fmt"
	"net"
	"strings"
)

// GDM (Good Day Mate) is Plex's LAN discovery protocol. The bridge joins
// multicast group 239.0.0.250 on UDP/32412 to listen for M-SEARCH queries
// from Plex controllers, replies with a unicast HTTP-like descriptor, and
// also broadcasts an unsolicited HELLO advertisement on startup to 32413.
//
// Reference: docs/references/plexdlnaplayer.md.
//
// Interface selection is intentionally omitted here: net.ListenMulticastUDP
// with a nil interface joins the default route, which matches the v1 spec's
// "don't make users pick an adapter" goal. Multi-NIC deployments can be
// handled in a future phase by reading an interface name from config.

// DiscoveryConfig is the minimal set of fields the responder splices into
// the M-SEARCH reply. DeviceName is user-facing (appears in the Plex cast
// picker); DeviceUUID must be stable across restarts so controllers dedupe
// correctly; HTTPPort is the Companion server's TCP port.
type DiscoveryConfig struct {
	DeviceName string
	DeviceUUID string
	HTTPPort   int
}

// Discovery owns the UDP socket bound to the GDM multicast group.
type Discovery struct {
	cfg  DiscoveryConfig
	conn *net.UDPConn
}

// NewDiscovery joins the GDM multicast group and immediately broadcasts a
// HELLO announcement. Callers are expected to invoke Run in a goroutine and
// Close on shutdown.
func NewDiscovery(cfg DiscoveryConfig) (*Discovery, error) {
	group := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32412}
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, err
	}
	d := &Discovery{cfg: cfg, conn: conn}
	if err := d.sendHello(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// sendHello announces our presence by writing a HELLO datagram to the GDM
// advertisement port (32413, distinct from the listen group port 32412).
func (d *Discovery) sendHello() error {
	dst := &net.UDPAddr{IP: net.ParseIP("239.0.0.250"), Port: 32413}
	_, err := d.conn.WriteToUDP([]byte("HELLO * HTTP/1.0\r\n\r\n"), dst)
	return err
}

// Run reads datagrams until the connection is closed and responds to each
// M-SEARCH with a unicast descriptor targeted at the source address.
func (d *Discovery) Run() {
	buf := make([]byte, 4096)
	for {
		n, src, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req := string(buf[:n])
		if strings.HasPrefix(req, "M-SEARCH") {
			d.respondToMSearch(src)
		}
	}
}

// respondToMSearch sends the GDM descriptor fields Plex controllers look for
// when populating the cast target list.
func (d *Discovery) respondToMSearch(dst *net.UDPAddr) {
	body := fmt.Sprintf("HTTP/1.0 200 OK\r\n"+
		"Name: %s\r\n"+
		"Port: %d\r\n"+
		"Resource-Identifier: %s\r\n"+
		"Product: MiSTer_GroovyRelay\r\n"+
		"Version: 1.0\r\n"+
		"Content-Type: plex/media-player\r\n"+
		"Protocol-Capabilities: timeline,playback,playqueues\r\n"+
		"Device-Class: stb\r\n"+
		"Protocol-Version: 1\r\n\r\n",
		d.cfg.DeviceName, d.cfg.HTTPPort, d.cfg.DeviceUUID)
	d.conn.WriteToUDP([]byte(body), dst)
}

// Close releases the multicast socket; Run will return shortly after.
func (d *Discovery) Close() error { return d.conn.Close() }
