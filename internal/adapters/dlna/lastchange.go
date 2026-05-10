package dlna

import (
	"bytes"
	"encoding/xml"
	"sort"
	"strconv"
)

type eventProperties map[string]string

type avTransportEventState struct {
	TransportState          string
	TransportStatus         string
	CurrentTrackURI         string
	CurrentTrackDuration    string
	RelativeTimePosition    string
	CurrentTransportActions string
}

type renderingControlEventState struct {
	Volume int
	Muted  bool
}

func buildAVTransportLastChange(st avTransportEventState) string {
	var b bytes.Buffer
	b.WriteString(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/AVT/">`)
	b.WriteString(`<InstanceID val="0">`)
	writeLastChangeVariable(&b, "TransportState", "", st.TransportState)
	writeLastChangeVariable(&b, "TransportStatus", "", st.TransportStatus)
	writeLastChangeVariable(&b, "CurrentTrackURI", "", st.CurrentTrackURI)
	writeLastChangeVariable(&b, "CurrentTrackDuration", "", st.CurrentTrackDuration)
	writeLastChangeVariable(&b, "RelativeTimePosition", "", st.RelativeTimePosition)
	writeLastChangeVariable(&b, "CurrentTransportActions", "", st.CurrentTransportActions)
	b.WriteString(`</InstanceID></Event>`)
	return b.String()
}

func buildRenderingControlLastChange(st renderingControlEventState) string {
	mute := "0"
	if st.Muted {
		mute = "1"
	}
	var b bytes.Buffer
	b.WriteString(`<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/">`)
	b.WriteString(`<InstanceID val="0">`)
	writeLastChangeVariable(&b, "Volume", "Master", strconv.Itoa(st.Volume))
	writeLastChangeVariable(&b, "Mute", "Master", mute)
	b.WriteString(`</InstanceID></Event>`)
	return b.String()
}

func writeLastChangeVariable(b *bytes.Buffer, name, channel, value string) {
	b.WriteByte('<')
	b.WriteString(name)
	if channel != "" {
		b.WriteString(` channel="`)
		xml.EscapeText(b, []byte(channel))
		b.WriteByte('"')
	}
	b.WriteString(` val="`)
	xml.EscapeText(b, []byte(value))
	b.WriteString(`"></`)
	b.WriteString(name)
	b.WriteByte('>')
}

func buildGENAPropertySet(props eventProperties) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0"?>`)
	b.WriteString(`<e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0">`)
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(`<e:property><`)
		b.WriteString(k)
		b.WriteByte('>')
		xml.EscapeText(&b, []byte(props[k]))
		b.WriteString(`</`)
		b.WriteString(k)
		b.WriteString(`></e:property>`)
	}
	b.WriteString(`</e:propertyset>`)
	return b.Bytes()
}
