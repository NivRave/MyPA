// Package docgen gathers every generated region of the docs (SYSTEM.md, README.md) in one map:
// the tool catalogue from the ToolModule declarations (P5-5) and the channel capability matrix
// from the adapters the app registers (V4-34). `go run ./cmd/devtools/gendocs` writes them and
// TestDocsAreUpToDate fails when a committed doc differs.
package docgen

import (
	"fmt"

	"github.com/nivik/mypa/internal/channel"
	"github.com/nivik/mypa/internal/channel/adapters"
	"github.com/nivik/mypa/internal/toolcatalog"
)

// ChannelMatrixRegion is the block region of SYSTEM.md that holds the capability matrix.
const ChannelMatrixRegion = "channel-matrix"

// DocFiles are the repository files (relative to the module root) containing generated regions.
var DocFiles = toolcatalog.DocFiles

// Regions returns the content of every generated region, by marker name.
func Regions() (map[string]string, error) {
	regions := toolcatalog.Build().Regions()
	matrix, err := ChannelMatrix()
	if err != nil {
		return nil, err
	}
	if _, dup := regions[ChannelMatrixRegion]; dup {
		return nil, fmt.Errorf("docgen: region %q is generated twice", ChannelMatrixRegion)
	}
	regions[ChannelMatrixRegion] = matrix
	return regions, nil
}

// ChannelMatrix renders the capabilities of the adapters the app registers, in registration
// order, as an unconfigured install declares them: its settings loaded from an empty environment,
// so it has no public address (INGRESS=none, V4-40) and an adapter that can receive without one
// shows that mode. An adapter with several modes (V4-41) gets a column for each, built from those
// settings with only its mode setting changed.
func ChannelMatrix() (string, error) {
	cfg := adapters.LoadConfig(func(string) string { return "" })
	reg, err := adapters.Build(cfg)
	if err != nil {
		return "", err
	}
	var columns []channel.Adapter
	for _, a := range reg.All() {
		modes := adapters.Modes()[a.Name()]
		if len(modes) == 0 {
			columns = append(columns, a)
			continue
		}
		for _, mode := range modes {
			modeCfg, err := adapters.InMode(cfg, a.Name(), mode)
			if err != nil {
				return "", err
			}
			modeReg, err := adapters.Build(modeCfg)
			if err != nil {
				return "", err
			}
			inMode, _ := modeReg.Get(a.Name())
			columns = append(columns, inMode)
		}
	}
	return channel.Matrix(columns), nil
}

// Apply replaces every generated region in doc (see toolcatalog.Apply for the markers).
func Apply(doc string, regions map[string]string) (string, error) {
	return toolcatalog.Apply(doc, regions)
}
