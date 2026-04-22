package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/idio-sync/MiSTer_GroovyRelay/internal/fakemister"
	"github.com/idio-sync/MiSTer_GroovyRelay/internal/groovy"
)

func main() {
	addr := flag.String("addr", ":32100", "UDP listen address")
	outDir := flag.String("out", "./fake-mister-dumps", "dump output directory")
	pngEvery := flag.Int("png-every", 60, "write a PNG every N fields (<=0 disables)")
	flag.Parse()

	l, err := fakemister.NewListener(*addr)
	if err != nil {
		slog.Error("listen", "err", err)
		os.Exit(1)
	}
	defer l.Close()
	slog.Info("fake-mister listening", "addr", l.Addr().String(), "out", *outDir)

	rec := fakemister.NewRecorder()
	dumper := fakemister.NewDumper(*outDir, *pngEvery)

	events := make(chan fakemister.Command, 64)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Session state carried across events. INIT fixes RGB mode + audio config;
	// SWITCHRES fixes the full-frame modeline, from which one-field payload
	// dimensions are derived.
	var (
		initPayload *fakemister.InitPayload
		modeline    *fakemister.SwitchresPayload
		sampleRate  int
		channels    int
	)

	// RunWithFields delivers already-reassembled BLIT and AUDIO payloads via
	// the FieldEvent and AudioEvent channels (see Task 3.7). Unknown-mode
	// datagrams arrive via events as parsed Commands.
	fieldEvents := make(chan fakemister.FieldEvent, 8)
	audioEvents := make(chan fakemister.AudioEvent, 32)
	go l.RunWithFields(events, fieldEvents, audioEvents, func() uint32 {
		return fieldSize(modeline, initPayload)
	})

	for {
		select {
		case <-ctx.Done():
			dumper.CloseAudio(sampleRate, channels)
			snap := rec.Snapshot()
			fmt.Printf("\n=== Session Summary ===\n")
			for t, n := range snap.Counts {
				fmt.Printf("  cmd %d: %d\n", t, n)
			}
			fmt.Printf("  audio bytes: %d\n", snap.AudioBytes)
			return
		case cmd := <-events:
			rec.Record(cmd)
			switch cmd.Type {
			case groovy.CmdInit:
				initPayload = cmd.Init
				sampleRate = sampleRateFromCode(cmd.Init.SoundRate)
				channels = int(cmd.Init.SoundChan)
				if sampleRate > 0 && channels > 0 {
					dumper.StartAudio(sampleRate, channels)
				}
			case groovy.CmdSwitchres:
				modeline = cmd.Switchres
			case groovy.CmdClose:
				dumper.CloseAudio(sampleRate, channels)
			}
		case fe := <-fieldEvents:
			payload := fe.Payload
			if fe.Header.Compressed {
				raw, err := groovy.LZ4Decompress(payload, int(fieldSize(modeline, initPayload)))
				if err != nil {
					slog.Warn("lz4 decompress failed", "err", err, "frame", fe.Header.Frame)
					continue
				}
				payload = raw
			}
			w, h, _ := fieldDims(modeline)
			dumper.MaybeDumpField(fe.Header.Frame, w, h, payload)
		case ae := <-audioEvents:
			dumper.WriteAudio(ae.PCM)
		}
	}
}

// fieldSize returns the expected reassembled bytes for one BLIT field payload,
// derived from INIT rgbMode and SWITCHRES hActive × one-field vActive.
func fieldSize(ml *fakemister.SwitchresPayload, init *fakemister.InitPayload) uint32 {
	if ml == nil {
		return 0
	}
	bpp := 3
	if init != nil {
		switch init.RGBMode {
		case groovy.RGBMode8888:
			bpp = 4
		case groovy.RGBMode565:
			bpp = 2
		}
	}
	return uint32(groovy.FieldPayloadBytes(ml.HActive, ml.VActive, ml.Interlace, bpp))
}

func fieldDims(ml *fakemister.SwitchresPayload) (w, h int, ok bool) {
	if ml == nil {
		return 0, 0, false
	}
	return int(ml.HActive), groovy.FieldLines(ml.VActive, ml.Interlace), true
}

func sampleRateFromCode(code byte) int {
	switch code {
	case groovy.AudioRate22050:
		return 22050
	case groovy.AudioRate44100:
		return 44100
	case groovy.AudioRate48000:
		return 48000
	}
	return 0
}
