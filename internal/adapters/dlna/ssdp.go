package dlna

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/ipv4"
)

// SSDP — Simple Service Discovery Protocol — is the discovery layer of
// UPnP. The DLNA renderer joins multicast group 239.255.255.250:1900,
// announces itself with periodic ssdp:alive NOTIFY messages, answers
// M-SEARCH queries from controllers (VLC, BubbleUPnP, Kodi, Windows
// "Cast to device"), and gracefully retracts via ssdp:byebye on shutdown.
//
// Spec: docs/superpowers/specs/2026-05-03-dlna-mediarenderer-design.md
// §SSDP. Closely mirrors the Plex GDM implementation pattern in
// internal/adapters/plex/discovery.go — split listen/sender sockets,
// per-platform REUSEADDR helper, idempotent Close — without importing
// the plex package (the DLNA spec is explicit that they remain
// independent).

const (
	ssdpGroupIP   = "239.255.255.250"
	ssdpPort      = 1900
	ssdpMaxAge    = 1800 // seconds; CACHE-CONTROL header value
	// ~50 % of max-age forms the lower bound; 50–60 s of jitter fits
	// inside the ±60 s spec band so the upper bound is `min + 120 s`.
	ssdpRefreshMin = 840 * time.Second
	ssdpRefreshMax = 960 * time.Second
	// ~50 ms between consecutive NOTIFY/M-SEARCH replies for the same
	// USN set. Spec line 278/279: "stagger those replies by roughly
	// 50 ms to avoid response bursts."
	ssdpUSNStagger = 50 * time.Millisecond
	// 1 s ceiling when the controller didn't send a parseable MX, and
	// 5 s ceiling when it did. UPnP 1.1 forbids honoring an MX larger
	// than 5 s precisely to limit the burst this one cap controls.
	ssdpMXCapDefault = 1 * time.Second
	ssdpMXCapMax     = 5 * time.Second

	// SERVER product token. The middle field is the UPnP version, the
	// outer fields identify the bridge implementation. T5 will swap the
	// hard-coded "0.1" for main.go's `version` variable; for now this
	// constant keeps T3 self-contained.
	ssdpServerToken = "MiSTerGroovyRelay/0.1 UPnP/1.0 MiSTer-DLNA/1.0"
)

// targets enumerates the six NT/USN/ST tuples the renderer advertises.
// Spec §SSDP "On start" list (lines 266-272) plus the M-SEARCH match
// table in this task's prompt. Initialized lazily in newTargets() so
// the device UUID can be spliced in.
type ssdpTarget struct {
	// nt is the NOTIFY's NT header value, also one of the ST values
	// this entry answers an M-SEARCH for.
	nt string
	// usn is the unique service name the entry advertises, computed
	// from the device UUID and the NT.
	usn string
	// extraSTs is additional ST values that should match this entry
	// in addition to the NT itself. Used so the device-UUID entry
	// answers ST=uuid:<...> while the NT-typed entries (rootdevice,
	// MediaRenderer, the three services) only answer their own NT.
	// Empty for entries whose NT is the only ST that selects them.
	extraSTs []string
}

// newTargets builds the six-row table per the spec.
//
//   - rootdevice                      — generic UPnP discovery
//   - the bare device UUID            — uniquely-identified discovery
//   - MediaRenderer:1                  — DLNA controller class filter
//   - AVTransport:1 / RC:1 / CM:1      — service-typed discovery
//
// The NT column carries one entry's NT; the USN column embeds it inside
// the standard UDN form. The device UUID itself uses USN == NT (no `::`
// suffix) per UPnP 1.1 § 1.1.4.
func newTargets(deviceUUID string) []ssdpTarget {
	udn := "uuid:" + deviceUUID
	return []ssdpTarget{
		{nt: "upnp:rootdevice", usn: udn + "::upnp:rootdevice"},
		{nt: udn, usn: udn},
		{nt: "urn:schemas-upnp-org:device:MediaRenderer:1", usn: udn + "::urn:schemas-upnp-org:device:MediaRenderer:1"},
		{nt: "urn:schemas-upnp-org:service:AVTransport:1", usn: udn + "::urn:schemas-upnp-org:service:AVTransport:1"},
		{nt: "urn:schemas-upnp-org:service:ConnectionManager:1", usn: udn + "::urn:schemas-upnp-org:service:ConnectionManager:1"},
		{nt: "urn:schemas-upnp-org:service:RenderingControl:1", usn: udn + "::urn:schemas-upnp-org:service:RenderingControl:1"},
	}
}

