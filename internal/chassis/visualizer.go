package chassis

// VisualizerViewer is the narrow read-only view of the live bridge's
// visualizer mode. *core.Manager satisfies it structurally via
// VisualizerMode(). Tests inject fakes. Mirrors Spec 2's SessionViewer
// pattern.
type VisualizerViewer interface {
	VisualizerMode() string
}

// VisualizerSaver persists a new visualizer mode and refreshes the
// live in-memory bridge config. main.go wires this via a small adapter
// struct over uiserver.BridgeSaver.SaveVisualizerMode so chassis does
// not depend on internal/uiserver. The chassis HTTP handler validates
// the mode before invoking the saver.
type VisualizerSaver interface {
	SaveVisualizerMode(mode string) error
}
