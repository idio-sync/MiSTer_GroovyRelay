# DLNA / UPnP Adapter

The DLNA adapter advertises the bridge as a UPnP `MediaRenderer:1` device on the LAN. DLNA control points such as VLC, BubbleUPnP, Kodi, and Windows "Cast to device" can select it and push media to the MiSTer pipeline.

## Security model

> **WARNING:** DLNA exposes unauthenticated LAN control. The UPnP spec has no pairing or auth model, so any device that can reach the bridge HTTP port can change what is playing.

The adapter is disabled by default. Enable it only on trusted home or lab networks, never on a shared network with untrusted clients.

## Required configuration

When DLNA is enabled, the bridge must know its own LAN IP so it can advertise a reachable `LOCATION` URL in SSDP NOTIFY packets and the device descriptor.

Set `bridge.host_ip` explicitly in `config.toml` when possible. The default-route autodetect path is the same one used for the Plex `/resources` response, but multi-NIC hosts can choose the wrong interface.

If `bridge.host_ip` is empty when DLNA is enabled, startup fails with `StateError` and the sidebar reports the misconfiguration instead of advertising a broken renderer.

## Configuration

```toml
[adapters.dlna]
enabled = false                   # Set true to advertise as a DLNA / UPnP MediaRenderer.
                                  # WARNING: any LAN device can control playback.
device_name = "MiSTer"            # Friendly name shown in DLNA controllers.
                                  # Restart bridge: changes apply on next start.
autoplay_on_set_uri = false       # Some controllers set a URI but never send Play.
                                  # Enable for compatibility with those controllers.
allow_public_source_urls = false  # Default false; private/LAN targets always allowed.
                                  # True accepts public-internet media URLs. SSRF risk.
```

## Compatibility notes

DLNA eventing (`SUBSCRIBE`, `UNSUBSCRIBE`, `NOTIFY`) is implemented for AVTransport, RenderingControl, and ConnectionManager.

Real controller findings live in [dlna-compatibility.md](dlna-compatibility.md). Controller-specific quirks and MIME support discoveries should land there first, then graduate into troubleshooting once verified.

## Troubleshooting

**The renderer does not appear.**

DLNA discovery uses SSDP multicast on UDP `239.255.255.250:1900`. Confirm `[adapters.dlna] enabled = true` and `bridge.host_ip` is set to a LAN address controllers can reach.

On Linux, check for another SSDP responder already bound to UDP 1900:

```bash
ss -ulpn | grep ':1900'
systemctl status minissdpd
systemctl status minidlna
systemctl status gerbera
```

On Windows, check the SSDP Discovery service and UDP 1900 ownership:

```powershell
Get-Service SSDPSRV
netstat -ano -p udp | findstr :1900
```

Stop conflicting services only on trusted test machines where you understand the LAN exposure, then restart the bridge.

**The renderer appears, but selecting it fails or controls never work.**

This usually means a multi-NIC `LOCATION` mismatch. The bridge advertises a `device.xml` URL built from `bridge.host_ip` and `bridge.ui.http_port`; if that URL points at a VPN, Docker bridge, or different subnet, the controller can discover the renderer but cannot control it.

Set `bridge.host_ip` to an address on the same subnet as the DLNA controller, then restart the bridge.

**The controller sets media, but playback never starts.**

Some controllers send `SetAVTransportURI` without a follow-up `Play` command. Enable autoplay compatibility mode for those controllers:

```toml
[adapters.dlna]
autoplay_on_set_uri = true
```

Leave it off for controllers that send explicit `Play` commands.

**The controller reports an illegal MIME-type.**

Direct HTTP URLs must use one of the bridge's allow-listed MIME types before the DLNA adapter passes them to playback. HLS/M3U8 URLs are accepted only after nested playlist and segment URLs pass validation.
