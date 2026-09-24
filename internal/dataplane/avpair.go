package dataplane

// avPairer keeps audio chunk k paired with video frame k across stalls.
//
// A/V sync in the pump is index pairing: the audio pipe is continuous PCM
// cut into one chunk per field (AudioPipeReader), so chunk k is the audio
// for video frame k regardless of when either arrives. Pairing therefore
// breaks only when a tick advances one stream and not the other:
//
//   - A duplicate-field tick (video underrun) shows no new frame, so it
//     must not consume audio either; otherwise audio permanently leads
//     video by one field per duplicate. Both freeze together instead, as
//     any player does on a stall.
//   - A video frame shown before its chunk arrived (audio stalled, e.g. the
//     separate audio input on the dual-input path) leaves that chunk stale
//     when it turns up. It is dropped rather than played a field late,
//     which would otherwise make audio lag video from then on.
//
// Owned by the tick goroutine.
type avPairer struct {
	debt        int    // chunks owed to frames already shown without audio
	resyncDrops uint64 // stale chunks dropped to repay debt
}

// next returns the audio chunk to queue for this tick, or nil. videoAdvanced
// is false on duplicate-field ticks. Chunks come from prebuf first, then ch
// (see pullAudioChunk).
func (p *avPairer) next(videoAdvanced bool, prebuf *[][]byte, ch <-chan []byte) []byte {
	if !videoAdvanced {
		return nil
	}
	for {
		pcm := pullAudioChunk(prebuf, ch)
		if len(pcm) == 0 {
			p.debt++
			return nil
		}
		if p.debt == 0 {
			return pcm
		}
		p.debt--
		p.resyncDrops++
	}
}