// matches returns true when the given M-SEARCH ST value should select
// this target for a unicast reply. ssdp:all is the catch-all — every
// row matches it. Otherwise the ST must equal either the NT or one of
// the extraSTs.
func (t ssdpTarget) matches(st string) bool {
	if st == "ssdp:all" {
		return true
	}
	if st == t.nt {
		return true
	}
	for _, alt := range t.extraSTs {
		if st == alt {
			return true
		}
	}
	return false
}

// DiscoveryConfig is the minimal set of inputs SSDP needs from the
// adapter. HostIP and HTTPPort feed every LOCATION header (spec line
// 282: "every LOCATION header must be generated from the
// configured/resolved HostIP and HTTPPort, not from the UDP packet's
// local address"). DeviceUUID drives the USN/UDN. DeviceName is
// reserved for future SERVER-header friendliness; v1 uses a fixed token.
type DiscoveryConfig struct {
	// DeviceUUID is the bare bridge UUID — no "uuid:" prefix; the
	// helpers add it. Required.
	DeviceUUID string
	// DeviceName is the user-facing friendly name. Not used in NOTIFY
	// or M-SEARCH replies (those go through the SCPD device descriptor
	// in T4) but recorded here for symmetry with the Plex
	// DiscoveryConfig and for any future SERVER-header customization.
	DeviceName string
	// HostIP is the LAN address the bridge advertises in LOCATION
	// headers. Must parse as an IP via net.ParseIP — caller responsibility.
	HostIP string
	// HTTPPort is the port number /dlna/device.xml is served on.
	// Must be in (0, 65535].
	HTTPPort int
	// Logger is nil-tolerant: when nil, slog.Default() is used.
	Logger *slog.Logger
}

// packetWriter is the small interface Discovery uses for outbound UDP.
// Production wires a *net.UDPConn; tests substitute a counting fake to
// verify NOTIFY/M-SEARCH content without binding sockets. Mirror of
// plex/discovery.go:packetWriter.
type packetWriter interface {
	WriteTo(b []byte, addr net.Addr) (int, error)
	Close() error
}

// Discovery owns the SSDP listen socket and a separate ephemeral
// sender. The split-socket pattern matches plex.Discovery and avoids
// the documented Windows quirk where a multicast-joined socket
// misbehaves for unicast egress.
type Discovery struct {
	cfg     DiscoveryConfig
	logger  *slog.Logger
	targets []ssdpTarget

	listen *net.UDPConn
	sender packetWriter

	// rng provides MX response-delay randomness. Not security-relevant
	// (UPnP timing fuzz), so math/rand is fine. Per-Discovery instance
	// so tests can seed deterministically if needed.
	rng   *rand.Rand
	rngMu sync.Mutex

	closeOnce sync.Once
	closeErr  error
	stop      chan struct{}
	wg        sync.WaitGroup
}

