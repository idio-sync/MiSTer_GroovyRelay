package airplay

import (
	"encoding/binary"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/adapters/liveaudio"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/artworkcache"
)

// shairport-sync's UDP metadata packets (metadata/multicast.c):
//
//	item:  type[4] code[4] data...
//	chunk: "ssncchnk" index[4] total[4] type[4] code[4] data...
//
// Four-character codes are ASCII; integers are big-endian. An item too
// large for one datagram (cover art) arrives as chunks 0..total-1.
const (
	chunkHeaderLen = 24
	itemHeaderLen  = 8
	maxChunks      = 1024
)

// metadataParser turns metadata packets into liveaudio events. It is
// not safe for concurrent use; one listener goroutine owns it.
type metadataParser struct {
	bundle *liveaudio.TrackMeta // open between ssnc/mdst and ssnc/mden
	track  liveaudio.TrackMeta  // last complete track text
	chunks *chunkAssembly
}

type chunkAssembly struct {
	typ, code string
	parts     [][]byte
	got       int
	size      int
}

// packet parses one datagram and returns the events it completes.
func (p *metadataParser) packet(b []byte) []liveaudio.Event {
	if len(b) >= chunkHeaderLen && string(b[:8]) == "ssncchnk" {
		return p.chunk(b)
	}
	if len(b) < itemHeaderLen {
		return nil
	}
	return p.item(string(b[0:4]), string(b[4:8]), b[itemHeaderLen:])
}

func (p *metadataParser) chunk(b []byte) []liveaudio.Event {
	ix := binary.BigEndian.Uint32(b[8:12])
	total := binary.BigEndian.Uint32(b[12:16])
	typ, code := string(b[16:20]), string(b[20:24])
	data := b[chunkHeaderLen:]
	if total == 0 || total > maxChunks || ix >= total {
		return nil
	}
	a := p.chunks
	if a == nil || a.typ != typ || a.code != code || len(a.parts) != int(total) {
		a = &chunkAssembly{typ: typ, code: code, parts: make([][]byte, total)}
		p.chunks = a
	}
	if a.parts[ix] == nil {
		if a.size+len(data) > artworkcache.MaxBytes {
			p.chunks = nil // oversized: drop the whole item
			return nil
		}
		a.parts[ix] = append([]byte{}, data...)
		a.got++
		a.size += len(data)
	}
	if a.got < len(a.parts) {
		return nil
	}
	p.chunks = nil
	whole := make([]byte, 0, a.size)
	for _, part := range a.parts {
		whole = append(whole, part...)
	}
	return p.item(typ, code, whole)
}

func (p *metadataParser) item(typ, code string, data []byte) []liveaudio.Event {
	switch typ + "/" + code {
	case "ssnc/pbeg":
		return []liveaudio.Event{{Kind: liveaudio.EventPlay}}
	case "ssnc/prsm":
		return []liveaudio.Event{{Kind: liveaudio.EventResume}}
	case "ssnc/pfls":
		return []liveaudio.Event{{Kind: liveaudio.EventPause}}
	case "ssnc/pend":
		p.track = liveaudio.TrackMeta{}
		return []liveaudio.Event{{Kind: liveaudio.EventStop}}
	case "ssnc/mdst":
		p.bundle = &liveaudio.TrackMeta{}
	case "ssnc/mden":
		if p.bundle == nil {
			return nil
		}
		p.track, p.bundle = *p.bundle, nil
		return []liveaudio.Event{{Kind: liveaudio.EventTrack, Track: p.track}}
	case "core/minm", "core/asar", "core/asal":
		// Inside a bundle the text waits for mden; outside one (some
		// senders) each field updates the current track on its own.
		target := p.bundle
		if target == nil {
			target = &p.track
		}
		switch code {
		case "minm":
			target.Title = string(data)
		case "asar":
			target.Artist = string(data)
		case "asal":
			target.Album = string(data)
		}
		if p.bundle == nil {
			return []liveaudio.Event{{Kind: liveaudio.EventTrack, Track: p.track}}
		}
	case "ssnc/PICT":
		// Empty clears the artwork; otherwise JPEG or PNG bytes. Copied:
		// data may alias the listener's reused read buffer.
		art := append([]byte(nil), data...)
		return []liveaudio.Event{{Kind: liveaudio.EventArtwork, Track: liveaudio.TrackMeta{ArtworkBytes: art}}}
	}
	return nil
}
