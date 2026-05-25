package chassis

// VolumeViewer is the read-only source for the live global output volume.
// *core.Manager satisfies this structurally via OutputVolume(). Tests use
// fakes. When nil, snapshots fall back to startup config.
type VolumeViewer interface {
	OutputVolume() int
}