// NewDiscovery validates the config, joins the SSDP multicast group,
// and binds an ephemeral sender socket. Does NOT start the run loop —
// callers invoke Run in a goroutine.
//
// On bind/multicast-join failure the listen socket is closed and the
// error is returned wrapped. The adapter's Start treats that as a
// StateError condition (the spec says an enabled DLNA adapter without
// SSDP is "effectively invisible", line 261).
func NewDiscovery(cfg DiscoveryConfig) (*Discovery, error) {
	if strings.TrimSpace(cfg.DeviceUUID) == "" {
		return nil, fmt.Errorf("dlna ssdp: DeviceUUID is required")
	}
	if cfg.HTTPPort <= 0 || cfg.HTTPPort > 65535 {
		return nil, fmt.Errorf("dlna ssdp: HTTPPort must be in (0, 65535], got %d", cfg.HTTPPort)
	}
	hostTrim := strings.TrimSpace(cfg.HostIP)
	if hostTrim == "" {
		return nil, fmt.Errorf("dlna ssdp: HostIP is required")
	}
	if net.ParseIP(hostTrim) == nil {
		return nil, fmt.Errorf("dlna ssdp: HostIP %q is not a valid IP", cfg.HostIP)
	}
	cfg.HostIP = hostTrim

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Resolve the HostIP-owning interface for multicast egress pinning.
	// Best-effort: a non-matching HostIP (typo, IPv6, host_ip set to a
	// non-local IP for NAT traversal) falls back to the kernel default.
	// Same fallback shape as plex.senderBindFor.
	iface := interfaceForHostIP(hostTrim, logger)

	// Bind the listen socket via ListenConfig with the per-platform
	// REUSEADDR control hook so port 1900 can be shared with other
	// UPnP daemons (miniDLNA, gerbera, Windows Network Discovery,
	// etc.). Then explicitly join the multicast group on the resolved
	// interface. We deliberately don't use net.ListenMulticastUDP
	// because that API offers no socket-control hook for REUSEADDR
	// before bind on every platform we target — splitting the bind +
	// join lets us set REUSEADDR first.
	bindAddr := fmt.Sprintf("%s:%d", ssdpGroupIP, ssdpPort)
	listen, err := listenReusablePacket("udp4", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("dlna ssdp: bind listen socket: %w", err)
	}
	group := &net.UDPAddr{IP: net.ParseIP(ssdpGroupIP), Port: ssdpPort}
	if err := joinMulticastGroup(listen, iface, group); err != nil {
		listen.Close()
		return nil, fmt.Errorf("dlna ssdp: join multicast group: %w", err)
	}

	// Ephemeral sender bound to :0 so unicast replies don't go out from
	// port 1900 (Windows quirk: multicast-joined sockets misbehave on
	// unicast send). REUSEADDR on the sender is unnecessary but cheap;
	// we use the plain ListenPacket because the sender socket is not
	// expected to coexist with another listener.
	sender, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		listen.Close()
		return nil, fmt.Errorf("dlna ssdp: bind sender: %w", err)
	}
	udpSender, ok := sender.(*net.UDPConn)
	if !ok {
		sender.Close()
		listen.Close()
		return nil, fmt.Errorf("dlna ssdp: unexpected sender type %T", sender)
	}

	// Pin multicast egress to the selected interface where we have one.
	// Failure is non-fatal: the kernel default takes over.
	if iface != nil {
		if e := ipv4.NewPacketConn(udpSender).SetMulticastInterface(iface); e != nil {
			logger.Warn("dlna ssdp: SetMulticastInterface failed; falling back to kernel default",
				"iface", iface.Name,
				"err", e,
			)
		}
	}

	d := &Discovery{
		cfg:     cfg,
		logger:  logger,
		targets: newTargets(cfg.DeviceUUID),
		listen:  listen,
		sender:  udpSender,
		rng:     newSeededRand(),
		stop:    make(chan struct{}),
	}
	return d, nil
}

// newSeededRand mints a fresh math/rand RNG seeded from the wall clock.
// Extracted so tests can build a struct-only Discovery (no socket
// binds) while still exercising the timing-jitter logic.
func newSeededRand() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// listenReusablePacket opens a UDP socket with the per-platform
// REUSEADDR control hook applied before bind. SSDP requires this
// because port 1900 may be shared with other UPnP daemons; without
// REUSEADDR the second bind would fail on every platform we target.
func listenReusablePacket(network, addr string) (*net.UDPConn, error) {
	lc := &net.ListenConfig{Control: controlReusableSocket}
	pc, err := lc.ListenPacket(context.Background(), network, addr)
	if err != nil {
		return nil, err
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		pc.Close()
		return nil, fmt.Errorf("dlna ssdp: unexpected packet conn type %T", pc)
	}
	return conn, nil
}

