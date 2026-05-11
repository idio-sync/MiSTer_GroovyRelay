package dlna

func (a *Adapter) seedEventSnapshots() {
	if a.events == nil {
		return
	}
	a.events.setSnapshot(serviceAVTransport, eventProperties{
		"LastChange": buildAVTransportLastChange(a.avTransportEventState()),
	})
	a.events.setSnapshot(serviceRenderingControl, eventProperties{
		"LastChange": buildRenderingControlLastChange(a.renderingControlEventState()),
	})
	a.events.setSnapshot(serviceConnectionManager, a.connectionManagerEventProperties())
}

func (a *Adapter) publishAVTransportLastChange() {
	if a.events == nil {
		return
	}
	a.events.publish(serviceAVTransport, eventProperties{
		"LastChange": buildAVTransportLastChange(a.avTransportEventState()),
	})
}

func (a *Adapter) publishRenderingControlLastChange() {
	if a.events == nil {
		return
	}
	a.events.publish(serviceRenderingControl, eventProperties{
		"LastChange": buildRenderingControlLastChange(a.renderingControlEventState()),
	})
}

func (a *Adapter) avTransportEventState() avTransportEventState {
	a.mu.Lock()
	uri := a.loadedURI
	metaDuration := a.loadedMeta.Duration
	owned := a.currentRef
	state := a.transportState
	lastError := a.lastError
	a.mu.Unlock()

	st := a.core.Status()
	ownSession := owned != "" && st.AdapterRef == owned
	foreignActive := st.AdapterRef != "" && !ownSession
	canSeek := ownSession && st.Duration > 0

	status := "OK"
	if lastError != "" {
		status = "ERROR_OCCURRED"
	}

	duration := "00:00:00"
	relTime := "00:00:00"
	if uri != "" {
		switch {
		case ownSession && st.Duration > 0:
			duration = formatUPnPDuration(st.Duration)
		case metaDuration > 0:
			duration = formatUPnPDuration(metaDuration)
		}
		if ownSession {
			relTime = formatUPnPDuration(st.Position)
		}
	}

	actions := ""
	switch {
	case uri == "":
		actions = ""
	case foreignActive:
		actions = ""
	case state == transportStatePlaying:
		actions = "Pause,Stop"
		if canSeek {
			actions = "Pause,Stop,Seek"
		}
	case state == transportStatePausedPlayback:
		actions = "Play,Stop"
		if canSeek {
			actions = "Play,Stop,Seek"
		}
	case state == transportStateStopped:
		actions = "Play"
	default:
		actions = ""
	}

	return avTransportEventState{
		TransportState:          state,
		TransportStatus:         status,
		CurrentTrackURI:         uri,
		CurrentTrackDuration:    duration,
		RelativeTimePosition:    relTime,
		CurrentTransportActions: actions,
	}
}

func (a *Adapter) renderingControlEventState() renderingControlEventState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return renderingControlEventState{
		Volume: a.volume,
		Muted:  a.muted,
	}
}

func (a *Adapter) connectionManagerEventProperties() eventProperties {
	return eventProperties{
		"SourceProtocolInfo":   "",
		"SinkProtocolInfo":     sinkProtocolInfo,
		"CurrentConnectionIDs": "0",
	}
}