// joinMulticastGroup adds the SSDP multicast group membership to an
// already-bound UDP listen socket. Splitting bind from join is the
// reason listenReusablePacket exists — we needed REUSEADDR set before
// the bind, and net.ListenMulticastUDP doesn't expose that hook.
//
// iface may be nil; the kernel picks the default-route interface in
// that case (matches plex.NewDiscovery's nil-interface fallback).
func joinMulticastGroup(conn *net.UDPConn, iface *net.Interface, group *net.UDPAddr) error {
	pc := ipv4.NewPacketConn(conn)
	if err := pc.JoinGroup(iface, group); err != nil {
		return err
	}
	return nil
}

// interfaceForHostIP returns the local interface that owns hostIP, or
// nil if no interface matches (in which case the kernel picks the
// default route). udp4-only — IPv6 HostIPs fall through to nil.
//
// Logs at WARN when the HostIP doesn't resolve so a misconfig surfaces
// in the operator's logs without taking down SSDP.
func interfaceForHostIP(hostIP string, logger *slog.Logger) *net.Interface {
	target := net.ParseIP(hostIP)
	if target == nil {
		return nil
	}
	target4 := target.To4()
	if target4 == nil {
		// IPv6 HostIP: kernel picks default. SSDP spec assumes IPv4
		// multicast on 239.255.255.250; IPv6 SSDP would use ff0x::c
		// and isn't in scope for Phase 1.
		return nil
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		logger.Warn("dlna ssdp: enumerate interfaces failed; using kernel default",
			"err", err,
		)
		return nil
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
			if ip.To4() != nil && ip.Equal(target4) {
				return &ifaces[i]
			}
		}
	}
	logger.Warn("dlna ssdp: HostIP not on a local interface; using kernel default",
		"host_ip", hostIP,
	)
	return nil
}

// Run blocks until ctx is cancelled or Close is called. On entry it
// fires one full alive burst, then loops: read inbound datagram (ignore
// errors caused by Close-induced socket closure); on M-SEARCH dispatch
// matching unicast replies; on the refresh timer rebroadcast the alive
// set.
//
// The refresh timer uses a single-shot time.NewTimer that's reset each
// iteration with fresh randomness in [ssdpRefreshMin, ssdpRefreshMax].
// A fixed-period ticker would skew over time; this pattern matches the
// spec's "every ~900 s ±60 s jitter" requirement (line 279).
func (d *Discovery) Run(ctx context.Context) {
	d.wg.Add(1)
	defer d.wg.Done()

	// Immediate alive burst on entry. Spec line 264: "On start: send
	// ssdp:alive NOTIFY messages for ..." — Run() is the moment that
	// happens, after the adapter has determined Enabled+HostIP are good.
	d.sendAliveBurst()

	refresh := time.NewTimer(d.nextRefreshDelay())
	defer refresh.Stop()

	// Reader goroutine: blocks on ReadFromUDP and pushes parsed
	// M-SEARCH events into a channel. We can't select on ReadFromUDP
	// directly, so we wrap it. When Close() runs, the listen socket
	// closes, ReadFromUDP returns an error, and the reader exits — at
	// which point the main loop notices via stop or the closed channel
	// and exits.
	type mSearchEvent struct {
		st  string
		mx  string
		src *net.UDPAddr
	}
	events := make(chan mSearchEvent, 8)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 4096)
		for {
			n, src, err := d.listen.ReadFromUDP(buf)
			if err != nil {
				return
			}
			st, mx, ok := parseMSearch(buf[:n])
			if !ok {
				continue
			}
			select {
			case events <- mSearchEvent{st: st, mx: mx, src: src}:
			case <-d.stop:
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stop:
			// Drain the reader so its goroutine exits cleanly.
			<-readerDone
			return
		case <-readerDone:
			// Listen socket closed externally (e.g., test harness);
			// nothing more to do.
			return
		case ev := <-events:
			d.handleMSearch(ev.st, ev.mx, ev.src)
		case <-refresh.C:
			d.sendAliveBurst()
			refresh.Reset(d.nextRefreshDelay())
		}
	}
}

// nextRefreshDelay samples a uniform duration in [ssdpRefreshMin,
// ssdpRefreshMax]. Uses the per-Discovery RNG under its mutex.
func (d *Discovery) nextRefreshDelay() time.Duration {
	d.rngMu.Lock()
	defer d.rngMu.Unlock()
	span := ssdpRefreshMax - ssdpRefreshMin
	jitter := time.Duration(d.rng.Int63n(int64(span) + 1))
	return ssdpRefreshMin + jitter
}

// sendAliveBurst writes one ssdp:alive NOTIFY per target with
// ssdpUSNStagger between writes. Each write logs at WARN on failure
// and continues — losing one NOTIFY shouldn't suppress the rest.
func (d *Discovery) sendAliveBurst() {
	dst := &net.UDPAddr{IP: net.ParseIP(ssdpGroupIP), Port: ssdpPort}
	for i, t := range d.targets {
		if i > 0 {
			// Stagger inside the burst. We sleep on the foreground
			// goroutine because a burst is short (6 × 50 ms = 300 ms)
			// and Run is single-threaded; spawning workers would be
			// overkill and complicate Close.
			select {
			case <-d.stop:
				return
			case <-time.After(ssdpUSNStagger):
			}
		}
		body := d.buildNotify("ssdp:alive", t.nt, t.usn)
		if _, err := d.sender.WriteTo(body, dst); err != nil {
			d.logger.Warn("dlna ssdp: NOTIFY alive send failed",
				"nt", t.nt,
				"err", err,
			)
		}
	}
}

// sendByebyeBurst writes one ssdp:byebye NOTIFY per target. No stagger
// — byebye is best-effort and we want it out the door before Close
// returns. UPnP 1.1 says byebye should fire on "controlled exit" which
// is exactly the Stop() path; controllers tolerant of dropped byebyes
// will fall back to the CACHE-CONTROL TTL.
func (d *Discovery) sendByebyeBurst() {
	dst := &net.UDPAddr{IP: net.ParseIP(ssdpGroupIP), Port: ssdpPort}
	for _, t := range d.targets {
		body := d.buildNotify("ssdp:byebye", t.nt, t.usn)
		if _, err := d.sender.WriteTo(body, dst); err != nil {
			// Debug-level: byebye failure during shutdown is common
			// (sender already closing) and not actionable.
			d.logger.Debug("dlna ssdp: NOTIFY byebye send failed",
				"nt", t.nt,
				"err", err,
			)
		}
	}
}

// handleMSearch implements the M-SEARCH responder. Spec line 278:
// "Honor the request's MX response-delay header: parse it as seconds,
// cap it at 5 seconds, choose a random delay in [0, MX], and send
// matching unicast replies after that delay. If MX is absent or
// malformed, use a 1-second cap. When one request matches multiple
// NT/USN advertisements, stagger those replies by roughly 50 ms to
// avoid response bursts."
//
// Each matching target gets its own goroutine that sleeps the per-USN
// delay then writes. d.wg.Add lets Close() wait for all reply goroutines
// to drain. If MX yields a 0-duration cap (parsed but small), every
// reply fires close to immediately, only separated by the stagger.
func (d *Discovery) handleMSearch(st, mxRaw string, src *net.UDPAddr) {
	mxCap := parseMX(mxRaw)
	delay := d.randomDelayUpTo(mxCap)

	matched := 0
	for _, t := range d.targets {
		if !t.matches(st) {
			continue
		}
		matched++
		stagger := time.Duration(matched-1) * ssdpUSNStagger
		usn := t.usn
		// Resolve the ST in the reply: when controllers ask for
		// "ssdp:all", they expect each reply's ST to be the per-entry
		// type, not "ssdp:all" itself. UPnP 1.1 § 1.3.3 makes this
		// explicit. For non-"ssdp:all" matches, we echo the same NT
		// the controller asked for — which is t.nt or one of t.extraSTs.
		var replyST string
		if st == "ssdp:all" {
			replyST = t.nt
		} else {
			replyST = st
		}

		d.wg.Add(1)
		go func(replyST, usn string, sleep time.Duration) {
			defer d.wg.Done()
			select {
			case <-d.stop:
				return
			case <-time.After(sleep):
			}
			body := d.buildSearchResponse(replyST, usn)
			if _, err := d.sender.WriteTo(body, src); err != nil {
				d.logger.Warn("dlna ssdp: M-SEARCH reply send failed",
					"dst", src.String(),
					"st", replyST,
					"err", err,
				)
			}
		}(replyST, usn, delay+stagger)
	}
	if matched == 0 {
		d.logger.Debug("dlna ssdp: M-SEARCH ignored",
			"src", src.String(),
			"st", st,
		)
	}
}

// randomDelayUpTo returns a uniformly random duration in [0, ceiling].
// A ceiling of 0 returns 0. Uses the per-Discovery RNG under its mutex.
func (d *Discovery) randomDelayUpTo(ceiling time.Duration) time.Duration {
	if ceiling <= 0 {
		return 0
	}
	d.rngMu.Lock()
	defer d.rngMu.Unlock()
	return time.Duration(d.rng.Int63n(int64(ceiling) + 1))
}

// buildNotify formats an SSDP NOTIFY message. method must be either
// "ssdp:alive" or "ssdp:byebye"; for byebye CACHE-CONTROL, LOCATION,
// and SERVER are omitted per UPnP 1.1 § 1.2.3 (controllers ignore the
// fields anyway because the device is going away).
//
// CRLF line endings are mandatory — UPnP parsers are HTTP-strict.
// Trailing blank line marks end-of-headers.
func (d *Discovery) buildNotify(method, nt, usn string) []byte {
	host := fmt.Sprintf("%s:%d", ssdpGroupIP, ssdpPort)
	var b strings.Builder
	b.WriteString("NOTIFY * HTTP/1.1\r\n")
	b.WriteString("HOST: ")
	b.WriteString(host)
	b.WriteString("\r\n")
	if method == "ssdp:alive" {
		b.WriteString("CACHE-CONTROL: max-age=")
		b.WriteString(strconv.Itoa(ssdpMaxAge))
		b.WriteString("\r\n")
		b.WriteString("LOCATION: ")
		b.WriteString(d.locationURL())
		b.WriteString("\r\n")
	}
	b.WriteString("NT: ")
	b.WriteString(nt)
	b.WriteString("\r\n")
	b.WriteString("NTS: ")
	b.WriteString(method)
	b.WriteString("\r\n")
	if method == "ssdp:alive" {
		b.WriteString("SERVER: ")
		b.WriteString(ssdpServerToken)
		b.WriteString("\r\n")
	}
	b.WriteString("USN: ")
	b.WriteString(usn)
	b.WriteString("\r\n")
	b.WriteString("\r\n")
	return []byte(b.String())
}

// buildSearchResponse formats a unicast HTTP/1.1 200 OK reply to an
// M-SEARCH. Spec / UPnP 1.1 § 1.3.3 mandates the listed headers; DATE
// is RFC 1123 GMT and computed at write time so caches age correctly.
func (d *Discovery) buildSearchResponse(st, usn string) []byte {
	var b strings.Builder
	b.WriteString("HTTP/1.1 200 OK\r\n")
	b.WriteString("CACHE-CONTROL: max-age=")
	b.WriteString(strconv.Itoa(ssdpMaxAge))
	b.WriteString("\r\n")
	b.WriteString("DATE: ")
	b.WriteString(time.Now().UTC().Format(http.TimeFormat))
	b.WriteString("\r\n")
	b.WriteString("EXT:\r\n")
	b.WriteString("LOCATION: ")
	b.WriteString(d.locationURL())
	b.WriteString("\r\n")
	b.WriteString("SERVER: ")
	b.WriteString(ssdpServerToken)
	b.WriteString("\r\n")
	b.WriteString("ST: ")
	b.WriteString(st)
	b.WriteString("\r\n")
	b.WriteString("USN: ")
	b.WriteString(usn)
	b.WriteString("\r\n")
	b.WriteString("\r\n")
	return []byte(b.String())
}

// locationURL renders the LOCATION header value for both NOTIFY and
// M-SEARCH replies. IPv6 hosts get bracketed. Spec line 282 mandates
// HostIP/HTTPPort here, never the local address of the inbound packet.
func (d *Discovery) locationURL() string {
	host := d.cfg.HostIP
	// Bracket bare IPv6 literals so the URL is parseable.
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d/dlna/device.xml", host, d.cfg.HTTPPort)
}

// Close signals Run to stop, fires the byebye burst on the live
// sender (still bound at this point), then releases sockets. Idempotent
// — second Close returns the first invocation's error and does no
// further I/O. The goroutine wait happens BEFORE socket close so
// in-flight reply goroutines have a chance to write their byebye-time
// reply (rare race; not strictly necessary for correctness but cleaner).
//
// Order of operations:
//  1. signal stop (close d.stop)
//  2. unblock the reader by closing the listen socket
//  3. wait for reader + reply goroutines via wg
//  4. send byebye burst on the still-open sender socket
//  5. close the sender
//
// The byebye burst could race with kernel queue flush; we accept that.
// Compliant controllers fall back to the CACHE-CONTROL TTL anyway.
func (d *Discovery) Close() error {
	d.closeOnce.Do(func() {
		close(d.stop)
		listenErr := d.listen.Close()
		d.wg.Wait()
		d.sendByebyeBurst()
		senderErr := d.sender.Close()
		switch {
		case listenErr != nil:
			d.closeErr = listenErr
		case senderErr != nil:
			d.closeErr = senderErr
		}
	})
	return d.closeErr
}

// parseMSearch returns (ST, raw MX header value, ok=true) for inbound
// M-SEARCH datagrams. Returns ok=false when the request isn't an
// M-SEARCH or doesn't include an ST header (UPnP 1.1 § 1.3.2 makes ST
// mandatory; missing ST is a malformed request we ignore).
//
// Uses textproto for header parsing so case-insensitive lookups Just
// Work (UPnP allows arbitrary casing on header names; controllers in
// the wild do everything from "St:" to "ST:" to "st:").
func parseMSearch(buf []byte) (st, mx string, ok bool) {
	// First line must start with M-SEARCH; otherwise this is a NOTIFY
	// from another device or an HTTP response we shouldn't parse.
	if !strings.HasPrefix(string(buf), "M-SEARCH") {
		return "", "", false
	}
	br := bufio.NewReader(strings.NewReader(string(buf)))
	if _, err := br.ReadString('\n'); err != nil {
		return "", "", false
	}
	tp := textproto.NewReader(br)
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return "", "", false
	}
	st = strings.TrimSpace(hdr.Get("St"))
	if st == "" {
		return "", "", false
	}
	mx = strings.TrimSpace(hdr.Get("Mx"))
	return st, mx, true
}

// parseMX implements the spec's MX header rules. Returns the cap for
// the random response delay:
//   - missing or non-integer or negative → ssdpMXCapDefault (1 s)
//   - 0 → 0 (controller asked for an immediate reply)
//   - in (0, 5] → that many seconds
//   - > 5 → ssdpMXCapMax (5 s)
//
// UPnP 1.1 § 1.3.2 says "If the MX header field specifies a field
// value greater than 5, the device should assume that it contained the
// value 5 or less." We honor the wording strictly: capped, not rejected.
func parseMX(raw string) time.Duration {
	if raw == "" {
		return ssdpMXCapDefault
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return ssdpMXCapDefault
	}
	if n < 0 {
		return ssdpMXCapDefault
	}
	d := time.Duration(n) * time.Second
	if d > ssdpMXCapMax {
		return ssdpMXCapMax
	}
	return d
}
